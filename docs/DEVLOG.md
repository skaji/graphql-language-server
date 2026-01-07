# DEVLOG

## Purpose
- Build a GraphQL language server in Go.
- Use `github.com/vektah/gqlparser/v2` for parsing and validation.
- Grow features incrementally.

## Development Notes
- LSP is implemented with `github.com/tliron/glsp` (LSP 3.16).
- Logging uses Go's standard `log/slog`.
- Debug logs are enabled when `DEBUG` is set; `LOG_FILE` redirects logs to a file.
- Additional debug logs exist for LSP lifecycle, diagnostics, hover, definition, and completion.
- Logs include source file/line (basename only) and process ID.
- Schema load debug logs include all type names.
- Definition logs include the missing type name when possible.
- Incremental text changes are applied using LSP ranges.
- Schema diagnostics are logged with a short message list when present.
- `make build`, `make test`, and `make lint` should pass after each milestone.
- Schema loading supports automatic discovery and configurable paths.
  - Default discovery scans all `*.graphql` and `*.graphqls` under the workspace.
  - Scans stop early on deep or large directories to avoid runaway traversal.

## Current Capabilities
- LSP lifecycle: `initialize`, `shutdown`, `setTrace`.
- Text sync: `didOpen`, `didChange`, `didClose`.
- Diagnostics: syntax and schema validation errors.
- Hover: minimal field hover with type info and description.
- Go-to-definition: fields in queries and type names in schema files.
- Go-to-definition: schema type references within SDL.
- Completion: fields, types, and directives with basic context and snippets.

## Configuration
- `initializationOptions.schemaPaths` accepts file paths, directories, or glob patterns.
- If `schemaPaths` is empty, the server scans `.graphqls` and `*schema*.graphql`.

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

## Structure
- `cmd/graphql-language-server/main.go`: entry point.
- `internal/ls/`: LSP implementation and helpers.

## Milestones Completed
- Minimal LSP server with diagnostics.
- Workspace schema loading and validation diagnostics.
- Hover support for fields.
- Go-to-definition for fields and types.
- Basic completion for fields, types, and directives.
- Completion snippets for arguments and selection sets.
- Type condition completion for inline fragments.
- Completion docs include field signatures; filtering includes argument names.
- Public README with usage and Vim configuration.
- Schema path configuration via initialization options.
- Refactor into `internal/ls` package with basic tests.

## Next Steps
- Completion context improvements (nested selection accuracy, argument snippets).
- Improve schema/query separation and caching.
- Add config file support (e.g. `graphql-language-server.json`).
