package ls

import (
	"sync"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/vektah/gqlparser/v2/validator"
)

type builtinCache struct {
	once    sync.Once
	scalars map[string]struct{}
}

var builtins builtinCache

func ensureBuiltinsLoaded() {
	builtins.once.Do(func() {
		builtins.scalars = make(map[string]struct{})
		doc, err := parser.ParseSchema(validator.Prelude)
		if err != nil || doc == nil {
			return
		}
		for _, def := range doc.Definitions {
			if def == nil {
				continue
			}
			if def.Kind == ast.Scalar {
				builtins.scalars[def.Name] = struct{}{}
			}
		}
	})
}

func isBuiltInScalar(name string) bool {
	if name == "" {
		return false
	}
	ensureBuiltinsLoaded()
	_, ok := builtins.scalars[name]
	return ok
}
