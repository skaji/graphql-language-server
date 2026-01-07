package ls

import (
	"fmt"
	"log/slog"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

func (s *Server) completion(_ *glsp.Context, params *protocol.CompletionParams) (any, error) {
	uri := params.TextDocument.URI

	s.state.mu.Lock()
	schema := s.state.schema
	s.state.mu.Unlock()
	if schema == nil {
		slog.Debug("completion: schema not loaded", "uri", uri)
		return nil, nil
	}

	text, ok := s.documentText(uri)
	if !ok {
		slog.Debug("completion: document missing", "uri", uri)
		return nil, nil
	}

	offset, _, _ := PositionToRuneOffset(text, params.Position)
	if shouldCompleteDirectives(text, offset) {
		items := directiveCompletionItems(schema)
		slog.Debug("completion: directive items", "uri", uri, "count", len(items))
		return items, nil
	}

	if isSchemaURI(uri) {
		if shouldCompleteSchemaTypes(text, offset) {
			items := typeCompletionItems(schema)
			slog.Debug("completion: schema type items", "uri", uri, "count", len(items))
			return items, nil
		}
		slog.Debug("completion: schema field name position; no type suggestions", "uri", uri)
		return nil, nil
	}

	if shouldCompleteTypeCondition(text, offset) {
		items := typeCompletionItems(schema)
		slog.Debug("completion: type condition items", "uri", uri, "count", len(items))
		return items, nil
	}

	doc, err := parser.ParseQuery(&ast.Source{
		Name:  string(uri),
		Input: text,
	})
	if err != nil {
		slog.Debug("completion: parse error", "uri", uri, "error", err)
		return nil, nil
	}

	parent := findCompletionParentType(doc, schema, text, offset)
	if parent == nil {
		parent = schema.Query
	}
	items := fieldCompletionItems(parent, schema)
	slog.Debug("completion: field items", "uri", uri, "count", len(items))
	return items, nil
}

func shouldCompleteDirectives(text string, offset int) bool {
	if offset <= 0 {
		return false
	}
	index := runeOffsetToByteIndex(text, offset)
	for index > 0 {
		r, size := utf8.DecodeLastRuneInString(text[:index])
		if r == utf8.RuneError && size == 0 {
			return false
		}
		if r == '@' {
			return true
		}
		if !unicode.IsSpace(r) {
			return false
		}
		index -= size
	}
	return false
}

func typeCompletionItems(schema *ast.Schema) []protocol.CompletionItem {
	items := make([]protocol.CompletionItem, 0, len(schema.Types))
	for name, def := range schema.Types {
		kind := completionKindForDefinition(def)
		items = append(items, protocol.CompletionItem{
			Label: name,
			Kind:  &kind,
		})
	}
	return items
}

func completionKindForDefinition(def *ast.Definition) protocol.CompletionItemKind {
	if def == nil {
		return protocol.CompletionItemKindStruct
	}
	switch def.Kind {
	case ast.Object:
		return protocol.CompletionItemKindClass
	case ast.Interface:
		return protocol.CompletionItemKindInterface
	case ast.Union:
		return protocol.CompletionItemKindEnum
	case ast.Enum:
		return protocol.CompletionItemKindEnum
	case ast.Scalar:
		return protocol.CompletionItemKindValue
	case ast.InputObject:
		return protocol.CompletionItemKindStruct
	default:
		return protocol.CompletionItemKindStruct
	}
}

func directiveCompletionItems(schema *ast.Schema) []protocol.CompletionItem {
	items := make([]protocol.CompletionItem, 0, len(schema.Directives))
	for name := range schema.Directives {
		kind := protocol.CompletionItemKindFunction
		items = append(items, protocol.CompletionItem{
			Label: name,
			Kind:  &kind,
		})
	}
	return items
}

func fieldCompletionItems(parent *ast.Definition, schema *ast.Schema) []protocol.CompletionItem {
	if parent == nil {
		return nil
	}
	items := make([]protocol.CompletionItem, 0, len(parent.Fields))
	for _, field := range parent.Fields {
		kind := protocol.CompletionItemKindField
		detail := field.Type.String()
		filterText := completionFilterText(field.Name, field.Arguments)
		sortText := strings.ToLower(field.Name)
		item := protocol.CompletionItem{
			Label:      field.Name,
			Kind:       &kind,
			Detail:     &detail,
			FilterText: &filterText,
			SortText:   &sortText,
		}
		if doc := completionDocumentation(field); doc != "" {
			item.Documentation = protocol.MarkupContent{
				Kind:  protocol.MarkupKindMarkdown,
				Value: doc,
			}
		}
		if insertText, ok := fieldInsertText(field, schema); ok {
			item.InsertText = &insertText
			format := protocol.InsertTextFormatSnippet
			item.InsertTextFormat = &format
		}
		items = append(items, item)
	}
	return items
}

func findCompletionParentType(doc *ast.QueryDocument, schema *ast.Schema, text string, offset int) *ast.Definition {
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
		if !selectionSetContainsOffset(text, op.Position, offset) {
			continue
		}
		return findParentTypeInSelectionSet(op.SelectionSet, schema, root, fragments, text, offset)
	}

	return nil
}

