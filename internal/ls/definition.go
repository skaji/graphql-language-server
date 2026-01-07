package ls

import (
	"strings"
	"unicode/utf8"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

func (s *Server) definition(_ *glsp.Context, params *protocol.DefinitionParams) (any, error) {
	uri := params.TextDocument.URI

	s.state.mu.Lock()
	schema := s.state.schema
	s.state.mu.Unlock()
	if schema == nil {
		return nil, nil
	}

	text, ok := s.documentText(uri)
	if !ok {
		return nil, nil
	}

	offset, line, column := PositionToRuneOffset(text, params.Position)
	if isSchemaURI(uri) {
		if loc := findTypeDefinitionLocation(schema, uri, text, line, column); loc != nil {
			return []protocol.Location{*loc}, nil
		}
		return nil, nil
	}

	doc, err := parser.ParseQuery(&ast.Source{
		Name:  string(uri),
		Input: text,
	})
	if err != nil {
		return nil, nil
	}

	def := findFieldDefinitionAtPosition(doc, schema, offset, line, column)
	if def == nil {
		return nil, nil
	}

	loc := locationFromDefinition(def.Name, def.Position)
	if loc == nil {
		return nil, nil
	}
	return []protocol.Location{*loc}, nil
}

func (s *Server) documentText(uri protocol.DocumentUri) (string, bool) {
	s.state.mu.Lock()
	text, ok := s.state.docs[uri]
	s.state.mu.Unlock()
	if ok {
		return text, true
	}
	path := uriToPath(uri)
	if path == "" {
		return "", false
	}
	return readDocument(s.state, uri, path)
}

func findFieldDefinitionAtPosition(doc *ast.QueryDocument, schema *ast.Schema, offset, line, column int) *ast.FieldDefinition {
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
		if def := findFieldDefinitionInSelectionSet(op.SelectionSet, schema, root, fragments, offset, line, column); def != nil {
			return def
		}
	}

	return nil
}

func findFieldDefinitionInSelectionSet(set ast.SelectionSet, schema *ast.Schema, parent *ast.Definition, fragments map[string]*ast.FragmentDefinition, offset, line, column int) *ast.FieldDefinition {
	if parent == nil {
		return nil
	}

	for _, selection := range set {
		switch sel := selection.(type) {
		case *ast.Field:
			if fieldMatchesPosition(sel.Position, offset, line, column, sel.Name) {
				return findFieldDefinition(parent, sel.Name)
			}

			def := findFieldDefinition(parent, sel.Name)
			if def == nil || len(sel.SelectionSet) == 0 {
				continue
			}
			nextParent := schema.Types[def.Type.Name()]
			if found := findFieldDefinitionInSelectionSet(sel.SelectionSet, schema, nextParent, fragments, offset, line, column); found != nil {
				return found
			}
		case *ast.InlineFragment:
			nextParent := parent
			if sel.TypeCondition != "" {
				if def := schema.Types[sel.TypeCondition]; def != nil {
					nextParent = def
				}
			}
			if found := findFieldDefinitionInSelectionSet(sel.SelectionSet, schema, nextParent, fragments, offset, line, column); found != nil {
				return found
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
			if found := findFieldDefinitionInSelectionSet(fragment.SelectionSet, schema, nextParent, fragments, offset, line, column); found != nil {
				return found
			}
		}
	}

	return nil
}

func findTypeDefinitionLocation(schema *ast.Schema, uri protocol.DocumentUri, text string, line, column int) *protocol.Location {
	if schema == nil {
		return nil
	}

	for _, def := range schema.Types {
		pos := def.Position
		if pos == nil || pos.Src == nil || protocol.DocumentUri(pos.Src.Name) != uri {
			continue
		}
		if pos.Line != line {
			continue
		}
		nameColumn := nameColumnInLine(text, line, def.Name, pos.Column)
		if nameColumn == 0 {
			continue
		}
		if column < nameColumn || column > nameColumn+utf8.RuneCountInString(def.Name) {
			continue
		}

		start := protocol.Position{
			Line:      protocol.UInteger(line - 1),
			Character: protocol.UInteger(nameColumn - 1),
		}
		end := protocol.Position{
			Line:      protocol.UInteger(line - 1),
			Character: protocol.UInteger(nameColumn - 1 + utf8.RuneCountInString(def.Name)),
		}
		return &protocol.Location{
			URI: uri,
			Range: protocol.Range{
				Start: start,
				End:   end,
			},
		}
	}

	return nil
}

func nameColumnInLine(text string, line int, name string, fallback int) int {
	lineText, ok := lineTextAt(text, line)
	if !ok {
		return fallback
	}
	index := strings.Index(lineText, name)
	if index == -1 {
		return fallback
	}
	return utf8.RuneCountInString(lineText[:index]) + 1
}

func lineTextAt(text string, line int) (string, bool) {
	if line <= 0 {
		return "", false
	}
	start := lineStartIndex(text, line)
	if start >= len(text) {
		return "", false
	}
	end := strings.Index(text[start:], "\n")
	if end == -1 {
		return text[start:], true
	}
	return text[start : start+end], true
}

func locationFromDefinition(name string, pos *ast.Position) *protocol.Location {
	if pos == nil || pos.Src == nil {
		return nil
	}
	uri := protocol.DocumentUri(pos.Src.Name)
	startLine := pos.Line - 1
	startChar := pos.Column - 1
	if startLine < 0 {
		startLine = 0
	}
	if startChar < 0 {
		startChar = 0
	}
	nameLen := utf8.RuneCountInString(name)
	return &protocol.Location{
		URI: uri,
		Range: protocol.Range{
			Start: protocol.Position{
				Line:      protocol.UInteger(startLine),
				Character: protocol.UInteger(startChar),
			},
			End: protocol.Position{
				Line:      protocol.UInteger(startLine),
				Character: protocol.UInteger(startChar + nameLen),
			},
		},
	}
}
