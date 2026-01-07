package ls

import (
	"sync"

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
	schemaURIs        map[protocol.DocumentUri]struct{}
}

func newState() *State {
	return &State{
		docs:              make(map[protocol.DocumentUri]string),
		queryDiagnostics:  make(map[protocol.DocumentUri][]protocol.Diagnostic),
		schemaDiagnostics: make(map[protocol.DocumentUri][]protocol.Diagnostic),
		schemaURIs:        make(map[protocol.DocumentUri]struct{}),
	}
}