func findParentTypeInSelectionSet(set ast.SelectionSet, schema *ast.Schema, parent *ast.Definition, fragments map[string]*ast.FragmentDefinition, text string, offset int) *ast.Definition {
	if parent == nil {
		return nil
	}

	for _, selection := range set {
		switch sel := selection.(type) {
		case *ast.Field:
			def := findFieldDefinition(parent, sel.Name)
			if def == nil || len(sel.SelectionSet) == 0 {
				continue
			}
			if !selectionSetContainsOffset(text, sel.Position, offset) {
				continue
			}
			nextParent := schema.Types[def.Type.Name()]
			if nextParent == nil {
				return parent
			}
			nested := findParentTypeInSelectionSet(sel.SelectionSet, schema, nextParent, fragments, text, offset)
			if nested != nil {
				return nested
			}
			return nextParent
		case *ast.InlineFragment:
			if !selectionSetContainsOffset(text, sel.Position, offset) {
				continue
			}
			nextParent := parent
			if sel.TypeCondition != "" {
				if def := schema.Types[sel.TypeCondition]; def != nil {
					nextParent = def
				}
			}
			nested := findParentTypeInSelectionSet(sel.SelectionSet, schema, nextParent, fragments, text, offset)
			if nested != nil {
				return nested
			}
			return nextParent
		case *ast.FragmentSpread:
			fragment := fragments[sel.Name]
			if fragment == nil || !selectionSetContainsOffset(text, fragment.Position, offset) {
				continue
			}
			nextParent := parent
			if fragment.TypeCondition != "" {
				if def := schema.Types[fragment.TypeCondition]; def != nil {
					nextParent = def
				}
			}
			nested := findParentTypeInSelectionSet(fragment.SelectionSet, schema, nextParent, fragments, text, offset)
			if nested != nil {
				return nested
			}
			return nextParent
		}
	}

	return parent
}

func selectionSetContainsOffset(text string, pos *ast.Position, offset int) bool {
	if pos == nil {
		return false
	}
	runes := []rune(text)
	start := pos.Start
	start = max(0, start)
	open, closeIndex, ok := selectionSetRange(runes, start)
	if !ok {
		return false
	}
	return offset >= open && offset <= closeIndex
}

func selectionSetRange(runes []rune, start int) (int, int, bool) {
	open, ok := scanToOpenBrace(runes, start)
	if !ok {
		return 0, 0, false
	}
	closeIndex, ok := findMatchingBrace(runes, open)
	if !ok {
		return 0, 0, false
	}
	return open, closeIndex, true
}

func scanToOpenBrace(runes []rune, start int) (int, bool) {
	inString := false
	inBlockString := false
	inComment := false
	for i := start; i < len(runes); i++ {
		r := runes[i]
		if inComment {
			if r == '\n' {
				inComment = false
			}
			continue
		}
		if inString {
			if r == '\\' {
				i++
				continue
			}
			if r == '"' {
				inString = false
			}
			continue
		}
		if inBlockString {
			if r == '"' && i+2 < len(runes) && runes[i+1] == '"' && runes[i+2] == '"' {
				inBlockString = false
				i += 2
			}
			continue
		}
		if r == '#' {
			inComment = true
			continue
		}
		if r == '"' {
			if i+2 < len(runes) && runes[i+1] == '"' && runes[i+2] == '"' {
				inBlockString = true
				i += 2
			} else {
				inString = true
			}
			continue
		}
		if r == '{' {
			return i, true
		}
	}
	return 0, false
}

