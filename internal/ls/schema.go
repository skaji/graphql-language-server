package ls

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

func (s *Server) publishQueryDiagnostics(context *glsp.Context, uri protocol.DocumentUri, text string) {
	if isSchemaURI(uri) {
		s.state.mu.Lock()
		delete(s.state.queryDiagnostics, uri)
		s.state.mu.Unlock()
		s.publishCombinedDiagnostics(context, uri)
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
	s.publishCombinedDiagnostics(context, uri)
}

func (s *Server) loadWorkspaceSchema(context *glsp.Context) {
	sources, uris := s.collectSchemaSources()
	diagnosticsByURI := make(map[protocol.DocumentUri][]protocol.Diagnostic)
	var schema *ast.Schema
	if len(sources) > 0 {
		loadedSchema, err := gqlparser.LoadSchema(sources...)
		schema = loadedSchema
		if err != nil {
			diagnosticsByURI = GqlErrorDiagnosticsByFile(err, uris)
		}
	}

	s.state.mu.Lock()
	s.state.schema = schema
	s.state.schemaDiagnostics = diagnosticsByURI
	s.state.mu.Unlock()

	s.publishAllDiagnostics(context)
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

		addSchemaSource(s.state, path, uris, &sources)
		return nil
	})

	return sources, uris
}

func collectSchemaSourcesFromPaths(state *State, root string, schemaPaths []string) ([]*ast.Source, map[protocol.DocumentUri]struct{}) {
	uris := make(map[protocol.DocumentUri]struct{})
	var sources []*ast.Source
	visited := make(map[string]struct{})

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
				sources = append(sources, collectSchemaSourcesFromDir(state, path, uris)...)
				continue
			}
			if !isGraphQLFile(path) {
				continue
			}
			addSchemaSource(state, path, uris, &sources)
		}
	}

	return sources, uris
}

func collectSchemaSourcesFromDir(state *State, root string, uris map[protocol.DocumentUri]struct{}) []*ast.Source {
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
		if !isGraphQLFile(path) {
			return nil
		}
		addSchemaSource(state, path, uris, &sources)
		return nil
	})
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

func (s *Server) publishAllDiagnostics(context *glsp.Context) {
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
		s.publishCombinedDiagnostics(context, uri)
	}
}

func (s *Server) publishCombinedDiagnostics(context *glsp.Context, uri protocol.DocumentUri) {
	s.state.mu.Lock()
	queryDiagnostics := s.state.queryDiagnostics[uri]
	schemaDiagnostics := s.state.schemaDiagnostics[uri]
	s.state.mu.Unlock()

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
