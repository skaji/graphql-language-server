package ls

import (
	"log/slog"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
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
	doc, err := parser.ParseSchema(&ast.Source{
		Name:  string(uri),
		Input: text,
	})
	if err == nil && doc != nil {
		if enumName, enumValue := findSchemaEnumValueAtPosition(doc, text, line, column); enumName != "" {
			if params.NewName == "" || params.NewName == enumValue {
				return nil, nil
			}
			sources, _ := s.collectSchemaSources()
			locations := findSchemaEnumValueReferencesInSources(sources, enumName, enumValue, true)
			if len(locations) == 0 {
				return nil, nil
			}
			return workspaceEditFromLocations(locations, params.NewName), nil
		}
	}

	target := definitionTargetAtPosition(text, line, column)
	if target == "" {
		target = typeNameAtLinePosition(text, line, column)
	}
	if target == "" {
		slog.Debug("rename: target not found", "uri", uri, "line", line, "column", column)
		return nil, nil
	}
	if params.NewName == "" || params.NewName == target {
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

	sources, _ := s.collectSchemaSources()
	locations := findSchemaTypeReferencesInSources(sources, target, true)
	if len(locations) == 0 {
		return nil, nil
	}
	return workspaceEditFromLocations(locations, params.NewName), nil
}

func workspaceEditFromLocations(locations []protocol.Location, newName string) *protocol.WorkspaceEdit {
	changes := make(map[protocol.DocumentUri][]protocol.TextEdit)
	for _, loc := range locations {
		edit := protocol.TextEdit{
			Range:   loc.Range,
			NewText: newName,
		}
		changes[loc.URI] = append(changes[loc.URI], edit)
	}
	return &protocol.WorkspaceEdit{Changes: changes}
}

func findSchemaEnumValueAtPosition(doc *ast.SchemaDocument, text string, line, column int) (string, string) {
	if doc == nil {
		return "", ""
	}
	defs := append(ast.DefinitionList{}, doc.Definitions...)
	defs = append(defs, doc.Extensions...)
	for _, def := range defs {
		if def == nil || def.Kind != ast.Enum {
			continue
		}
		for _, enumValue := range def.EnumValues {
			if enumValue == nil || enumValue.Position == nil {
				continue
			}
			if enumValue.Position.Line != line {
				continue
			}
			if matchesTypeName(text, line, column, enumValue.Name, enumValue.Position.Column) {
				return def.Name, enumValue.Name
			}
		}
	}

	for _, def := range defs {
		if def == nil {
			continue
		}
		for _, field := range def.Fields {
			if field == nil || field.Type == nil {
				continue
			}
			if valueMatchesEnumAtPosition(field.DefaultValue, field.Type.Name(), text, line, column) {
				return field.Type.Name(), field.DefaultValue.Raw
			}
			for _, arg := range field.Arguments {
				if arg.Type == nil {
					continue
				}
				if valueMatchesEnumAtPosition(arg.DefaultValue, arg.Type.Name(), text, line, column) {
					return arg.Type.Name(), arg.DefaultValue.Raw
				}
			}
		}
	}
	for _, directive := range doc.Directives {
		for _, arg := range directive.Arguments {
			if arg.Type == nil {
				continue
			}
			if valueMatchesEnumAtPosition(arg.DefaultValue, arg.Type.Name(), text, line, column) {
				return arg.Type.Name(), arg.DefaultValue.Raw
			}
		}
	}
	return "", ""
}

func valueMatchesEnumAtPosition(value *ast.Value, enumName, text string, line, column int) bool {
	if value == nil || value.Position == nil || value.Kind != ast.EnumValue {
		return false
	}
	if enumName == "" {
		return false
	}
	if value.Position.Line != line {
		return false
	}
	return matchesTypeName(text, line, column, value.Raw, value.Position.Column)
}

func findSchemaEnumValueReferencesInSources(sources []*ast.Source, enumName, valueName string, includeDeclaration bool) []protocol.Location {
	locations := make([]protocol.Location, 0)
	seen := make(map[string]struct{})

	addLocation := func(loc *protocol.Location) {
		if loc == nil {
			return
		}
		key := locationKey(*loc)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		locations = append(locations, *loc)
	}

	for _, source := range sources {
		doc, err := parser.ParseSchema(source)
		if err != nil {
			continue
		}

		defs := append(ast.DefinitionList{}, doc.Definitions...)
		defs = append(defs, doc.Extensions...)
		for _, def := range defs {
			if def == nil {
				continue
			}
			if includeDeclaration && def.Kind == ast.Enum && def.Name == enumName {
				for _, enumValue := range def.EnumValues {
					if enumValue == nil || enumValue.Name != valueName {
						continue
					}
					addLocation(locationFromDefinition(valueName, enumValue.Position))
				}
			}
			for _, field := range def.Fields {
				if field == nil {
					continue
				}
				if field.Type != nil && field.Type.Name() == enumName {
					if matchesEnumValue(field.DefaultValue, valueName) {
						addLocation(locationFromDefinition(valueName, field.DefaultValue.Position))
					}
				}
				for _, arg := range field.Arguments {
					if arg.Type != nil && arg.Type.Name() == enumName {
						if matchesEnumValue(arg.DefaultValue, valueName) {
							addLocation(locationFromDefinition(valueName, arg.DefaultValue.Position))
						}
					}
				}
			}
		}

		for _, directive := range doc.Directives {
			for _, arg := range directive.Arguments {
				if arg.Type != nil && arg.Type.Name() == enumName {
					if matchesEnumValue(arg.DefaultValue, valueName) {
						addLocation(locationFromDefinition(valueName, arg.DefaultValue.Position))
					}
				}
			}
		}
	}

	return locations
}

func matchesEnumValue(value *ast.Value, name string) bool {
	if value == nil || value.Kind != ast.EnumValue || value.Position == nil {
		return false
	}
	return value.Raw == name
}
