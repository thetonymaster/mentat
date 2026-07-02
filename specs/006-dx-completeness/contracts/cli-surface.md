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
- Runs all authoring prechecks without driving SUTs or contacting store/judge.
- Reports **all** findings (file:line, class, message); exit 1 on any finding
  or when no feature files are found; exit 0 clean.
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

Summary (additive lines, existing lines unchanged for script compatibility):

```
run: <run id>
tools: <ordered tool names>
spans: <n>
answer: <answer>
tokens: <in>/<out>
cost: $<x.xxxx>
latency: <ms> ms
traces: <root trace ids>
```

## Compatibility

- All new subcommands/flags are additive; no existing flag changes meaning.
- `--junit` behaviour change (console no longer silenced) is the designed
  behaviour; changelog callout.
