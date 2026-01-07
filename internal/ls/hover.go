package ls

import (
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

type HoverInfo struct {
	Name        string
	TypeString  string
	Signature   string
	Description string
}

func (s *Server) hover(_ *glsp.Context, params *protocol.HoverParams) (*protocol.Hover, error) {
	uri := params.TextDocument.URI
	s.state.mu.Lock()
	text, ok := s.state.docs[uri]
	schema := s.state.schema
	s.state.mu.Unlock()
	if !ok || schema == nil {
		return nil, nil
	}

	if isSchemaURI(uri) {
		_, line, column := PositionToRuneOffset(text, params.Position)
		info := findSchemaHover(schema, uri, text, line, column)
		if info == nil {
			slog.Debug("hover: no schema info", "uri", uri, "line", line, "column", column)
			return nil, nil
		}
		return hoverFromInfo(info), nil
	}

	doc, err := parser.ParseQuery(&ast.Source{
		Name:  string(uri),
		Input: text,
	})
	if err != nil {
		return nil, nil
	}

	offset, line, column := PositionToRuneOffset(text, params.Position)
	info := FindFieldHover(doc, schema, offset, line, column)
	if info == nil {
		slog.Debug("hover: no field info", "uri", uri, "line", line, "column", column)
		return nil, nil
	}

	return hoverFromInfo(info), nil
}

func FindFieldHover(doc *ast.QueryDocument, schema *ast.Schema, offset, line, column int) *HoverInfo {
	if doc == nil || schema == nil {
		return nil
	}

	fragments := make(map[string]*ast.FragmentDefinition, len(doc.Fragments))
	for _, fragment := range doc.Fragments {
		fragments[fragment.Name] = fragment
	}

	for _, op := range doc.Operations {
		root := rootTypeForOperation(schema, op.Operation)
		if root == nil {
			continue
		}
		if info := findFieldInSelectionSet(op.SelectionSet, schema, root, fragments, offset, line, column); info != nil {
			return info
		}
	}

	return nil
}

func findSchemaHover(schema *ast.Schema, uri protocol.DocumentUri, text string, line, column int) *HoverInfo {
	if schema == nil {
		return nil
	}
	for _, def := range schema.Types {
		if def == nil || def.Position == nil || def.Position.Src == nil {
			continue
		}
		if protocol.DocumentUri(def.Position.Src.Name) != uri {
			continue
		}
		if typeSignature := schemaTypeSignature(def); typeSignature != "" {
			if matchesTypeName(text, line, column, def.Name, def.Position.Column) {
				return &HoverInfo{
					Name:        def.Name,
					TypeString:  string(def.Kind),
					Signature:   typeSignature,
					Description: def.Description,
				}
			}
		}
		for _, field := range def.Fields {
			if field == nil || field.Position == nil {
				continue
			}
			if field.Position.Line != line {
				continue
			}
			if nameMatchesColumn(field.Position.Column, column, field.Name) {
				return &HoverInfo{
					Name:        field.Name,
					TypeString:  field.Type.String(),
					Signature:   fieldSignature(field),
					Description: field.Description,
				}
			}
		}
	}
	return nil
}

func schemaTypeSignature(def *ast.Definition) string {
	if def == nil {
		return ""
	}
	kind := strings.ToLower(string(def.Kind))
	if kind == "" {
		return def.Name
	}
	return kind + " " + def.Name
}

func matchesTypeName(text string, line, column int, name string, fallback int) bool {
	nameCol := nameColumnInLine(text, line, name, fallback)
	if nameCol <= 0 {
		return false
	}
	return nameMatchesColumn(nameCol, column, name)
}

func nameMatchesColumn(startColumn, column int, name string) bool {
	if startColumn <= 0 {
		return false
	}
	nameLen := utf8.RuneCountInString(name)
	return column >= startColumn && column <= startColumn+nameLen
}

func rootTypeForOperation(schema *ast.Schema, operation ast.Operation) *ast.Definition {
	switch operation {
	case ast.Mutation:
		return schema.Mutation
	case ast.Subscription:
		return schema.Subscription
	default:
		return schema.Query
	}
}

func findFieldInSelectionSet(set ast.SelectionSet, schema *ast.Schema, parent *ast.Definition, fragments map[string]*ast.FragmentDefinition, offset, line, column int) *HoverInfo {
	if parent == nil {
		return nil
	}

	for _, selection := range set {
		switch sel := selection.(type) {
		case *ast.Field:
			if fieldMatchesPosition(sel.Position, offset, line, column, sel.Name) {
				if def := findFieldDefinition(parent, sel.Name); def != nil {
					return &HoverInfo{
						Name:        sel.Name,
						TypeString:  def.Type.String(),
						Signature:   sel.Name + ": " + def.Type.String(),
						Description: def.Description,
					}
				}
			}

			def := findFieldDefinition(parent, sel.Name)
			if def == nil || len(sel.SelectionSet) == 0 {
				continue
			}
			nextParent := schema.Types[def.Type.Name()]
			if info := findFieldInSelectionSet(sel.SelectionSet, schema, nextParent, fragments, offset, line, column); info != nil {
				return info
			}
		case *ast.InlineFragment:
			nextParent := parent
			if sel.TypeCondition != "" {
				if def := schema.Types[sel.TypeCondition]; def != nil {
					nextParent = def
				}
			}
			if info := findFieldInSelectionSet(sel.SelectionSet, schema, nextParent, fragments, offset, line, column); info != nil {
				return info
			}
		case *ast.FragmentSpread:
			fragment := fragments[sel.Name]
			if fragment == nil {
				continue
			}
			nextParent := parent
			if fragment.TypeCondition != "" {
				if def := schema.Types[fragment.TypeCondition]; def != nil {
					nextParent = def
				}
			}
			if info := findFieldInSelectionSet(fragment.SelectionSet, schema, nextParent, fragments, offset, line, column); info != nil {
				return info
			}
		}
	}

	return nil
}

func findFieldDefinition(parent *ast.Definition, name string) *ast.FieldDefinition {
	if parent == nil {
		return nil
	}
	for _, field := range parent.Fields {
		if field.Name == name {
			return field
		}
	}
	return nil
}

func fieldMatchesPosition(pos *ast.Position, offset, line, column int, name string) bool {
	if pos == nil {
		return false
	}
	if pos.End > pos.Start && offset >= pos.Start && offset <= pos.End {
		return true
	}
	if pos.Line > 0 && pos.Column > 0 && line == pos.Line {
		nameLen := utf8.RuneCountInString(name)
		if column >= pos.Column && column <= pos.Column+nameLen {
			return true
		}
	}
	return false
}

func hoverFromInfo(info *HoverInfo) *protocol.Hover {
	if info == nil {
		return nil
	}
	signature := info.Signature
	if signature == "" && info.Name != "" && info.TypeString != "" {
		signature = info.Name + ": " + info.TypeString
	}
	if signature == "" {
		return nil
	}
	value := "```graphql\n" + signature + "\n```"
	if info.Description != "" {
		value += "\n\n" + info.Description
	}
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.MarkupKindMarkdown,
			Value: value,
		},
	}
}

func PositionToRuneOffset(text string, pos protocol.Position) (int, int, int) {
	byteOffset := pos.IndexIn(text)
	byteOffset = max(0, byteOffset)

	line := int(pos.Line) + 1
	lineStart := lineStartIndex(text, line)
	byteOffset = max(lineStart, byteOffset)

	offset := utf8.RuneCountInString(text[:byteOffset])
	column := utf8.RuneCountInString(text[lineStart:byteOffset]) + 1
	column = max(1, column)
	return offset, line, column
}
