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

- JSON: top-level `"interrupted": true`
- HTML: visible banner ("suite interrupted — N of M scenarios completed")
- JUnit: suite-level `<property name="interrupted" value="true"/>`
- Files are written atomically (temp + rename); a killed run never leaves a
  truncated report file.

## Failure-message shapes (substrings tests may pin)

| Situation | Shape |
|-----------|-------|
| Run timeout | names target, phase, elapsed: `drive "research-agent": run timeout after 5m0s (phase: drive)` |
| Judge timeout | phase attributed `judge`, includes elapsed |
| Interrupt mid-scenario | scenario failure names the signal; report marked interrupted |
| Post-seal registration | panic message contains `registries are sealed at the composition root` |
| Parallel batch structural error | batch error returned promptly; iterations not started never drive (observable via drive-count in tests) |

## Compatibility

- Existing configs without the new keys keep working (defaults apply). The only
  behaviour change for healthy suites: a pathological >5m single run now needs an
  explicit `run_timeout` override — called out in the changelog/README.
- godog step-signature change is internal; feature files are untouched.
- `ctl.ReplayFeature` callers gain real cancellation; no API change.
