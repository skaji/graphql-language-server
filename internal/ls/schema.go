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
	slog.Debug("query diagnostics updated", "uri", uri, "count", len(diagnostics))
	s.publishCombinedDiagnostics(context, uri)
}

func (s *Server) loadWorkspaceSchema(context *glsp.Context) {
	slog.Debug("loading workspace schema")
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
	slog.Debug("schema load complete", "sources", len(sources), "diagnostics", len(diagnosticsByURI))

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
