package ls

import (
	"errors"

	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func GqlErrorDiagnostics(err error) []protocol.Diagnostic {
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

func GqlErrorDiagnosticsByFile(err error, knownURIs map[protocol.DocumentUri]struct{}) map[protocol.DocumentUri][]protocol.Diagnostic {
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
		if hasFileScheme(file) {
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
		Source:   &ServerName,
	}
}
