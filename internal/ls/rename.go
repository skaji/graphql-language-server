package ls

import (
	"log/slog"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
)

func (s *Server) rename(_ *glsp.Context, params *protocol.RenameParams) (*protocol.WorkspaceEdit, error) {
	if params == nil {
		return nil, nil
	}
	uri := params.TextDocument.URI

	s.state.mu.Lock()
	schema := s.state.schema
	s.state.mu.Unlock()
	if schema == nil {
		slog.Debug("rename: schema not loaded", "uri", uri)
		return nil, nil
	}
	if !s.isSchemaURI(uri) {
		return nil, nil
	}

	text, ok := s.documentText(uri)
	if !ok {
		return nil, nil
	}

	_, line, column := PositionToRuneOffset(text, params.Position)
	target := definitionTargetAtPosition(text, line, column)
	if target == "" {
		target = typeNameAtLinePosition(text, line, column)
	}
	if target == "" {
		slog.Debug("rename: target not found", "uri", uri, "line", line, "column", column)
		return nil, nil
	}
	if isBuiltInScalar(target) {
		slog.Debug("rename: builtin scalar skipped", "uri", uri, "target", target)
		return nil, nil
	}
	if schema.Types[target] == nil {
		slog.Debug("rename: type not found", "uri", uri, "target", target)
		return nil, nil
	}
	if params.NewName == "" || params.NewName == target {
		return nil, nil
	}

	sources, _ := s.collectSchemaSources()
	locations := findSchemaTypeReferencesInSources(sources, target, true)
	if len(locations) == 0 {
		return nil, nil
	}

	changes := make(map[protocol.DocumentUri][]protocol.TextEdit)
	for _, loc := range locations {
		edit := protocol.TextEdit{
			Range:   loc.Range,
			NewText: params.NewName,
		}
		changes[loc.URI] = append(changes[loc.URI], edit)
	}

	return &protocol.WorkspaceEdit{
		Changes: changes,
	}, nil
}
