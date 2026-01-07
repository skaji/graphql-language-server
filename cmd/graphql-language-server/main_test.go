package main

import (
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vektah/gqlparser/v2/parser"
)

func TestGqlErrorDiagnosticsEmpty(t *testing.T) {
	diagnostics := gqlErrorDiagnostics(nil)
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

	diagnostics := gqlErrorDiagnostics(err)
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
	byURI := gqlErrorDiagnosticsByFile(err, known)
	if len(byURI[fileURI]) != 1 {
		t.Fatalf("expected diagnostics for %s", fileURI)
	}
}
