# Crush Development Guide

Go terminal AI coding assistant by [Charm](https://charm.land).
Module: `github.com/charmbracelet/crush`.

## Architecture

`main.go` → `internal/cmd` (cobra) → `internal/app/app.go` wires everything:
SQLite (`internal/db`, sqlc), config (`internal/config`, `internal/shellconfig`),
agents (`internal/agent`), tools (`internal/agent/tools`), LSP (`internal/lsp`),
MCP (`internal/agent/tools/mcp`), hooks (`internal/hooks`), TUI (`internal/ui`,
Bubble Tea v2), sessions/messages (`internal/session`, `internal/message`).

- **Config is a Service**, not global state: `config.Service`.
- **Tools are self-documenting**: each has a `.go` and a `.md`/`.md.tpl`.
- **Prompts are Go templates**: `internal/agent/templates/*.md.tpl`.
- **`crushrc`** is the primary config format (bash builtins registered in
  `internal/shellconfig`); `crush.json` still works and deep-merges with it.
- **Hooks** run user shell commands on tool events; hooks run before permission
  checks. See `HOOKS.md`.
- **CGO disabled**: `CGO_ENABLED=0`, `GOEXPERIMENT=greenteagc`.

Layered extras in this fork: `internal/contextreduction`, `internal/governor`,
`internal/nudge`, `internal/editsafe`, `internal/speed`, `internal/validate`,
`internal/memory`, `internal/promptlayer`, `internal/replyhygiene`,
`internal/goaltree`, all configured under `options.{context_reduction,governance,
edit_safety,speed,leverage}`.

## Commands

- Build: `go build .` / `go run .`
- Test: `task test` or `go test ./...`; one test:
  `go test ./internal/agent -run TestName`
- Golden files: `go test ./... -update`
- Lint: `task lint:fix` · Format: `task fmt` (gofumpt) · `task modernize`
- Dev: `task dev` (profiling)

## Style

- gofumpt; imports grouped stdlib / external / internal.
- Explicit types, small consuming-side interfaces, `context.Context` first.
- Errors returned and wrapped with `fmt.Errorf`.
- Tests: testify `require`, `t.Parallel()`, `t.Setenv`, `t.TempDir()`.
- JSON tags snake_case; file modes octal (`0o755`).
- Log messages start with a capital letter (`task lint:log` enforces it).
- Comments: capital letter, full stop, wrapped at 78 columns.
- Semantic commits, one line unless context is truly needed.
- Always format Go you write: `gofumpt -w .`, else `goimports`, else `gofmt`.

## Testing with mock providers

```go
originalUseMock := config.UseMockProviders
config.UseMockProviders = true
defer func() { config.UseMockProviders = originalUseMock; config.ResetProviders() }()
config.ResetProviders()
```

## TUI

Read `internal/ui/AGENTS.md` before touching the TUI. Styles live in
`internal/ui/styles/`: `quickstyle.go` is the token-driven base (never hardcode
`tone.*` there), `themes.go` defines themes and their overrides, `styles.go`
documents the shape. A new theme is a `quickStyle` call with a palette wired into
`ThemeForProvider`; override only the colors that differ.
