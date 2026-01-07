# graphql-language-server

GraphQL language server implemented in Go, built on `github.com/tliron/glsp` and `github.com/vektah/gqlparser/v2`.

Developed with Codex.

## Features

- Diagnostics: syntax and schema validation errors
- Hover: field type info
- Go-to-definition: fields, types, and schema type references
- Completion: fields, types, directives, and schema type positions
- Schema discovery with configurable paths (defaults to all `.graphql`/`.graphqls`)

## Install

Download the appropriate binary from:
https://github.com/skaji/graphql-language-server/releases/latest

## Usage

The server speaks LSP over stdio. Most editors can launch it directly.

### Schema configuration

You can provide schema paths via `initializationOptions.schemaPaths`.

Patterns may be files, directories, or globs. Relative paths are resolved from the workspace root.
If omitted, the server scans all `.graphql` and `.graphqls` files under the workspace root.

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

## License

MIT
