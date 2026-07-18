# Spec 011 prompt — mentatctl help overhaul + CLI/authoring UX completeness

Input for `/speckit-specify`. Feature name suggestion: `011-cli-ux-completeness`.
Core is the mentatctl help overhaul; the attached cluster items are the remaining
P1/P2 user-ergonomics findings from the 2026-07-18 audit (at `b1aabb4`) — trim at
specify/clarify time if the scope should stay narrower. Persona scores at audit
time: spec author 7/10, CI operator 6/10, red-run debugger 8/10.

## Feature description (pass this to speckit-specify)

Close the user-ergonomics gaps in both CLIs and the spec-authoring loop. Every
strength of the product (best-in-class trace-not-found diagnosis, drift-proofed
step reference, static validate) is currently discoverable only by users who
survive an unhelpful help system and undocumented schemas.

### Core: mentatctl help overhaul

Today (`cmd/mentatctl/main.go`): `mentatctl agent --help` errors with
`unknown subcommand "--help"` exit 1 (`checkDomainVerb`, main.go:64); the only
help is a single usage line naming six verbs with no descriptions; all verbs
share one flag set so `mentatctl agent replay --help` shows irrelevant flags
(`-scenario`, `-save`, `-prompt`); replay's REQUIRED positional run id
(`internal/ctl/replay.go:19`) appears in no help text; there is no mentatctl docs
page (`README.md:288` is one line).

Required: `--help`/`-h` works at every level (top, domain, verb) and exits 0;
per-verb flag sets showing only that verb's flags plus its positionals and one
usage example each; one-line description per verb in the domain listing; a
`docs/mentatctl.md` reference generated or drift-checked the same way
`docs/steps.md` is (006 pattern, `cmd/mentat/steps_cmd.go`).

### Cluster items (each independently shippable)

1. **Consistent `--help` contract on `mentat` too.** Today: `mentat --help` exits
   2, `mentat run --help` exits 0, `mentat validate --help` exits 2, and
   steps/validate leak `mentat: flag: help requested` after usage
   (`cmd/mentat/main.go:42-48`). All help paths exit 0, no leak; top-level usage
   must mention `-config`, `-v/-vv`, and report flags. Document the
   flags-before-paths rule (flag pkg stops at first positional — today documented
   only in a test comment, `e2e/golden_test.go:86-88`) in usage AND in the
   feature-path-not-found error message.
2. **Run id on red stdout.** Comparator failures render reasons but no run id
   (`internal/steps/steps.go:270,589`); the id needed for `/traces` or
   `mentatctl agent trace` is only in `-v` narration or json/html reports. Print
   the run id once per failed scenario in default stdout output. (Stdout golden
   churn expected — regenerate deliberately.)
3. **Typo'd-step UX.** A misspelled step at run time emits godog's stock "write
   this Go func" snippet — wrong advice for a closed, embedded step vocabulary.
   Replace/augment the undefined-step output with a pointer to `mentat steps` and
   a nearest-match suggestion; ideally `mentat run` prechecks step binding the
   way `validate` does (`[unbound-step]`, precheck runs only shape patterns today,
   `internal/steps/steps.go:120`).
4. **Expectations/shape schema docs.** The YAML schema
   (`name`/`description`/`clauses`/`child`/`of`; loader
   `internal/expectations/expectations.go`) is documented nowhere user-facing,
   and the only committed example (`expectations/bad_expectation.yaml`) is
   deliberately impossible. Add a docs page + a committed VALID example wired
   into a passing feature.
5. **Complete mentat.yaml reference.** `poll` (interval/stableFor/timeout/
   searchLimit), `pricing`, `tempo`, `otlpEndpoint`, `expectations` are
   undiscoverable except by reading `internal/config/config.go:40-58`. Produce a
   full schema reference (drift-checked against the struct tags if feasible —
   same spirit as docs/steps.md).
6. **Small error-quality fixes:** `internal/ctl/replay.go:31` "feature failed
   against run %s (status %d)" → include the failing reasons, not a bare number;
   pinned-path trace-not-found (`internal/correlate/correlate.go:366`) gets the
   same enriched diagnosis (store endpoint, query, checklist) as the poll path
   at :257/:306 — replay users currently get the weaker error.
7. **Pretty-output noise:** every step prints `# metadata.go:75 -> *world`
   (see `cmd/mentat/testdata/golden-green.txt`) — internal file:line meaningless
   to spec authors, repeated per step. Suppress or replace with the step's
   doc reference. (Stdout golden churn — deliberate regen.)

## Constraints

- Behaviour changes (help exit codes, stdout content, precheck, error text) →
  go-test-writer TDD; the cmd packages are coverage-exempt but their _test.go
  suites exist and must stay green; docs pages → go-coder.
- Both stdout goldens (hermetic `mentat_golden_test.go` + live
  `e2e/golden_test.go` vs `cmd/mentat/testdata/golden-green.txt`) will churn on
  items 2/3/7: regenerate deliberately, and remember local `make ci` does NOT run
  the live golden — the CI e2e job on the PR is the gate (see the
  e2e-golden-gate memory note).
- Exit-code contract (0/1/2/130) is public (`Results.ExitCode` godoc) — help
  paths moving to 0 must not disturb the run/validate error codes; pin with tests.
- No new public API expected; if any facade change sneaks in, it goes through
  the 009-hardened surface golden with the diff called out.

## Out of scope

Custom-comparator steps (010), tag-based default exclusion of `features/meta/`
(if wanted, it's its own small spec — the Bucket-1 README fix already de-risks
the quickstart), any mentatctl new *verbs*.
