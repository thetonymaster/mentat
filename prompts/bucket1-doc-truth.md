# Bucket 1 — doc-truth & hygiene chore PR

Direct execution prompt — no spec needed (docs + hygiene + one small test bump).
Route doc/hygiene items to **go-coder**, the orderflow test item to **go-test-writer**.
Evidence below was gathered 2026-07-18 at commit `b1aabb4` by a five-agent audit;
**re-verify every file:line before editing** — the repo may have moved.

## Context

A full audit found the code production-grade but the surrounding docs actively
misleading. Every item below is a small, verified falsehood or hygiene problem.
Goal: after this PR, every gate and doc in the repo tells the truth.

## Items (one commit each or logically grouped; Conventional Commits)

1. **README quickstart cannot go green.** `README.md:14` says
   `go run ./cmd/mentat run features/`, but `features/meta/` holds 14 deliberately-red
   meta features and `mentat validate features/` exits 1 (unknown target "svc" in
   `features/meta/bad_meaning.feature:10`). Fix: point the quickstart at the
   golden-proven `features/research_agent.feature` (see `e2e/golden_test.go:96`).
   Add one sentence explaining `features/meta/` is a deliberately-failing corpus.
   Do NOT change run/tag behaviour here — that would be a feature.

2. **README mentatctl example is rejected by the binary.** `README.md:166`:
   `go run ./cmd/mentatctl -v agent run ...` fails — `splitDomainVerb`
   (`cmd/mentatctl/main.go:34-43`) requires domain first; `-v` is registered
   per-verb (`main.go:104`). Fix the example to put flags after the verb.
   **Verify by running the corrected command** (expect a real error about the
   target/harness, not `usage:` exit 2).

3. **`mentat.go:13-15` package godoc claims the package is a skeleton** ("Run …
   arrive in later tasks") — false; `Run`, all `With*` options, `Config`, `Results`
   shipped in 007. Delete/replace that paragraph with an accurate one-paragraph
   overview. Optional secondary: strip repo-internal citations meaningless on
   pkg.go.dev ("audit finding G1", "SC-006", "Constitution I"). After editing,
   run `go test . -run TestPublicSurface` — comment-only edits must NOT churn the
   golden; if they do, STOP and report.

4. **Phantom "(008)" deferral, two places.** `run.go:112-114` (ComparatorFactory
   godoc) and `docs/extending/comparator.md:80` defer custom-comparator Gherkin
   steps "to a dedicated future spec (008)" — but 008 shipped as trace-completeness.
   Replace with: "planned as spec 010 (custom comparator steps); not yet started."
   Also fix the same stale pointer in `mentat_run_test.go:470` comment if present.

5. **CLAUDE.md footer stale.** Last paragraph points at
   `specs/007-public-extension-api/plan.md` as "the current plan"; 007 and 008 are
   merged. Replace with: features 001–008 are shipped; consult `specs/` for history
   and the highest-numbered spec dir with open tasks (if any) for in-flight work.

6. **005 spec-text divergence.** `specs/005-observability-config/spec.md` US1 AS2
   puts injected-env/poll detail under `-v`; implementation + contract gate it
   under `-vv` (acknowledged, never reconciled — see
   `specs/005-observability-config/tasks.md:136-140`). Update the spec text to
   match the implemented `-vv` behaviour and note the reconciliation date.

7. **`docs/extending/stability.md:41-43` overclaims.** It says the surface golden
   captures "every exported type, function signature, and struct field" — false for
   aliased structs (`Verdict.Qualifiers` and `Target.Completeness` landed with zero
   golden churn). Interim honest wording: interface method sets are frozen (008
   T028); struct-alias fields are NOT yet (planned: spec 009). Spec 009 will
   restore the strong claim.

8. **`examples/kafkaecho` has no README.** `README.md:281` sells it as the
   CI-enforced proof the surface suffices; add a short README.md there: what it is,
   the internal-import tripwire (`Makefile:32`), how `make example` gates it.

9. **Hygiene:** (a) remove the stale worktree: `git worktree remove
   .claude/worktrees/phase4` — first verify branch `worktree-phase4` is merged
   (`git branch --merged main`); if not merged, STOP and ask Q. (b) Delete stale
   untracked root binaries `./mentat`, `./mentatctl` (gitignored; the audit found
   `./mentat` predates 008 and rejects the repo's own mentat.yaml). (c) Delete
   stray `cover.out` at root if present (gitignored).

10. **orderflow coverage margin** (go-test-writer, separate commit): package
    `tracelab/orderflow` sits at **80.5%** vs the 80% CI floor. Add table-driven
    tests for `scenarios.go` `RunLateFlush`/`runSentinel` (63.6%) and `system.go`
    `Drive` (74.1%) to lift the package to ≥85%. Verify with the coverage skill
    (`.claude/skills/coverage/coverage.sh ./tracelab/orderflow/`).

## Constraints (repo rules — enforced by hooks)

- `git add <file>` individually; `git add .` is blocked.
- No AI attribution in commits/PR.
- `gofmt -l .` clean, `go vet ./...` clean, `make ci` green before PR.
- Doc/comment-only changes must not alter the surface golden or stdout goldens;
  item 1 does not touch step registration, so the e2e golden should be unaffected —
  but note CI runs the live e2e job on the PR; watch it.
- Batch size 3: after every ~3 items, run the relevant checks before continuing.

## Done means

Every command shown in README works as written on a fresh clone; no doc in the
repo references a spec number for something that spec doesn't contain; `make ci`
green; CI e2e job green on the PR.
