# Contract: Lifecycle Configuration, Signals, and Failure Shapes

## Config keys (mentat.yaml)

```yaml
run_timeout: 5m        # suite default; "unbounded" opts out explicitly
kill_grace: 10s
targets:
  research-agent:
    run_timeout: 10m   # per-target override
```

- Omitted keys → documented defaults (5m / 10s). Worst-case scenario wall time =
  `run_timeout + kill_grace` (documented).
- Unknown string values (typos) are parse errors, not silent defaults.

## Signal contract (mentat run)

| Event | Behaviour |
|-------|-----------|
| SIGINT/SIGTERM (first) | cancel in-flight scenario; SIGTERM the SUT process group; SIGKILL group after `kill_grace`; write all configured reports (completed results + interrupted marker); exit 130 |
| SIGINT/SIGTERM (second) | immediate exit (force) |
| normal completion | unchanged (exit 0 green / 1 red) |

## Report interrupted marker

- JSON: top-level `"interrupted": true` (omitted on a clean run — `omitempty`).
- HTML: visible banner — `⚠ suite interrupted — <N> scenario(s) completed before the
  signal` (class `interrupted`). N is the completed count; the total-planned count M
  is unknown at report time — an interrupt leaves un-started scenarios uncollected.
- JUnit: suite-level `<property name="interrupted" value="true"/>`.
- **JUnit is now mentat-native** (rendered from the collector via the `junit`
  reporter, registered alongside `json`/`html`), not godog's `--format junit`. So
  `--junit` no longer suppresses the console pretty output, and it carries the
  interrupted property. All three formats emit through `report.EmitReports`.
- Files are written atomically (temp file in the target dir + rename); a killed run
  never leaves a truncated report file.

## Failure-message shapes (substrings tests may pin)

| Situation | Shape |
|-----------|-------|
| Run timeout | names target, phase, elapsed budget, wraps `context.DeadlineExceeded`: `engine: drive "research-agent": run timeout after 5m0s (phase: drive): context deadline exceeded` (phase is `drive` or `resolve`) |
| Interrupt mid-scenario | scenario failure surfaces the run's failure; report marked interrupted; process exits 130 |
| Report write failure | `writing <fmt> report "<path>": create temp file in "<dir>": ...` (atomic-emit path; was `create <fmt> report ...`) |
| Post-seal registration | panic message contains `registries are sealed at the composition root` |
| Parallel batch structural error | batch error returned promptly; iterations not started never drive (observable via drive-count in tests) |

## Compatibility

- Existing configs without the new keys keep working (defaults apply). The only
  behaviour change for healthy suites: a pathological >5m single run now needs an
  explicit `run_timeout` override — called out in the README.
- Scenario steps now run under the scenario context (was `context.Background()`);
  feature files are untouched. `ctl.ReplayFeature` callers gain real cancellation;
  no API change.
- `--junit` moved from godog's native format to the mentat collector-based `junit`
  reporter (see above): the XML shape changed and the console output is no longer
  suppressed. No test pinned godog's junit output.
- Seam registries seal at the end of `engine.Build`/`BuildStore` (re-entrant, so
  building multiple engines still works); registering a seam after build panics.
  Tests that register custom seams call `registry.ResetForTest(t)`.
