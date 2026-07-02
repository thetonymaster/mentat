# Mentat — repo guide for Claude Code

Read this once at the start of any task in this repo. It anchors the conventions
the `go-*` subagents enforce. The global `~/.claude/CLAUDE.md` (epistemics, batch
size, failure protocol) still applies and takes precedence where they overlap.

## What this is

**Mentat** is a trace-based behaviour test framework: write Gherkin specs, drive a
system-under-test (AI/LLM agents or microservices), fetch its OpenTelemetry trace
from Tempo, and run **comparators** that assert how it behaved and what it produced.

- Module: `github.com/thetonymaster/mentat`
- Design: `docs/superpowers/specs/2026-06-16-trace-behaviour-test-framework-design.md`
  and `…-2026-06-17-mentat-test-harness-design.md`
- Plans: `docs/superpowers/plans/2026-06-17-{tracelab-harness,mentat-framework-core,mentat-integration-cli}-v1.md`
- Architecture diagram: `docs/architecture/mentat-architecture.html`

## Architecture invariants (do not violate)

1. **Comparators consume `Evidence` only** (`Trace` forest + driver `Output`). They
   never touch a `TraceStore` or `Driver`. This is what keeps them portable across
   agents and microservices.
2. **`Trace` is a forest** — a run may span ≥1 root trace (multi-turn / sub-agent).
   Never assume a single root. Correlation resolves by `test.run.id` and merges.
3. **Every seam is an interface, wired at one composition root** (`engine.Build`)
   via per-seam registries. Manual DI — **no `wire`/`fx`**. The engine depends on
   interfaces, never concrete `Tempo`/`shell`/`Claude`.
4. **No silent fallbacks.** A function that cannot do its job returns an `error`
   (wrapped with `%w`), never a zero-value success or a guessed result. Trace
   not-found, ambiguous match, missing required attribute → hard, descriptive error.
   Crashes are data.
5. **Correlation is tag-first.** Inject `test.run.id` per run (resource attribute via
   `OTEL_RESOURCE_ATTRIBUTES` for spawned agents; baggage for http/grpc); resolve by
   querying that tag.

## Go conventions

- **Format & vet:** `gofmt -l .` clean and `go vet ./...` clean before any commit.
  Run `golangci-lint run` if a `.golangci.yml` exists.
- **Errors:** wrap with `fmt.Errorf("doing X: %w", err)`; messages name the concrete
  thing that failed and the value involved (`"port: expected int, got %q"`), not
  `"invalid input"`.
- **Interfaces small, defined by the consumer.** Keep files focused (one clear
  responsibility); split when a file grows unwieldy.
- **No `panic` in library code** except true, caller-unreachable invariants.

## Testing rules (enforced by go-test-writer and gated by go-reviewer)

- **TDD** for all features/bugfixes: red → green → refactor, one failing test at a
  time. (go-test-writer owns this; go-coder refuses feature work.)
- **Table-driven tests** are the default shape:
  ```go
  tests := []struct {
      name string
      // inputs
      // want / wantErr
  }{ ... }
  for _, tt := range tests {
      t.Run(tt.name, func(t *testing.T) { ... })
  }
  ```
  (No `tt := tt` capture — unnecessary since Go 1.22; this module is on 1.25.)
- **`t.Parallel()` — soft default, not a gate.** Prefer `t.Parallel()` in new
  table-driven tests (at the top, and inside each `t.Run`) **when the test shares no
  mutable state**. It surfaces ordering / shared-state / data-race bugs, which matter
  for a trace-correlation framework. For unit tests it is *not* a CI-speed measure
  (execution is seconds; CI cost is compilation) and *not* required. **Exception:**
  `//go:build e2e` tests are I/O-bound (each blocks on Tempo trace-ingestion), so
  there `t.Parallel()` *is* a real ~7× wall-clock win — see the Hermetic bullet.
  **Skip it** for tests using `t.Setenv` / `t.Chdir` — those *panic* under `t.Parallel()`.
- **Mocks: uber gomock** (`go.uber.org/mock/gomock` + `mockgen`). Generate mocks for
  the `core` interfaces (`Driver`, `TraceStore`, `Correlator`, `Comparator`,
  `Reporter`, `Judge`) rather than hand-rolling fakes:
  - Install: `go install go.uber.org/mock/mockgen@latest`
  - Declare next to the interfaces (e.g. in `internal/core/core.go`):
    `//go:generate mockgen -source=core.go -destination=mocks/mock_core.go -package=mocks`
    (mockgen paths are relative to the file's package dir)
  - Regenerate with `go generate ./...`; commit generated mocks.
  - Use `gomock.NewController(t)`, set `.EXPECT()` expectations, assert via the
    controller's automatic `t.Cleanup` finish.
  - Trivial value stubs (a struct returning a fixed trace) are acceptable only when
    no call-count/argument verification is needed; prefer gomock when behavior matters.
- **Coverage floor: 80%** per package. Check with the `coverage` skill
  (`.claude/skills/coverage`) or:
  `go test ./... -coverprofile=cover.out && go tool cover -func=cover.out`.
  A PR that drops a package below 80% is blocked.
- **BDD layer:** behaviour specs use `godog`; step defs live in `internal/steps`.
  The L3 meta-test (drive bad scenarios, assert Mentat fails) is mandatory — a test
  framework must prove it goes red on bad behaviour.
- **Hermetic by default:** unit tests use the `inmem`/`otlp-file` store + gomock; no
  network. Live-Tempo tests are `//go:build e2e` and need `make harness-up`. New e2e
  scenarios exec the prebuilt `mentatBin` (built once in `e2e/main_test.go`'s
  `TestMain`), **not** `go run ./cmd/mentat`, and call `t.Parallel()` (top + each
  `t.Run`) so the suite overlaps the per-scenario trace-ingestion waits.

## Git

- Conventional Commits (`feat:`, `fix:`, `test:`, `docs:`, `refactor:`, `chore:`).
- `git add .` is forbidden — add files individually.
- **No AI attribution** in commits or PRs (no "Generated with…", no `Co-Authored-By`).

## Routing (which subagent for what)

| Task | Agent |
| --- | --- |
| New feature / bugfix (behaviour change) | **go-test-writer** (TDD) |
| Scaffolding, config, `deploy/`, dep bumps, mocks regen, behavior-preserving refactor | **go-coder** |
| Pre-commit audit or mid-task scan | **go-reviewer** (`gate` / `pair`) |
| Explore + brief before planning | **go-context-builder** |

## Skills

- `/traces` — query the local Tempo (the `deploy/` stack) by `test.run.id`, render
  `gen_ai.*` span forests and tool-call sequences. See `.claude/skills/traces`.
- `/coverage` — run `go test` with coverage and enforce the 80% floor.

<!-- SPECKIT START -->
For additional context about technologies to be used, project structure,
shell commands, and other important information, read the current plan
at specs/007-public-extension-api/plan.md
<!-- SPECKIT END -->
