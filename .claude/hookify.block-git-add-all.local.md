---
name: block-git-add-all
enabled: true
event: bash
action: block
pattern: git\s+add\s+(-A|--all|\.(\s|$))
---

🛑 **`git add .` / `-A` / `--all` is forbidden in this repo.**

CLAUDE.md: *"`git add .` is forbidden — add files individually. Know what you're committing."*

**Stage each file explicitly instead:**

    git status                      # see what changed
    git add internal/foo/bar.go internal/foo/bar_test.go

Add only the files that belong in this commit.
