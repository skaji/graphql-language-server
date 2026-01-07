package ls

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

const (
	maxSchemaFiles = 2000
	maxScanDepth   = 8
	maxDirEntries  = 5000
)

var errStopScan = errors.New("stop schema scan")

type scanStats struct {
	fileCount int
}

func newScanStats() *scanStats {
	return &scanStats{}
}

func (s *Server) publishQueryDiagnostics(ctx *glsp.Context, uri protocol.DocumentUri, text string) {
	if s.isSchemaURI(uri) {
		s.state.mu.Lock()
		delete(s.state.queryDiagnostics, uri)
		s.state.mu.Unlock()
		s.publishCombinedDiagnostics(ctx, uri)
		return
	}

	doc := &ast.Source{
		Name:  string(uri),
		Input: text,
	}

	_, err := parser.ParseQuery(doc)
	diagnostics := GqlErrorDiagnostics(err)
	s.state.mu.Lock()
	s.state.queryDiagnostics[uri] = diagnostics
	s.state.mu.Unlock()
	slog.Debug("query diagnostics updated", "uri", uri, "count", len(diagnostics))
	s.publishCombinedDiagnostics(ctx, uri)
}

func (s *Server) loadWorkspaceSchema(ctx *glsp.Context) {
	slog.Debug("loading workspace schema")
	s.state.mu.Lock()
	previousSchema := s.state.schema
	s.state.mu.Unlock()

	sources, uris := s.collectSchemaSources()
	diagnosticsByURI := make(map[protocol.DocumentUri][]protocol.Diagnostic)
	var schema *ast.Schema
	if len(sources) > 0 {
		if _, err := parser.ParseSchemas(sources...); err != nil {
			slog.Debug("schema parse error; skipping validation", "error", err)
			diagnosticsByURI = GqlErrorDiagnosticsByFile(err, uris)
			ensureSchemaDiagnosticEntries(diagnosticsByURI, uris)
			s.state.mu.Lock()
			s.state.schemaDiagnostics = diagnosticsByURI
			s.state.mu.Unlock()
			s.publishAllDiagnostics(ctx)
			return
		}
		loadedSchema, err := gqlparser.LoadSchema(sources...)
		schema = loadedSchema
		if err != nil {
			diagnosticsByURI = GqlErrorDiagnosticsByFile(err, uris)
			if previousSchema != nil {
				slog.Debug("schema validation error; keeping previous schema", "error", err)
				schema = previousSchema
			}
		}
	}

	ensureSchemaDiagnosticEntries(diagnosticsByURI, uris)
	s.state.mu.Lock()
	s.state.schema = schema
	s.state.schemaDiagnostics = diagnosticsByURI
	s.state.schemaURIs = uris
	for uri := range uris {
		delete(s.state.queryDiagnostics, uri)
	}
	s.state.mu.Unlock()
	slog.Debug("schema load complete", "sources", len(sources), "diagnostics", len(diagnosticsByURI))
	if len(diagnosticsByURI) > 0 {
		slogSchemaDiagnostics(diagnosticsByURI)
	}

	s.publishAllDiagnostics(ctx)
}

func (s *Server) collectSchemaSources() ([]*ast.Source, map[protocol.DocumentUri]struct{}) {
	root := ""
	var schemaPaths []string
	s.state.mu.Lock()
	root = s.state.rootPath
	schemaPaths = append(schemaPaths, s.state.schemaPaths...)
	s.state.mu.Unlock()

	if len(schemaPaths) > 0 {
		return collectSchemaSourcesFromPaths(s.state, root, schemaPaths)
	}

	if root == "" {
		return nil, map[protocol.DocumentUri]struct{}{}
	}

	uris := make(map[protocol.DocumentUri]struct{})
	var sources []*ast.Source
	stats := newScanStats()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			name := entry.Name()
			if shouldSkipDir(name) {
				return filepath.SkipDir
			}
			if root != "" && exceedsMaxDepth(root, path) {
				return filepath.SkipDir
			}
			if tooLargeDir(path) {
				slog.Debug("schema scan: skipping large directory", "path", path)
				return filepath.SkipDir
			}
			return nil
		}
		if !isGraphQLFile(path) {
			return nil
		}

		addSchemaSource(s.state, path, uris, &sources)
		stats.fileCount++
		if stats.fileCount >= maxSchemaFiles {
			return errStopScan
		}
		return nil
	})
	if errors.Is(err, errStopScan) {
		slog.Debug("schema scan stopped", "files", stats.fileCount)
	}

	return sources, uris
}

