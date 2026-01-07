package ls

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

func TestInitializeSetsStateAndCapabilities(t *testing.T) {
	s := New()
	root := t.TempDir()
	rootURI := pathToURI(root)

	result, err := s.initialize(nil, &protocol.InitializeParams{
		RootURI: &rootURI,
		InitializationOptions: map[string]any{
			"schemaPaths": []string{"schema/**/*.graphqls"},
		},
	})
	if err != nil {
		t.Fatalf("initialize error: %v", err)
	}

	initResult, ok := result.(protocol.InitializeResult)
	if !ok {
		t.Fatalf("unexpected result type: %T", result)
	}

	opts, ok := initResult.Capabilities.TextDocumentSync.(*protocol.TextDocumentSyncOptions)
	if !ok || opts.Change == nil || *opts.Change != protocol.TextDocumentSyncKindFull {
		t.Fatalf("expected full text sync capabilities")
	}

	s.state.mu.Lock()
	gotRoot := s.state.rootPath
	gotPaths := append([]string(nil), s.state.schemaPaths...)
	s.state.mu.Unlock()

	if gotRoot != filepath.Clean(root) {
		t.Fatalf("expected root %q, got %q", root, gotRoot)
	}
	if len(gotPaths) != 1 || gotPaths[0] != "schema/**/*.graphqls" {
		t.Fatalf("unexpected schema paths: %v", gotPaths)
	}
}

func TestSchemaDiscoveryIncludesGraphQLFiles(t *testing.T) {
	s := New()
	root := t.TempDir()
	schemaPath := filepath.Join(root, "schema.graphql")
	opsPath := filepath.Join(root, "query.graphql")
	if err := os.WriteFile(schemaPath, []byte("type Query { name: String }"), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	if err := os.WriteFile(opsPath, []byte("{ name }"), 0o644); err != nil {
		t.Fatalf("write query: %v", err)
	}

	s.state.mu.Lock()
	s.state.rootPath = root
	s.state.schemaPaths = nil
	s.state.mu.Unlock()

	sources, _ := s.collectSchemaSources()
	if len(sources) == 0 {
		t.Fatal("expected schema sources")
	}
}

func TestSchemaParseErrorSkipsValidation(t *testing.T) {
	s := New()
	root := t.TempDir()
	schemaPath := filepath.Join(root, "schema.graphql")
	if err := os.WriteFile(schemaPath, []byte("type Query {"), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	previous := gqlparser.MustLoadSchema(&ast.Source{
		Input: "type Query { ok: String }\n",
	})

	s.state.mu.Lock()
	s.state.rootPath = root
	s.state.schemaPaths = nil
	s.state.schema = previous
	s.state.mu.Unlock()

	context := &glsp.Context{
		Notify: func(_ string, _ any) {},
	}
	s.loadWorkspaceSchema(context)

	s.state.mu.Lock()
	current := s.state.schema
	diagnostics := s.state.schemaDiagnostics
	s.state.mu.Unlock()

	if current != previous {
		t.Fatal("expected schema to remain unchanged on parse error")
	}
	if len(diagnostics) == 0 {
		t.Fatal("expected parse diagnostics")
	}
}

func TestDidOpenChangeClosePublishesDiagnostics(t *testing.T) {
	s := New()
	uri := protocol.DocumentUri("file:///tmp/query.graphql")

	var published []protocol.PublishDiagnosticsParams
	context := &glsp.Context{
		Notify: func(method string, params any) {
			if method != string(protocol.ServerTextDocumentPublishDiagnostics) {
				return
			}
			value, ok := params.(protocol.PublishDiagnosticsParams)
			if !ok {
				t.Fatalf("unexpected diagnostics params type: %T", params)
			}
			published = append(published, value)
		},
	}

	if err := s.didOpen(context, &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        uri,
			LanguageID: "graphql",
			Version:    1,
			Text:       "{",
		},
	}); err != nil {
		t.Fatalf("didOpen error: %v", err)
	}
	if len(published) == 0 || len(published[len(published)-1].Diagnostics) == 0 {
		t.Fatal("expected diagnostics on didOpen")
	}

	if err := s.didChange(context, &protocol.DidChangeTextDocumentParams{
		TextDocument: protocol.VersionedTextDocumentIdentifier{
			TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri},
			Version:                2,
		},
		ContentChanges: []any{
			protocol.TextDocumentContentChangeEventWhole{
				Text: "{ __typename }",
			},
		},
	}); err != nil {
		t.Fatalf("didChange error: %v", err)
	}
	if len(published) == 0 || len(published[len(published)-1].Diagnostics) != 0 {
		t.Fatal("expected no diagnostics on didChange")
	}

	if err := s.didClose(context, &protocol.DidCloseTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
	}); err != nil {
		t.Fatalf("didClose error: %v", err)
	}
	if len(published) == 0 || len(published[len(published)-1].Diagnostics) != 0 {
		t.Fatal("expected cleared diagnostics on didClose")
	}
}

