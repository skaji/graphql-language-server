package ls

import (
	"os"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

func readDocument(state *State, uri protocol.DocumentUri, path string) (string, bool) {
	state.mu.Lock()
	if text, ok := state.docs[uri]; ok {
		state.mu.Unlock()
		return text, true
	}
	state.mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}
