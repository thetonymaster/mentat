# Mentat Ops & CI v1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax. **These are non-TDD tasks — route to `go-coder`** (config/docs/CI, no behaviour to test). The "test" for each is that the tool/workflow parses and runs.

**Goal:** Wire the repo's operational glue — linter config, a CI workflow that runs vet/lint/tests + the 80% coverage gate + mock-drift check + the hermetic E2E, Makefile convenience targets, and a top-level README.

**Architecture:** CI runs three jobs — `check` (vet, gofmt, golangci-lint, `go test -race`, coverage gate via the `coverage` skill), `mocks` (regenerate gomock mocks and fail on drift), and `e2e` (bring up the `deploy/` Tempo+Collector stack, run the `e2e`-tagged tests).

**Tech Stack:** GitHub Actions (`actions/setup-go@v5`, `golangci/golangci-lint-action@v6`), `golangci-lint`, Docker Compose, Make.

## Global Constraints

- **Go module:** `github.com/thetonymaster/mentat`; Go floor `1.23`.
- **Sequencing:** add after Plan 1 lands (so `go test ./...` has packages); the gates
  go green as the code plans complete. CI may be committed earlier and will simply
  fail until there is code — acceptable.
- **Coverage floor 80%** enforced via `.claude/skills/coverage/coverage.sh` (single
  source of truth for the gate — do not duplicate the threshold logic in CI yaml).
- Conventional Commits; files added individually; no AI attribution.

## File Structure

```
.golangci.yml                 linter config
Makefile                      (extend) test / lint / cover / ci targets
.github/workflows/ci.yml      check + mocks + e2e jobs
README.md                     quickstart + pointers to specs/plans
```

---

### Task 1: Linter config + Makefile targets

**Files:**
- Create: `.golangci.yml`
- Modify: `Makefile` (append targets; `harness-up/down/smoke` already exist from Plan 1)

- [ ] **Step 1: Write the linter config**

Create `.golangci.yml`:
```yaml
run:
  timeout: 5m
linters:
  enable:
    - govet
    - errcheck
    - staticcheck
    - ineffassign
    - unused
    - gofmt
    - misspell
    - bodyclose
issues:
  exclude-rules:
    # generated mocks are not ours to lint
    - path: internal/core/mocks/
      linters: [errcheck, staticcheck, unused]
```

- [ ] **Step 2: Append Makefile targets**

Add to `Makefile`:
```make
.PHONY: test lint cover ci

test:
	go test ./... -race

lint:
	gofmt -l . | tee /dev/stderr | (! read)   # fail if any file is unformatted
	go vet ./...
	golangci-lint run

cover:
	bash .claude/skills/coverage/coverage.sh ./... 80

ci: lint test cover
```

- [ ] **Step 3: Verify**

Run (install golangci-lint first if missing — `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`):
```bash
gofmt -l .            # expect no output
go vet ./...          # expect clean
golangci-lint run     # expect clean (or only intended findings)
```
Expected: all clean once Plan 1+ code exists. (`make cover` needs tests present.)

- [ ] **Step 4: Commit**

```bash
git add .golangci.yml Makefile
git commit -m "chore(ci): golangci-lint config + make test/lint/cover/ci targets"
```

---

### Task 2: GitHub Actions CI workflow

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: Write the workflow**

Create `.github/workflows/ci.yml`:
```yaml
name: ci
on:
  push:
    branches: [main]
  pull_request:

jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.23'
      - run: test -z "$(gofmt -l .)" || (gofmt -l . && exit 1)
      - run: go vet ./...
      - uses: golangci/golangci-lint-action@v6
        with:
          version: latest
      - run: go test ./... -race
      - name: coverage gate (80%)
        run: bash .claude/skills/coverage/coverage.sh ./... 80

  mocks:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.23'
      - run: go install go.uber.org/mock/mockgen@latest
      - run: go generate ./...
      - name: fail on uncommitted mock drift
        run: git diff --exit-code -- internal/core/mocks || (echo "regenerate mocks: go generate ./..." && exit 1)

  e2e:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.23'
      - name: start Tempo + Collector
        run: docker compose -f deploy/docker-compose.yml up -d
      - name: wait for Tempo
        run: |
          for i in $(seq 1 30); do
            curl -sf http://localhost:3200/ready && break || sleep 2
          done
      - run: go test -tags e2e ./e2e/ -v
      - if: always()
        run: docker compose -f deploy/docker-compose.yml down -v
```