func TestDidSaveTriggersSchemaLoad(t *testing.T) {
	s := New()
	dir := t.TempDir()
	path := filepath.Join(dir, "schema.graphql")
	uri := pathToURI(path)
	schema := "type Query { name: String }\n"

	if err := os.WriteFile(path, []byte(schema), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	s.state.mu.Lock()
	s.state.docs[uri] = schema
	s.state.rootPath = dir
	s.state.schemaPaths = []string{"schema.graphql"}
	s.state.mu.Unlock()

	context := &glsp.Context{
		Notify: func(_ string, _ any) {},
	}
	if err := s.didSave(context, &protocol.DidSaveTextDocumentParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: uri},
	}); err != nil {
		t.Fatalf("didSave error: %v", err)
	}

	s.state.mu.Lock()
	loaded := s.state.schema != nil
	s.state.mu.Unlock()
	if !loaded {
		t.Fatal("expected schema to be loaded on save")
	}
}

func TestDidChangeIncrementalUpdatesText(t *testing.T) {
	s := New()
	uri := protocol.DocumentUri("file:///tmp/schema.graphql")
	initial := "type Query {\n  foo: Foo\n}\n"

	s.state.mu.Lock()
	s.state.docs[uri] = initial
	s.state.mu.Unlock()

	context := &glsp.Context{
		Notify: func(_ string, _ any) {},
	}

	err := s.didChange(context, &protocol.DidChangeTextDocumentParams{
		TextDocument: protocol.VersionedTextDocumentIdentifier{
			TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri},
			Version:                2,
		},
		ContentChanges: []any{
			protocol.TextDocumentContentChangeEvent{
				Range: &protocol.Range{
					Start: protocol.Position{Line: 1, Character: 2},
					End:   protocol.Position{Line: 1, Character: 5},
				},
				Text: "bar",
			},
		},
	})
	if err != nil {
		t.Fatalf("didChange error: %v", err)
	}

	s.state.mu.Lock()
	updated := s.state.docs[uri]
	s.state.mu.Unlock()

	if !strings.Contains(updated, "bar: Foo") {
		t.Fatalf("expected updated text, got %q", updated)
	}
}

func TestHoverHandler(t *testing.T) {
	s := New()
	uri := protocol.DocumentUri("file:///tmp/query.graphql")
	query := "{ user { name } }"
	schema := gqlparser.MustLoadSchema(&ast.Source{
		Input: "type Query { user: User }\n type User { name: String }\n",
	})

	s.state.mu.Lock()
	s.state.schema = schema
	s.state.docs[uri] = query
	s.state.mu.Unlock()

	hover, err := s.hover(nil, &protocol.HoverParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri},
			Position: protocol.Position{
				Line:      0,
				Character: 2,
			},
		},
	})
	if err != nil {
		t.Fatalf("hover error: %v", err)
	}
	if hover == nil {
		t.Fatal("expected hover result")
	}
}