func collectSchemaSourcesFromPaths(state *State, root string, schemaPaths []string) ([]*ast.Source, map[protocol.DocumentUri]struct{}) {
	uris := make(map[protocol.DocumentUri]struct{})
	var sources []*ast.Source
	visited := make(map[string]struct{})
	stats := newScanStats()

	for _, pattern := range schemaPaths {
		for _, path := range expandSchemaPattern(root, pattern) {
			if path == "" {
				continue
			}
			if _, ok := visited[path]; ok {
				continue
			}
			visited[path] = struct{}{}

			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			if info.IsDir() {
				sources = append(sources, collectSchemaSourcesFromDir(state, path, uris, stats)...)
				continue
			}
			if !isGraphQLFile(path) {
				continue
			}
			addSchemaSource(state, path, uris, &sources)
			stats.fileCount++
			if stats.fileCount >= maxSchemaFiles {
				slog.Debug("schema scan stopped", "files", stats.fileCount)
				return sources, uris
			}
		}
	}

	return sources, uris
}

func collectSchemaSourcesFromDir(state *State, root string, uris map[protocol.DocumentUri]struct{}, stats *scanStats) []*ast.Source {
	var sources []*ast.Source
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			name := entry.Name()
			if shouldSkipDir(name) {
				return filepath.SkipDir
			}
			if root != "" && exceedsMaxDepth(root, path) {
				return filepath.SkipDir
			}
			if tooLargeDir(path) {
				slog.Debug("schema scan: skipping large directory", "path", path)
				return filepath.SkipDir
			}
			return nil
		}
		if !isGraphQLFile(path) {
			return nil
		}
		addSchemaSource(state, path, uris, &sources)
		stats.fileCount++
		if stats.fileCount >= maxSchemaFiles {
			return errStopScan
		}
		return nil
	})
	if errors.Is(err, errStopScan) {
		slog.Debug("schema scan stopped", "files", stats.fileCount)
	}
	return sources
}

func addSchemaSource(state *State, path string, uris map[protocol.DocumentUri]struct{}, sources *[]*ast.Source) {
	uri := pathToURI(path)
	if _, ok := uris[uri]; ok {
		return
	}
	content, ok := readDocument(state, uri, path)
	if !ok {
		return
	}
	uris[uri] = struct{}{}
	*sources = append(*sources, &ast.Source{
		Name:  string(uri),
		Input: content,
	})
}

func shouldSkipDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "vendor", "dist", "build":
		return true
	default:
		return false
	}
}

func exceedsMaxDepth(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return false
	}
	depth := strings.Count(rel, string(os.PathSeparator)) + 1
	return depth > maxScanDepth
}

func tooLargeDir(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	return len(entries) > maxDirEntries
}

func ensureSchemaDiagnosticEntries(diags map[protocol.DocumentUri][]protocol.Diagnostic, uris map[protocol.DocumentUri]struct{}) {
	for uri := range uris {
		if _, ok := diags[uri]; !ok {
			diags[uri] = nil
		}
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func slogSchemaDiagnostics(diags map[protocol.DocumentUri][]protocol.Diagnostic) {
	const maxPerFile = 10
	for uri, list := range diags {
		if len(list) == 0 {
			continue
		}
		msgs := make([]string, 0, minInt(len(list), maxPerFile))
		for i, diag := range list {
			if i >= maxPerFile {
				msgs = append(msgs, "...")
				break
			}
			msgs = append(msgs, diag.Message)
		}
		slog.Debug("schema diagnostics", "uri", uri, "count", len(list), "messages", msgs)
	}
}

func (s *Server) publishAllDiagnostics(ctx *glsp.Context) {
	s.state.mu.Lock()
	uris := make(map[protocol.DocumentUri]struct{})
	for uri := range s.state.queryDiagnostics {
		uris[uri] = struct{}{}
	}
	for uri := range s.state.schemaDiagnostics {
		uris[uri] = struct{}{}
	}
	s.state.mu.Unlock()

	for uri := range uris {
		s.publishCombinedDiagnostics(ctx, uri)
	}
}

func (s *Server) publishCombinedDiagnostics(ctx *glsp.Context, uri protocol.DocumentUri) {
	s.state.mu.Lock()
	queryDiagnostics := s.state.queryDiagnostics[uri]
	schemaDiagnostics := s.state.schemaDiagnostics[uri]
	s.state.mu.Unlock()

	combined := make([]protocol.Diagnostic, 0, len(queryDiagnostics)+len(schemaDiagnostics))
	combined = append(combined, queryDiagnostics...)
	combined = append(combined, schemaDiagnostics...)
	notifyDiagnostics(ctx, uri, combined)
}

func notifyDiagnostics(ctx *glsp.Context, uri protocol.DocumentUri, diagnostics []protocol.Diagnostic) {
	params := protocol.PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diagnostics,
	}
	ctx.Notify(protocol.ServerTextDocumentPublishDiagnostics, params)
}
