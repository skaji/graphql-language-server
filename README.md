# graphql-language-server

GraphQL language server for Go projects, built on `github.com/tliron/glsp` and `github.com/vektah/gqlparser/v2`.

## Features
- Diagnostics: syntax and schema validation errors
- Hover: field type info
- Go-to-definition: fields and types
- Completion: fields, types, directives
- Schema discovery with configurable paths

## Install
```bash
go install github.com/skaji/graphql-language-server/cmd/graphql-language-server@latest
```

## Usage
The server speaks LSP over stdio. Most editors can launch it directly.

### Schema configuration
You can provide schema paths via `initializationOptions.schemaPaths`.

Patterns may be files, directories, or globs. Relative paths are resolved from the workspace root.

Example:
```json
{
  "initializationOptions": {
    "schemaPaths": [
      "schema/**/*.graphqls",
      "graphql/schema",
      "schemas/*.graphql"
    ]
  }
}
```

## Vim configuration (vim-lsp)
Example for [vim-lsp](https://github.com/prabirshrestha/vim-lsp):

```vim
if executable('graphql-language-server')
  augroup graphql_lsp
    autocmd!
    autocmd FileType graphql,graphqls call lsp#register_server({
      \ 'name': 'graphql-language-server',
      \ 'cmd': {server_info->['graphql-language-server']},
      \ 'whitelist': ['graphql', 'graphqls'],
      \ 'initialization_options': {
      \   'schemaPaths': ['schema/**/*.graphqls', 'graphql/schema'],
      \ },
      \ })
  augroup END
endif
```
