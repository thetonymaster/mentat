# Contract: Narration & Error Enrichment

## Verbosity contract

| Flag | Level | Audience |
|------|-------|----------|
| (none) | silent | happy-path output byte-identical to pre-feature (golden-tested) |
| `-v` | Info | CI logs: drive/resolve lifecycle, one line per stage |
| `-vv` | Debug | local debugging: injected env (Mentat-set keys only), per-poll rounds |

All narration → **stderr**. stdout remains report/godog territory.

## Pinned substrings (tests + user scripts may rely on these)

| Where | Pinned |
|-------|--------|
| resolve timeout error | `store: <endpoint>`, `query: { .test.run.id = "<id>" }`, `checklist:` |
| unknown config key | the key name and path (e.g. `poll.timout`) |
| unregistered adapter | adapter name + `registered:` + sorted driver list |
| ordinal parse error | the ordinal text |
| log attributes | `run_id`, `store_endpoint`, `query`, `spans`, `round` |

Message prose around the pinned parts may evolve; the pinned parts are API.

## Injection policy (SUT-visible contract)

| Situation | SUT environment result |
|-----------|------------------------|
| config endpoint set | config value (overrides ambient) |
| config endpoint unset, ambient set | ambient value untouched |
| both unset | variable absent (SUT default behaviour) |
| ambient resource attributes present | merged with Mentat tags; Mentat keys (incl. `test.run.id`) win collisions |
| ambient resource attributes malformed | hard error naming the malformed value |

## Compatibility

- Intentional break: configs containing unknown keys now fail to load (they were
  already silently misconfigured — the failure is the fix). Changelog callout.
- Intentional break: `adapter: mcp`/`grpc` now fails at startup instead of
  mid-suite (no driver exists; behaviour was already broken, just later).
- Error classes from features 002/004 unchanged — messages gain fields only.
