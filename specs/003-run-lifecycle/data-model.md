# Phase 1 Data Model: Run Lifecycle

Additive; references research.md R1–R6.

## Config (internal/config, mentat.yaml)

| Key | Type | Default | Rules |
|-----|------|---------|-------|
| `run_timeout` | duration or `"unbounded"` | `5m` | Suite-level bound per SUT run. Parse error on any other non-duration string. |
| `kill_grace` | duration | `10s` | Grace between polite signal and forceful kill; also the pipe `WaitDelay`. Must be > 0. |
| `targets.<name>.run_timeout` | duration or `"unbounded"` | inherits suite | Per-target override. |

Resolution: target override → suite value → built-in default. Values surface in
`config.Config` as resolved `time.Duration` + explicit `Unbounded bool` (no magic
zero meaning "forever" — constitution IV).

## RunSpec / driver (internal/core, internal/driver)

| Element | Change | Rules |
|---------|--------|-------|
| `RunSpec.KillGrace time.Duration` | **new** | Passed by the engine so the shell driver sets `WaitDelay` and escalation without knowing config. |
| shell driver process handling | changed | Own process group; cancel = SIGTERM group, SIGKILL group after grace; `WaitDelay` = grace. Output captured up to finalization is preserved in `RunResult`. |

## Engine (internal/engine)

| Element | Change | Rules |
|---------|--------|-------|
| `driveOnce` context | derived | `context.WithTimeout(scenarioCtx, runTimeout)` unless unbounded. Timeout failure text: target, phase (`drive`/`resolve`), elapsed. |
| `DriveN` parallel batch | changed | Batch-level `WithCancel`; structural error cancels not-yet-started iterations (checked before semaphore acquire and before drive). |
| Phase attribution | **new** | Failure wrapping records which engine call was in flight; reused by steps for FR-007 messages. |

## Steps (internal/steps)

| Element | Change | Rules |
|---------|--------|-------|
| Step signatures | changed | Context-aware godog signatures; `world` holds the scenario ctx from `sc.Before`. `context.Background()` is banned in this package (enforced by test grepping or lint note). |

## Registry (internal/registry)

| State | Transition | Rules |
|-------|------------|-------|
| open → sealed | `Seal()` called by `engine.Build`/`BuildStore` on completion | `Register*` while sealed → panic with descriptive message (programming error). Maps mutex-guarded in both states. `ResetForTest(t *testing.T)` reopens for tests only. |

## Report (internal/report)

| Field | Change | Rules |
|-------|--------|-------|
| `Run.Interrupted bool` | **new** | Set when the signal context cancelled the suite. Rendered: JSON field, HTML banner, JUnit suite property. |
| file emission | changed | Write temp file in target dir + rename (no corrupt artifacts on interrupt). Emission runs on all exit paths of `cmd/mentat`. |

## Exit codes (cmd/mentat)

| Condition | Exit |
|-----------|------|
| all green | 0 |
| assertion/harness failures | 1 (unchanged) |
| interrupted by signal | non-zero, distinct from plain failure (130 for SIGINT convention; documented in contract) |