func TestDefinitionHandlerField(t *testing.T) {
	s := New()
	queryURI := protocol.DocumentUri("file:///tmp/query.graphql")
	schemaURI := protocol.DocumentUri("file:///tmp/schema.graphqls")
	query := "{ user { name } }"

	schema := gqlparser.MustLoadSchema(&ast.Source{
		Name:  string(schemaURI),
		Input: "type Query { user: User }\n type User { name: String }\n",
	})

	s.state.mu.Lock()
	s.state.schema = schema
	s.state.docs[queryURI] = query
	s.state.mu.Unlock()

	result, err := s.definition(nil, &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: queryURI},
			Position: protocol.Position{
				Line:      0,
				Character: 2,
			},
		},
	})
	if err != nil {
		t.Fatalf("definition error: %v", err)
	}
	locations, ok := result.([]protocol.Location)
	if !ok || len(locations) == 0 {
		t.Fatalf("expected locations, got %T", result)
	}
	if locations[0].URI != schemaURI {
		t.Fatalf("expected schema URI %s, got %s", schemaURI, locations[0].URI)
	}
}

func TestDefinitionHandlerType(t *testing.T) {
	s := New()
	schemaURI := protocol.DocumentUri("file:///tmp/schema.graphqls")
	schemaText := "type Query { user: User }\n type User { name: String }\n"
	schema := gqlparser.MustLoadSchema(&ast.Source{
		Name:  string(schemaURI),
		Input: schemaText,
	})

	s.state.mu.Lock()
	s.state.schema = schema
	s.state.docs[schemaURI] = schemaText
	s.state.mu.Unlock()

	result, err := s.definition(nil, &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: schemaURI},
			Position: protocol.Position{
				Line:      0,
				Character: 6,
			},
		},
	})
	if err != nil {
		t.Fatalf("definition error: %v", err)
	}
	locations, ok := result.([]protocol.Location)
	if !ok || len(locations) == 0 {
		t.Fatalf("expected locations, got %T", result)
	}
	if locations[0].URI != schemaURI {
		t.Fatalf("expected schema URI %s, got %s", schemaURI, locations[0].URI)
	}
}

func TestDefinitionHandlerSchemaReference(t *testing.T) {
	s := New()
	schemaURI := protocol.DocumentUri("file:///tmp/schema.graphql")
	schemaText := "type Query { foo: Foo }\n type Foo { a: Int }\n"
	schema := gqlparser.MustLoadSchema(&ast.Source{
		Name:  string(schemaURI),
		Input: schemaText,
	})

	s.state.mu.Lock()
	s.state.schema = schema
	s.state.docs[schemaURI] = schemaText
	s.state.mu.Unlock()

	result, err := s.definition(nil, &protocol.DefinitionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: schemaURI},
			Position: protocol.Position{
				Line:      0,
				Character: 20,
			},
		},
	})
	if err != nil {
		t.Fatalf("definition error: %v", err)
	}
	locations, ok := result.([]protocol.Location)
	if !ok || len(locations) == 0 {
		t.Fatalf("expected locations, got %T", result)
	}
	if locations[0].URI != schemaURI {
		t.Fatalf("expected schema URI %s, got %s", schemaURI, locations[0].URI)
	}
}

