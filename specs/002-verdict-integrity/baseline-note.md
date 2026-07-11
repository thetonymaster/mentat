# Pre-feature Baseline — 002-verdict-integrity

Recorded at the start of implementation (T001). Branch `002-verdict-integrity`
cut from `main` @ e8a6b1a.

## Hermetic suite (`go test ./...`, no e2e)

- **Result: GREEN.** All packages `ok`; no `FAIL`.
- Build: `go build ./...` clean.

## Full `make ci` (lint + test -race + cover)

- Not re-run in full at baseline; `go test ./...` green and `go build ./...` clean
  establish the green starting point for the red→green pairs. `make ci` is the
  gate at each phase checkpoint and in Phase 7 (T032).

## e2e wall time (baseline)

- **Not captured at baseline.** e2e (`//go:build e2e`) requires `make harness-up`
  (Docker + Tempo/collector), which was not available at baseline — so there is no
  pre-feature e2e timing to record here.

---

## Post-implementation e2e timing (T032 — NOT baseline data)

- Captured at T032 close with the harness up: full `go test -tags e2e ./e2e/`
  (all meta suites + the new `TestErrorStatusL3` A1 proof) = **56.5s** wall,
  suite green. `TestErrorStatusL3` alone ≈ 10s (Tempo ingestion-bound).

## Notes on current state (reality check before implementation)

- `trace.Span` already has both `Kind` and `Status` string fields, but **no**
  canonical `StatusUnset/StatusOk/StatusError` constants and **no**
  `NormalizeStatus/NormalizeKind` — Phase 2 adds these.
- `core.Evidence` has `Failed` + `FailureKind` but **no** `FailureMsg`; engine
  drops `res.Output` on resolve failure (`engine.go:112`).
- No `poll.searchLimit` in config; no `DerivationNote` in report.
- Conclusion: feature 002 is essentially unimplemented — clean red→green runway.