func findMatchingBrace(runes []rune, open int) (int, bool) {
	depth := 0
	inString := false
	inBlockString := false
	inComment := false
	for i := open; i < len(runes); i++ {
		r := runes[i]
		if inComment {
			if r == '\n' {
				inComment = false
			}
			continue
		}
		if inString {
			if r == '\\' {
				i++
				continue
			}
			if r == '"' {
				inString = false
			}
			continue
		}
		if inBlockString {
			if r == '"' && i+2 < len(runes) && runes[i+1] == '"' && runes[i+2] == '"' {
				inBlockString = false
				i += 2
			}
			continue
		}
		if r == '#' {
			inComment = true
			continue
		}
		if r == '"' {
			if i+2 < len(runes) && runes[i+1] == '"' && runes[i+2] == '"' {
				inBlockString = true
				i += 2
			} else {
				inString = true
			}
			continue
		}
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

func runeOffsetToByteIndex(text string, offset int) int {
	if offset <= 0 {
		return 0
	}
	count := 0
	for i := range text {
		if count == offset {
			return i
		}
		count++
	}
	return len(text)
}

func shouldCompleteTypeCondition(text string, offset int) bool {
	lineText, ok := linePrefixAtOffset(text, offset)
	if !ok {
		return false
	}
	trim := strings.TrimSpace(lineText)
	if !strings.Contains(trim, "...") {
		return false
	}
	idx := strings.LastIndex(trim, "...") + 3
	rest := strings.TrimSpace(trim[idx:])
	return strings.HasPrefix(rest, "on")
}

func shouldCompleteSchemaTypes(text string, offset int) bool {
	linePrefix, ok := linePrefixAtOffset(text, offset)
	if !ok {
		return false
	}
	if strings.Contains(linePrefix, "\"") {
		return false
	}
	return strings.Contains(linePrefix, ":")
}

func linePrefixAtOffset(text string, offset int) (string, bool) {
	line := lineFromOffset(text, offset)
	if line <= 0 {
		return "", false
	}
	start := lineStartIndex(text, line)
	byteOffset := runeOffsetToByteIndex(text, offset)
	byteOffset = max(start, byteOffset)
	byteOffset = min(len(text), byteOffset)
	return text[start:byteOffset], true
}

func lineFromOffset(text string, offset int) int {
	if offset <= 0 {
		return 1
	}
	byteOffset := runeOffsetToByteIndex(text, offset)
	line := 1
	for i := 0; i < len(text) && i < byteOffset; i++ {
		if text[i] == '\n' {
			line++
		}
	}
	return line
}

func fieldInsertText(field *ast.FieldDefinition, schema *ast.Schema) (string, bool) {
	if field == nil {
		return "", false
	}

	argSnippet := fieldArgumentsSnippet(field.Arguments)
	selectionSnippet := fieldSelectionSnippet(field, schema)

	if argSnippet == "" && selectionSnippet == "" {
		return "", false
	}

	return field.Name + argSnippet + selectionSnippet, true
}

func fieldArgumentsSnippet(args ast.ArgumentDefinitionList) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, 0, len(args))
	for i, arg := range args {
		parts = append(parts, fmt.Sprintf("%s: ${%d}", arg.Name, i+1))
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func fieldSelectionSnippet(field *ast.FieldDefinition, schema *ast.Schema) string {
	if schema == nil || field == nil || field.Type == nil {
		return ""
	}
	def := schema.Types[field.Type.Name()]
	if def == nil || !def.IsCompositeType() {
		return ""
	}
	return " { $0 }"
}

func completionFilterText(name string, args ast.ArgumentDefinitionList) string {
	if len(args) == 0 {
		return name
	}
	argNames := make([]string, 0, len(args))
	for _, arg := range args {
		argNames = append(argNames, arg.Name)
	}
	return name + " " + strings.Join(argNames, " ")
}

func completionDocumentation(field *ast.FieldDefinition) string {
	if field == nil {
		return ""
	}
	signature := fieldSignature(field)
	if field.Description == "" {
		return "```graphql\n" + signature + "\n```"
	}
	return "```graphql\n" + signature + "\n```\n\n" + field.Description
}

func fieldSignature(field *ast.FieldDefinition) string {
	if field == nil || field.Type == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(field.Name)
	if len(field.Arguments) > 0 {
		b.WriteByte('(')
		for i, arg := range field.Arguments {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(arg.Name)
			b.WriteString(": ")
			b.WriteString(arg.Type.String())
		}
		b.WriteByte(')')
	}
	b.WriteString(": ")
	b.WriteString(field.Type.String())
	return b.String()
}
