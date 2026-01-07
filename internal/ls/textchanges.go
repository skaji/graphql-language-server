package ls

import (
	protocol "github.com/tliron/glsp/protocol_3_16"
)

func applyContentChanges(text string, changes []any) (string, bool) {
	current := text
	for _, change := range changes {
		switch value := change.(type) {
		case protocol.TextDocumentContentChangeEventWhole:
			current = value.Text
		case protocol.TextDocumentContentChangeEvent:
			if value.Range == nil {
				current = value.Text
				continue
			}
			current = applyRangeChange(current, *value.Range, value.Text)
		default:
			return current, false
		}
	}
	return current, true
}

func applyRangeChange(text string, r protocol.Range, replacement string) string {
	start := r.Start.IndexIn(text)
	end := r.End.IndexIn(text)
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if start > len(text) {
		start = len(text)
	}
	if end > len(text) {
		end = len(text)
	}
	return text[:start] + replacement + text[end:]
}
