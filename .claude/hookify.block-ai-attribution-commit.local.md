---
name: block-ai-attribution-commit
enabled: true
event: bash
action: block
pattern: git\s+commit[\s\S]*(Co-[Aa]uthored-[Bb]y|Generated with Claude|🤖)
---

🛑 **No AI attribution in commits.**

CLAUDE.md: *"No AI attribution in commits or PRs (no 'Generated with…', no
`Co-Authored-By`)."*

Rewrite the message with **no** `Co-Authored-By:` trailer, no "Generated with
Claude", and no 🤖 — just a plain Conventional Commit:

    git commit -m "feat(core): add span-attribute source"
