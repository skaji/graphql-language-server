package main

import (
	"errors"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/tliron/glsp/server"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vektah/gqlparser/v2/parser"
)

var serverName = "graphql-language-server"

var (
	version string = "0.0.1"
	handler protocol.Handler
	state   = newServerState()
)

type serverState struct {
	mu                sync.Mutex
	docs              map[protocol.DocumentUri]string
	queryDiagnostics  map[protocol.DocumentUri][]protocol.Diagnostic
	schemaDiagnostics map[protocol.DocumentUri][]protocol.Diagnostic
	rootPath          string
	schema            *ast.Schema
}

func newServerState() *serverState {
	return &serverState{
		docs:              make(map[protocol.DocumentUri]string),
		queryDiagnostics:  make(map[protocol.DocumentUri][]protocol.Diagnostic),
		schemaDiagnostics: make(map[protocol.DocumentUri][]protocol.Diagnostic),
	}
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	handler = protocol.Handler{
		Initialize:            initialize,
		Shutdown:              shutdown,
		SetTrace:              setTrace,
		TextDocumentDidOpen:   didOpen,
		TextDocumentDidChange: didChange,
		TextDocumentDidClose:  didClose,
	}

	server := server.NewServer(&handler, serverName, false)
	if err := server.RunStdio(); err != nil {
		slog.Error("server failed", "error", err)
	}
}

func initialize(context *glsp.Context, params *protocol.InitializeParams) (any, error) {
	capabilities := handler.CreateServerCapabilities()
	syncKind := protocol.TextDocumentSyncKindFull
	capabilities.TextDocumentSync = &protocol.TextDocumentSyncOptions{
		OpenClose: &protocol.True,
		Change:    &syncKind,
	}

	rootPath := ""
	if params.RootURI != nil {
		rootPath = uriToPath(*params.RootURI)
	} else if params.RootPath != nil {
		rootPath = *params.RootPath
	}
	state.mu.Lock()
	state.rootPath = rootPath
	state.mu.Unlock()

	return protocol.InitializeResult{
		Capabilities: capabilities,
		ServerInfo: &protocol.InitializeResultServerInfo{
			Name:    serverName,
			Version: &version,
		},
	}, nil
}

func shutdown(context *glsp.Context) error {
	protocol.SetTraceValue(protocol.TraceValueOff)
	return nil
}

func setTrace(context *glsp.Context, params *protocol.SetTraceParams) error {
	protocol.SetTraceValue(params.Value)
	return nil
}

func didOpen(context *glsp.Context, params *protocol.DidOpenTextDocumentParams) error {
	state.mu.Lock()
	state.docs[params.TextDocument.URI] = params.TextDocument.Text
	state.mu.Unlock()

	publishQueryDiagnostics(context, params.TextDocument.URI, params.TextDocument.Text)
	loadWorkspaceSchema(context)
	return nil
}

func didChange(context *glsp.Context, params *protocol.DidChangeTextDocumentParams) error {
	if len(params.ContentChanges) == 0 {
		return nil
	}

	var text string
	switch change := params.ContentChanges[len(params.ContentChanges)-1].(type) {
	case protocol.TextDocumentContentChangeEventWhole:
		text = change.Text
	case protocol.TextDocumentContentChangeEvent:
		text = change.Text
	default:
		return nil
	}

	state.mu.Lock()
	state.docs[params.TextDocument.URI] = text
	state.mu.Unlock()

	publishQueryDiagnostics(context, params.TextDocument.URI, text)
	loadWorkspaceSchema(context)
	return nil
}

func didClose(context *glsp.Context, params *protocol.DidCloseTextDocumentParams) error {
	state.mu.Lock()
	delete(state.docs, params.TextDocument.URI)
	delete(state.queryDiagnostics, params.TextDocument.URI)
	state.mu.Unlock()

	loadWorkspaceSchema(context)
	publishCombinedDiagnostics(context, params.TextDocument.URI)
	return nil
}

