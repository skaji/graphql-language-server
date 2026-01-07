package ls

import (
	"fmt"
	"log/slog"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

func (s *Server) references(_ *glsp.Context, params *protocol.ReferenceParams) ([]protocol.Location, error) {
	uri := params.TextDocument.URI

	s.state.mu.Lock()
	schema := s.state.schema
	s.state.mu.Unlock()
	if schema == nil {
		slog.Debug("references: schema not loaded", "uri", uri)
		return nil, nil
	}

	text, ok := s.documentText(uri)
	if !ok {
		return nil, nil
	}

	if !s.isSchemaURI(uri) {
		return nil, nil
	}

	_, line, column := PositionToRuneOffset(text, params.Position)
	target := definitionTargetAtPosition(text, line, column)
	if target == "" || (schema.Types[target] == nil && !isBuiltInScalar(target)) {
		if alt := typeNameAtLinePosition(text, line, column); alt != "" {
			target = alt
		}
	}
	if target == "" {
		slog.Debug("references: target not found", "uri", uri, "line", line, "column", column)
		return nil, nil
	}

	sources, _ := s.collectSchemaSources()
	locations := findSchemaTypeReferencesInSources(sources, target, params.Context.IncludeDeclaration)
	if len(locations) == 0 {
		slog.Debug("references: no matches", "uri", uri, "line", line, "column", column, "target", target)
		return nil, nil
	}
	return locations, nil
}

func findSchemaTypeReferencesInSources(sources []*ast.Source, target string, includeDeclaration bool) []protocol.Location {
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

		for _, schemaDef := range doc.Schema {
			for _, opType := range schemaDef.OperationTypes {
				if opType.Type == target && opType.Position != nil {
					addLocation(locationFromDefinition(target, opType.Position))
				}
			}
		}
		for _, schemaDef := range doc.SchemaExtension {
			for _, opType := range schemaDef.OperationTypes {
				if opType.Type == target && opType.Position != nil {
					addLocation(locationFromDefinition(target, opType.Position))
				}
			}
		}

		for _, directive := range doc.Directives {
			for _, arg := range directive.Arguments {
				if arg.Type != nil && arg.Type.Name() == target && arg.Type.Position != nil {
					addLocation(locationFromDefinition(target, arg.Type.Position))
				}
			}
		}

		defs := append(ast.DefinitionList{}, doc.Definitions...)
		defs = append(defs, doc.Extensions...)
		for _, def := range defs {
			if def == nil {
				continue
			}
			if includeDeclaration && def.Name == target {
				addLocation(locationFromDefinition(target, def.Position))
			}
			for _, field := range def.Fields {
				if field == nil {
					continue
				}
				if field.Type != nil && field.Type.Name() == target && field.Type.Position != nil {
					addLocation(locationFromDefinition(target, field.Type.Position))
				}
				for _, arg := range field.Arguments {
					if arg.Type != nil && arg.Type.Name() == target && arg.Type.Position != nil {
						addLocation(locationFromDefinition(target, arg.Type.Position))
					}
				}
			}
		}
	}

	return locations
}

func locationKey(loc protocol.Location) string {
	return fmt.Sprintf("%s:%d:%d:%d:%d",
		loc.URI,
		loc.Range.Start.Line,
		loc.Range.Start.Character,
		loc.Range.End.Line,
		loc.Range.End.Character,
	)
}
