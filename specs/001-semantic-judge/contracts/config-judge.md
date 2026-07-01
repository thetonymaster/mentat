# Contract — `mentat.yaml` `judge:` block

## Schema

```yaml
judge:
  backend: claude            # default "claude"; resolved from the judge registry
  model: claude-opus-4-8     # default "claude-opus-4-8"; e.g. claude-haiku-4-5 (cheapest), claude-sonnet-4-6
  votes: 1                   # default 1; best-of-N majority (odd N recommended)
  temperature: 0             # optional; applied only on models that accept it (Sonnet 4.6 / Haiku 4.5)
```

All fields optional; the whole block may be omitted (defaults apply) — a project that never
writes `the result means` never needs it.

## Defaults & validation (`config.Load`, no silent fallbacks)

| Field | Default | Validation |
|---|---|---|
| `backend` | `"claude"` | resolved at `engine.Build`; unknown name → hard error there (FR-005) |
| `model` | `"claude-opus-4-8"` | passed through to the backend; an invalid model surfaces as a Judge call error |
| `votes` | `1` | `< 1` → error `"judge.votes must be >= 1, got %d"`; **even `> 1` → error** naming the value (majority is undefined on a tie; odd N required). Stricter than runtime-tie-only — chosen for fail-fast at config load. |
| `temperature` | `0` | `< 0` or non-finite → error `"judge.temperature must be finite and >= 0, got %v"` (mirrors pricing validation) |

## Credentials
- `ANTHROPIC_API_KEY` (environment), read by the SDK. Not a config field (secret stays out of
  `mentat.yaml`). A missing key surfaces as a hard error before any model call (US2-AC3).

## Data egress note (FR-016)
- Selecting `the result means` + `backend: claude` sends the run's result content to the
  Anthropic API. This is the documented opt-in; no redaction in v1. The block's presence in
  config + the step in a spec is the egress consent surface.