func TestCompletionFields(t *testing.T) {
	s := New()
	queryURI := protocol.DocumentUri("file:///tmp/query.graphql")
	query := "{ user { name } }"
	schema := gqlparser.MustLoadSchema(&ast.Source{
		Input: "type Query { user(id: ID): User }\n type User { name: String }\n",
	})

	s.state.mu.Lock()
	s.state.schema = schema
	s.state.docs[queryURI] = query
	s.state.mu.Unlock()

	result, err := s.completion(nil, &protocol.CompletionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: queryURI},
			Position: protocol.Position{
				Line:      0,
				Character: 2,
			},
		},
	})
	if err != nil {
		t.Fatalf("completion error: %v", err)
	}
	items, ok := result.([]protocol.CompletionItem)
	if !ok || len(items) == 0 {
		t.Fatalf("expected completion items, got %T", result)
	}
	if !hasCompletionLabel(items, "user") {
		t.Fatalf("expected user completion, got %v", completionLabels(items))
	}
	item, ok := findCompletionItem(items, "user")
	if !ok {
		t.Fatalf("expected user completion, got %v", completionLabels(items))
	}
	if item.InsertText == nil || *item.InsertText == "" {
		t.Fatal("expected snippet insert text")
	}
	if item.InsertTextFormat == nil || *item.InsertTextFormat != protocol.InsertTextFormatSnippet {
		t.Fatal("expected snippet insert text format")
	}
	doc, ok := item.Documentation.(protocol.MarkupContent)
	if !ok || !strings.Contains(doc.Value, "user(id: ID): User") {
		t.Fatalf("expected documentation signature, got %#v", item.Documentation)
	}
}

func TestCompletionDirectives(t *testing.T) {
	s := New()
	queryURI := protocol.DocumentUri("file:///tmp/query.graphql")
	query := "{ user @ }"
	schema := gqlparser.MustLoadSchema(&ast.Source{
		Input: "type Query { user: String }\n",
	})

	s.state.mu.Lock()
	s.state.schema = schema
	s.state.docs[queryURI] = query
	s.state.mu.Unlock()

	result, err := s.completion(nil, &protocol.CompletionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: queryURI},
			Position: protocol.Position{
				Line:      0,
				Character: 8,
			},
		},
	})
	if err != nil {
		t.Fatalf("completion error: %v", err)
	}
	items, ok := result.([]protocol.CompletionItem)
	if !ok || len(items) == 0 {
		t.Fatalf("expected completion items, got %T", result)
	}
	if !hasCompletionLabel(items, "include") {
		t.Fatalf("expected include directive, got %v", completionLabels(items))
	}
}

func TestCompletionTypeCondition(t *testing.T) {
	s := New()
	queryURI := protocol.DocumentUri("file:///tmp/query.graphql")
	query := "{ ... on }"
	schema := gqlparser.MustLoadSchema(&ast.Source{
		Input: "type Query { user: User }\n type User { name: String }\n",
	})

	s.state.mu.Lock()
	s.state.schema = schema
	s.state.docs[queryURI] = query
	s.state.mu.Unlock()

	prefix := "{ ... on "
	result, err := s.completion(nil, &protocol.CompletionParams{
		TextDocumentPositionParams: protocol.TextDocumentPositionParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: queryURI},
			Position: protocol.Position{
				Line:      0,
				Character: protocol.UInteger(utf8.RuneCountInString(prefix)),
			},
		},
	})
	if err != nil {
		t.Fatalf("completion error: %v", err)
	}
	items, ok := result.([]protocol.CompletionItem)
	if !ok || len(items) == 0 {
		t.Fatalf("expected completion items, got %T", result)
	}
	if !hasCompletionLabel(items, "User") {
		t.Fatalf("expected User completion, got %v", completionLabels(items))
	}
}

func TestShutdownAndSetTrace(t *testing.T) {
	s := New()
	if err := s.setTrace(nil, &protocol.SetTraceParams{Value: protocol.TraceValueVerbose}); err != nil {
		t.Fatalf("setTrace error: %v", err)
	}
	if err := s.shutdown(nil); err != nil {
		t.Fatalf("shutdown error: %v", err)
	}
}

func hasCompletionLabel(items []protocol.CompletionItem, label string) bool {
	for _, item := range items {
		if item.Label == label {
			return true
		}
	}
	return false
}

func completionLabels(items []protocol.CompletionItem) string {
	labels := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, item.Label)
	}
	return strings.Join(labels, ", ")
}

func findCompletionItem(items []protocol.CompletionItem, label string) (protocol.CompletionItem, bool) {
	for _, item := range items {
		if item.Label == label {
			return item, true
		}
	}
	return protocol.CompletionItem{}, false
}