func publishQueryDiagnostics(context *glsp.Context, uri protocol.DocumentUri, text string) {
	if isSchemaURI(uri) {
		state.mu.Lock()
		delete(state.queryDiagnostics, uri)
		state.mu.Unlock()
		publishCombinedDiagnostics(context, uri)
		return
	}

	doc := &ast.Source{
		Name:  string(uri),
		Input: text,
	}

	_, err := parser.ParseQuery(doc)
	diagnostics := gqlErrorDiagnostics(err)
	state.mu.Lock()
	state.queryDiagnostics[uri] = diagnostics
	state.mu.Unlock()
	publishCombinedDiagnostics(context, uri)
}

func loadWorkspaceSchema(context *glsp.Context) {
	sources, uris := collectSchemaSources()
	diagnosticsByURI := make(map[protocol.DocumentUri][]protocol.Diagnostic)
	var schema *ast.Schema
	if len(sources) > 0 {
		loadedSchema, err := gqlparser.LoadSchema(sources...)
		schema = loadedSchema
		if err != nil {
			diagnosticsByURI = gqlErrorDiagnosticsByFile(err, uris)
		}
	}

	state.mu.Lock()
	state.schema = schema
	state.schemaDiagnostics = diagnosticsByURI
	state.mu.Unlock()

	publishAllDiagnostics(context)
}

func collectSchemaSources() ([]*ast.Source, map[protocol.DocumentUri]struct{}) {
	root := ""
	state.mu.Lock()
	root = state.rootPath
	state.mu.Unlock()

	if root == "" {
		return nil, map[protocol.DocumentUri]struct{}{}
	}

	uris := make(map[protocol.DocumentUri]struct{})
	var sources []*ast.Source
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			name := entry.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !isSchemaPath(path) {
			return nil
		}

		uri := pathToURI(path)
		content, ok := readDocument(uri, path)
		if !ok {
			return nil
		}
		uris[uri] = struct{}{}
		sources = append(sources, &ast.Source{
			Name:  string(uri),
			Input: content,
		})
		return nil
	})

	return sources, uris
}

func readDocument(uri protocol.DocumentUri, path string) (string, bool) {
	state.mu.Lock()
	if text, ok := state.docs[uri]; ok {
		state.mu.Unlock()
		return text, true
	}
	state.mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

func isSchemaPath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".graphqls" {
		return true
	}
	if ext != ".graphql" {
		return false
	}
	base := strings.ToLower(filepath.Base(path))
	return strings.Contains(base, "schema")
}

func isSchemaURI(uri protocol.DocumentUri) bool {
	path := uriToPath(uri)
	if path == "" {
		return false
	}
	return isSchemaPath(path)
}

func publishAllDiagnostics(context *glsp.Context) {
	state.mu.Lock()
	uris := make(map[protocol.DocumentUri]struct{})
	for uri := range state.queryDiagnostics {
		uris[uri] = struct{}{}
	}
	for uri := range state.schemaDiagnostics {
		uris[uri] = struct{}{}
	}
	state.mu.Unlock()

	for uri := range uris {
		publishCombinedDiagnostics(context, uri)
	}
}

func publishCombinedDiagnostics(context *glsp.Context, uri protocol.DocumentUri) {
	state.mu.Lock()
	queryDiagnostics := state.queryDiagnostics[uri]
	schemaDiagnostics := state.schemaDiagnostics[uri]
	state.mu.Unlock()

	combined := make([]protocol.Diagnostic, 0, len(queryDiagnostics)+len(schemaDiagnostics))
	combined = append(combined, queryDiagnostics...)
	combined = append(combined, schemaDiagnostics...)
	notifyDiagnostics(context, uri, combined)
}

func notifyDiagnostics(context *glsp.Context, uri protocol.DocumentUri, diagnostics []protocol.Diagnostic) {
	params := protocol.PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diagnostics,
	}
	context.Notify(protocol.ServerTextDocumentPublishDiagnostics, params)
}

