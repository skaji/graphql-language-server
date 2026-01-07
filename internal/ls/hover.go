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
	if ok {
		_, line, column := PositionToRuneOffset(text, params.Position)
		slog.Debug("hover request", "uri", uri, "line", line, "column", column)
	} else {
		slog.Debug("hover request", "uri", uri, "line", int(params.Position.Line)+1, "column", int(params.Position.Character)+1)
	}
	if !ok || schema == nil {
		return nil, nil
	}

	if s.isSchemaURI(uri) {
		doc, err := parser.ParseSchema(&ast.Source{
			Name:  string(uri),
			Input: text,
		})
		if err != nil {
			slog.Debug("hover: schema parse error", "uri", uri, "error", err)
			return nil, nil
		}
		offset, line, column := PositionToRuneOffset(text, params.Position)
		info := findSchemaHover(doc, text, offset, line, column)
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

func findSchemaHover(doc *ast.SchemaDocument, text string, offset, line, column int) *HoverInfo {
	if doc == nil {
		return nil
	}
	if typeName := typeNameAtLinePosition(text, line, column); typeName != "" {
		return schemaHoverForType(doc, typeName)
	}
	defs := append(ast.DefinitionList{}, doc.Definitions...)
	defs = append(defs, doc.Extensions...)
	for _, def := range defs {
		if def == nil || def.Position == nil {
			continue
		}
		if def.Position.Line == line {
			if typeSignature := schemaDefinitionSnippet(def); typeSignature != "" {
				if matchesTypeName(text, line, column, def.Name, def.Position.Column) {
					return &HoverInfo{
						Name:        def.Name,
						TypeString:  string(def.Kind),
						Signature:   typeSignature,
						Description: def.Description,
					}
				}
			}
		}
		for _, field := range def.Fields {
			if field == nil || field.Position == nil {
				continue
			}
			if !positionContainsOffset(field.Position, offset) && field.Position.Line != line {
				continue
			}
			if field.Position.Line == line && matchesTypeName(text, line, column, field.Name, field.Position.Column) {
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

func schemaDefinitionSnippet(def *ast.Definition) string {
	if def == nil {
		return ""
	}
	keyword := schemaTypeKeyword(def.Kind)
	if keyword == "" {
		return def.Name
	}
	switch def.Kind {
	case ast.Object, ast.Interface, ast.InputObject:
		return schemaFieldBlock(keyword, def.Name, def.Fields)
	case ast.Enum:
		return schemaEnumBlock(def.Name, def.EnumValues)
	case ast.Union:
		return schemaUnionSignature(def.Name, def.Types)
	default:
		return keyword + " " + def.Name
	}
}

func schemaFieldBlock(keyword, name string, fields ast.FieldList) string {
	var b strings.Builder
	b.WriteString(keyword)
	b.WriteByte(' ')
	b.WriteString(name)
	b.WriteString(" {\n")
	for _, field := range fields {
		if field == nil {
			continue
		}
		b.WriteString("  ")
		b.WriteString(fieldSignature(field))
		b.WriteByte('\n')
	}
	b.WriteByte('}')
	return b.String()
}

func schemaEnumBlock(name string, values ast.EnumValueList) string {
	var b strings.Builder
	b.WriteString("enum ")
	b.WriteString(name)
	b.WriteString(" {\n")
	for _, value := range values {
		if value == nil {
			continue
		}
		b.WriteString("  ")
		b.WriteString(value.Name)
		b.WriteByte('\n')
	}
	b.WriteByte('}')
	return b.String()
}

func schemaUnionSignature(name string, types []string) string {
	if len(types) == 0 {
		return "union " + name
	}
	return "union " + name + " = " + strings.Join(types, " | ")
}

func schemaTypeKeyword(kind ast.DefinitionKind) string {
	switch kind {
	case ast.Object:
		return "type"
	case ast.InputObject:
		return "input"
	case ast.Interface:
		return "interface"
	case ast.Enum:
		return "enum"
	case ast.Union:
		return "union"
	case ast.Scalar:
		return "scalar"
	default:
		return ""
	}
}

func typeNameAtLinePosition(text string, line, column int) string {
	lineText, ok := lineTextAt(text, line)
	if !ok {
		return ""
	}
	runes := []rune(lineText)
	for i := range runes {
		if runes[i] != ':' {
			continue
		}
		j := i + 1
		for j < len(runes) && (runes[j] == ' ' || runes[j] == '\t') {
			j++
		}
		for j < len(runes) && (runes[j] == '[' || runes[j] == ']' || runes[j] == '!') {
			j++
		}
		for j < len(runes) && (runes[j] == ' ' || runes[j] == '\t') {
			j++
		}
		if j >= len(runes) || !isNameStart(runes[j]) {
			continue
		}
		start := j
		j++
		for j < len(runes) && isNameContinue(runes[j]) {
			j++
		}
		end := j
		startCol := start + 1
		endCol := end
		if column >= startCol && column <= endCol {
			return string(runes[start:end])
		}
	}
	return ""
}

func isNameStart(r rune) bool {
	return r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
}

func isNameContinue(r rune) bool {
	return isNameStart(r) || (r >= '0' && r <= '9')
}

func positionContainsOffset(pos *ast.Position, offset int) bool {
	if pos == nil {
		return false
	}
	if pos.End > pos.Start && offset >= pos.Start && offset <= pos.End {
		return true
	}
	return false
}

func schemaTypeForName(doc *ast.SchemaDocument, name string) *ast.Definition {
	if doc == nil {
		return nil
	}
	for _, def := range doc.Definitions {
		if def != nil && def.Name == name {
			return def
		}
	}
	for _, def := range doc.Extensions {
		if def != nil && def.Name == name {
			return def
		}
	}
	return nil
}

func schemaHoverForType(doc *ast.SchemaDocument, typeName string) *HoverInfo {
	if typeName == "" {
		return nil
	}
	if typeDef := schemaTypeForName(doc, typeName); typeDef != nil {
		return &HoverInfo{
			Name:        typeDef.Name,
			TypeString:  string(typeDef.Kind),
			Signature:   schemaDefinitionSnippet(typeDef),
			Description: typeDef.Description,
		}
	}
	if isBuiltInScalar(typeName) {
		return &HoverInfo{
			Name:       typeName,
			TypeString: "scalar",
			Signature:  "scalar " + typeName,
		}
	}
	return &HoverInfo{
		Name:       typeName,
		TypeString: "type",
		Signature:  "type " + typeName,
	}
}

func isBuiltInScalar(name string) bool {
	switch name {
	case "String", "Int", "Float", "Boolean", "ID":
		return true
	default:
		return false
	}
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
