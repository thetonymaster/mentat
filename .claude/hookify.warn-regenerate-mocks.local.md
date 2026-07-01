---
name: warn-regenerate-mocks
enabled: true
event: file
action: warn
conditions:
  - field: file_path
    operator: regex_match
    pattern: internal/core/core\.go$
---

⚠️ **You edited the mockgen source (`internal/core/core.go`).**

If you changed any interface (`Driver`, `TraceStore`, `Correlator`, `Comparator`,
`Reporter`, `Judge`), the committed mocks are now stale and CI's `mocks` drift job
will fail.

**Before committing, regenerate and stage the mocks:**

    go generate ./...
    git add internal/core/mocks/mock_core.go
