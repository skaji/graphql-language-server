package ls

import (
	"fmt"
	"log/slog"
	"strings"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const maxChangePreview = 40

func logChangeSummary(uri protocol.DocumentUri, version protocol.Integer, changes []any, length int) {
	if len(changes) == 0 {
		return
	}

	var summary []string
	for _, change := range changes {
		switch value := change.(type) {
		case protocol.TextDocumentContentChangeEventWhole:
			summary = append(summary, fmt.Sprintf("full(len=%d)", len(value.Text)))
		case protocol.TextDocumentContentChangeEvent:
			if value.Range == nil {
				summary = append(summary, fmt.Sprintf("full(len=%d)", len(value.Text)))
				continue
			}
			start := formatPosition(value.Range.Start)
			end := formatPosition(value.Range.End)
			preview := truncatePreview(value.Text, maxChangePreview)
			summary = append(summary, fmt.Sprintf("range(%s-%s,len=%d,\"%s\")", start, end, len(value.Text), preview))
		default:
			summary = append(summary, "unknown")
		}
	}

	slog.Debug("didChange", "uri", uri, "version", version, "length", length, "changes", strings.Join(summary, "; "))
}

func formatPosition(pos protocol.Position) string {
	return fmt.Sprintf("%d:%d", pos.Line+1, pos.Character+1)
}

func truncatePreview(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "..."
}