- [ ] **Step 2: Verify the YAML parses**

Run:
```bash
python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/ci.yml')); print('ci.yml OK')"
```
Expected: `ci.yml OK`. (If `actionlint` is installed, also run it: `actionlint`.)

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "chore(ci): GitHub Actions — check, mock-drift, and hermetic e2e jobs"
```

---

### Task 3: Top-level README

**Files:**
- Create: `README.md`

- [ ] **Step 1: Write the README**

Create `README.md`:
```markdown
# Mentat

A trace-based **behaviour test framework**: write Gherkin specs, drive a
system-under-test (AI/LLM agents or microservices), fetch its OpenTelemetry trace
from Tempo, and run **comparators** that assert *how it behaved* and *what it produced*.

## Quickstart

```bash
# 1. bring up the local Tempo + OTel Collector
make harness-up

# 2. run the behaviour specs against the researchbot agent SUT
go run ./cmd/mentat run features/

# 3. inspect a run manually
go run ./cmd/mentatctl agent run --target research-agent --scenario happy
go run ./cmd/mentatctl agent tools --last

make harness-down
```

## How it works

`Gherkin (.feature) → godog → engine → drive SUT → resolve trace (Tempo) →
comparators (sequence / budgets / result) on the Evidence forest → JUnit`.

A run is tagged with `test.run.id` and may span several traces; correlation resolves
by that tag and merges them. Comparators consume only `Evidence` (trace forest +
captured output), which keeps them portable across agents and microservices.

## Layout

- `cmd/mentat` — the behaviour-test runner (embeds godog)
- `cmd/mentatctl` — manual driver: `agent run/trace/tools/replay/diff`
- `internal/` — `engine`, `driver`, `correlate`, `store`, `comparator`, `steps`, `ctl`
- `tracelab/` — deterministic SUTs (`researchbot`); `deploy/` — Tempo + Collector
- `docs/superpowers/specs` — design; `docs/superpowers/plans` — implementation plans;
  `docs/architecture/mentat-architecture.html` — interactive diagram

## Development

`CLAUDE.md` is the contributor guide: uber gomock, table-driven tests, 80% coverage
floor, no silent fallbacks, interfaces + manual DI. `make ci` runs the full gate.
Subagents (`go-test-writer`, `go-coder`, `go-reviewer`) and skills (`/traces`,
`/coverage`) live under `.claude/`.
```

- [ ] **Step 2: Verify the internal links resolve**

Run:
```bash
for p in CLAUDE.md docs/superpowers/specs docs/superpowers/plans docs/architecture/mentat-architecture.html deploy; do
  test -e "$p" && echo "ok: $p" || echo "MISSING: $p"
done
```
Expected: every path `ok` (the architecture file and docs already exist; `deploy/` and
`cmd/*` exist once Plan 1/2b land).

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: top-level README with quickstart and layout"
```

---

## Done criteria for Ops & CI

- `make lint test cover` (a.k.a. `make ci`) passes locally once code exists.
- `.github/workflows/ci.yml` parses; on push it runs check + mock-drift + e2e.
- `README.md` quickstart commands work against the harness.
- The coverage threshold lives in exactly one place (`coverage.sh`), referenced by
  both `make cover` and CI.

With this, **all of v1 is planned**: Plans 1, 2a, 2b, 2c (product + tests) and this
ops/CI plan. The post-v1 phases (microservices/http, shape, semantic, breadth) remain
future spec→plan cycles.
