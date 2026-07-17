# Contract: CLI Surface Additions

## mentat steps

```
mentat steps [--format md|text]
```

Prints every registered step: pattern, summary, example — grouped (drive /
sequence / result / shape / budgets / aggregate / judge), followed by the
selector/quantifier/ordinal grammar and CEL variable reference. `md` output is
byte-identical to the committed `docs/steps.md` (drift test).

## mentat validate

```
mentat validate [paths...] [--config mentat.yaml] [--format text|json]
```

- Default paths: the configured/conventional features directory.
- Runs all authoring prechecks without driving SUTs or contacting store/judge
  (it constructs no store/driver/correlator at all — no network by construction).
- Reports **all** findings; exit 1 on any finding or when no feature files are
  found; exit 0 clean.
- `--format json` emits `{"findings": [{"file", "line", "class", "message"}]}`
  (`internal/steps.Finding`); `--format text` (default) prints `file:line: [class] message`.
- Budget: completes in < 1s on this repo's corpus (hermetic test enforces no
  network by construction, not by timing).

## mentat run — output formats

- `--junit <file>` now **adds** the JUnit formatter; console (`pretty`) output
  is always present. JSON/HTML report flags unchanged and composable.
- JUnit write failure still fails the run (existing reporter contract).

## mentatctl agent run

```
mentatctl agent run [--prompt "..." | --prompt-file f | --prompt-file -] \
                    [-o answer.txt] [--timeout 90s] <target>
```

Summary (additive lines, existing lines unchanged for script compatibility).
Exact spellings are golden-locked in `internal/ctl/format.go` (`RenderSummary`) and
`internal/ctl/testdata/run-golden.txt`:

```
run <run id>
tools: <ordered tool names>
spans: <n>
answer: <answer>
tokens: in <in> out <out>
cost: $<x.xxxx>
latency: <ms> ms
traces: <space-separated root trace ids>
```

## Compatibility

- All new subcommands/flags are additive; no existing flag changes meaning.
- `--junit` behaviour change (console no longer silenced) is the designed
  behaviour; changelog callout.