func gqlErrorDiagnostics(err error) []protocol.Diagnostic {
	if err == nil {
		return nil
	}

	var list gqlerror.List
	if errors.As(err, &list) {
		return diagnosticsFromList(list)
	}

	var gqlErr *gqlerror.Error
	if errors.As(err, &gqlErr) {
		return diagnosticsFromList(gqlerror.List{gqlErr})
	}

	return diagnosticsFromList(gqlerror.List{gqlerror.Wrap(err)})
}

func gqlErrorDiagnosticsByFile(err error, knownURIs map[protocol.DocumentUri]struct{}) map[protocol.DocumentUri][]protocol.Diagnostic {
	byURI := make(map[protocol.DocumentUri][]protocol.Diagnostic)
	if err == nil {
		return byURI
	}

	var list gqlerror.List
	if errors.As(err, &list) {
		addDiagnosticsByFile(byURI, list, knownURIs)
		return byURI
	}

	var gqlErr *gqlerror.Error
	if errors.As(err, &gqlErr) {
		addDiagnosticsByFile(byURI, gqlerror.List{gqlErr}, knownURIs)
		return byURI
	}

	addDiagnosticsByFile(byURI, gqlerror.List{gqlerror.Wrap(err)}, knownURIs)
	return byURI
}

func addDiagnosticsByFile(byURI map[protocol.DocumentUri][]protocol.Diagnostic, list gqlerror.List, knownURIs map[protocol.DocumentUri]struct{}) {
	for _, gqlErr := range list {
		uri := gqlErrorURI(gqlErr)
		if uri == "" {
			uri = firstKnownURI(knownURIs)
		}
		if uri == "" {
			continue
		}
		byURI[uri] = append(byURI[uri], gqlErrorToDiagnostic(gqlErr))
	}
}

func gqlErrorURI(err *gqlerror.Error) protocol.DocumentUri {
	if err == nil {
		return ""
	}
	if err.Extensions == nil {
		return ""
	}
	if file, ok := err.Extensions["file"].(string); ok && file != "" {
		if strings.HasPrefix(file, "file://") {
			return protocol.DocumentUri(file)
		}
		return pathToURI(file)
	}
	return ""
}

func firstKnownURI(knownURIs map[protocol.DocumentUri]struct{}) protocol.DocumentUri {
	for uri := range knownURIs {
		return uri
	}
	return ""
}

func diagnosticsFromList(list gqlerror.List) []protocol.Diagnostic {
	diagnostics := make([]protocol.Diagnostic, 0, len(list))
	for _, gqlErr := range list {
		diagnostics = append(diagnostics, gqlErrorToDiagnostic(gqlErr))
	}
	return diagnostics
}

func gqlErrorToDiagnostic(err *gqlerror.Error) protocol.Diagnostic {
	startLine, startChar := 0, 0
	if len(err.Locations) > 0 {
		startLine = err.Locations[0].Line - 1
		startChar = err.Locations[0].Column - 1
	}

	if startLine < 0 {
		startLine = 0
	}
	if startChar < 0 {
		startChar = 0
	}

	start := protocol.Position{
		Line:      protocol.UInteger(startLine),
		Character: protocol.UInteger(startChar),
	}
	end := protocol.Position{
		Line:      protocol.UInteger(startLine),
		Character: protocol.UInteger(startChar + 1),
	}

	severity := protocol.DiagnosticSeverityError
	return protocol.Diagnostic{
		Range: protocol.Range{
			Start: start,
			End:   end,
		},
		Severity: &severity,
		Message:  err.Message,
		Source:   &serverName,
	}
}

func uriToPath(uri protocol.DocumentUri) string {
	parsed, err := url.Parse(string(uri))
	if err != nil {
		return ""
	}
	if parsed.Scheme != "file" {
		return ""
	}
	path, err := url.PathUnescape(parsed.Path)
	if err != nil {
		return ""
	}
	return filepath.FromSlash(path)
}

func pathToURI(path string) protocol.DocumentUri {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return protocol.DocumentUri(path)
	}
	absPath = filepath.ToSlash(absPath)
	u := url.URL{
		Scheme: "file",
		Path:   absPath,
	}
	return protocol.DocumentUri(u.String())
}
