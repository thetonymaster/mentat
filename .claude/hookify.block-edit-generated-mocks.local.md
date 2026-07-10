---
name: block-edit-generated-mocks
enabled: true
event: file
action: block
conditions:
  - field: file_path
    operator: regex_match
    pattern: internal/core/mocks/
---

🛑 **This is a generated file — don't hand-edit it.**

`internal/core/mocks/` is produced by `mockgen` (the `//go:generate` directive in
`internal/core/core.go`). Manual edits are silently clobbered the next time anyone
runs `go generate ./...`, and CI's `mocks` job hard-fails on drift.

**To change a mock, change the interface** in `internal/core/core.go`, then:

    go generate ./...
