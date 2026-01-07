package main

import (
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vektah/gqlparser/v2/parser"

	"github.com/skaji/graphql-language-server/internal/ls"
)

func TestGqlErrorDiagnosticsEmpty(t *testing.T) {
	diagnostics := ls.GqlErrorDiagnostics(nil)
	if len(diagnostics) != 0 {
		t.Fatalf("expected no diagnostics, got %d", len(diagnostics))
	}
}

func TestGqlErrorDiagnosticsParseError(t *testing.T) {
	_, err := parser.ParseQuery(&ast.Source{
		Name:  "test.graphql",
		Input: "{",
	})
	if err == nil {
		t.Fatal("expected parse error")
	}

	diagnostics := ls.GqlErrorDiagnostics(err)
	if len(diagnostics) == 0 {
		t.Fatal("expected diagnostics")
	}

	if diagnostics[0].Message == "" {
		t.Fatal("expected diagnostic message")
	}
}

func TestGqlErrorDiagnosticsByFile(t *testing.T) {
	fileURI := protocol.DocumentUri("file:///tmp/schema.graphql")
	known := map[protocol.DocumentUri]struct{}{
		fileURI: {},
	}
	err := gqlerror.ErrorLocf(string(fileURI), 1, 1, "bad schema")
	byURI := ls.GqlErrorDiagnosticsByFile(err, known)
	if len(byURI[fileURI]) != 1 {
		t.Fatalf("expected diagnostics for %s", fileURI)
	}
}

func TestFindFieldHover(t *testing.T) {
	schema := gqlparser.MustLoadSchema(&ast.Source{
		Input: "type Query { user: User }\n type User { name: String }\n",
	})
	query := "{ user { name } }"
	doc, err := parser.ParseQuery(&ast.Source{
		Input: query,
	})
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	offset, line, column := ls.PositionToRuneOffset(query, protocol.Position{
		Line:      0,
		Character: 2,
	})
	info := ls.FindFieldHover(doc, schema, offset, line, column)
	if info == nil {
		t.Fatal("expected hover info")
	}
	if info.Name != "user" {
		t.Fatalf("expected field name user, got %q", info.Name)
	}
	if info.TypeString != "User" {
		t.Fatalf("expected type User, got %q", info.TypeString)
	}
}
