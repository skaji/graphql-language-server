package ls

import (
	"sync"
	"time"

	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/vektah/gqlparser/v2/ast"
)

type State struct {
	mu                sync.Mutex
	docs              map[protocol.DocumentUri]string
	queryDiagnostics  map[protocol.DocumentUri][]protocol.Diagnostic
	schemaDiagnostics map[protocol.DocumentUri][]protocol.Diagnostic
	schemaPaths       []string
	rootPath          string
	schema            *ast.Schema
	schemaDebounce    *time.Timer
}

func newState() *State {
	return &State{
		docs:              make(map[protocol.DocumentUri]string),
		queryDiagnostics:  make(map[protocol.DocumentUri][]protocol.Diagnostic),
		schemaDiagnostics: make(map[protocol.DocumentUri][]protocol.Diagnostic),
	}
}
