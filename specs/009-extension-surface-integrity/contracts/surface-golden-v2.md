# Contract: Surface Golden v2 — struct-field freezing

**Consumers**: maintainers reviewing PRs; `TestPublicSurface` (`surface_test.go`);
extension authors reading `docs/extending/stability.md`.
**Fulfils**: FR-001..FR-005, SC-001. Decision: [research.md R1](../research.md).

## Rendering rules

For every declaration on the public surface (`mentat.go`, facade-owned decls in
`run.go`), the golden (`specs/007-public-extension-api/contracts/public-surface.golden`)
renders:

1. **Unchanged**: top-level funcs/consts/vars; alias lines; interface aliases
   expanded to their full method sets (008 T028 behaviour — MUST NOT regress).
2. **New — struct expansion**: an alias whose right-hand side resolves (via the
   existing import-dir resolution, `surfaceCtx`) to an `*ast.StructType` is
   followed by its **exported fields**, one per line, using the same layout
   convention T028 established for interface methods:
   - field name + type exactly as written in the aliased package's source,
     printed with `go/printer` mode 0 (formatting-noise-proof);
   - unexported fields omitted (e.g. `config.ExtractConfig.compiled` — it is not
     a public promise);
   - embedded fields rendered as written (the embedded type, if public, is frozen
     by its own entry);
   - field order as declared (reordering is a golden diff by design — declaration
     order is visible surface).
3. **Explicitly out**: aliases of map, func, and `any` types keep single-line
   rendering — their declaration text already is their complete shape. This scope
   boundary is restated in `stability.md`.

## Gate behaviour

- Adding, removing, or re-typing an exported field of any rendered struct ⇒
  `TestPublicSurface` FAILS, and the failure output names the drifted type.
- Golden regeneration happens **only** under `MENTAT_UPDATE_GOLDEN=1`
  (`mentat_golden_test.go:28`); no default-mode test run writes the golden.
- This feature regenerates the golden **exactly once** to capture current field
  sets (~25 aliased structs, including the two previously-invisible drifts
  `Verdict.Qualifiers` and `Target.Completeness`); the full diff is itemized in
  the PR body per the golden-change protocol.

## Proof obligations

- **Mutation rehearsal** (documented in `surface_test.go` beside the T014/T028
  blocks at :42-52/:54-70): temporarily add an exported field to `core.Verdict`
  → FAIL naming `Verdict`; revert → PASS. The rehearsal narrative (dates,
  observed failure text) is committed as a comment, like its precedents.
- **No-regression**: interface method-set expansion output for existing entries
  is byte-identical before/after, except lines added by struct expansion.
- **Docs truth**: `docs/extending/stability.md` drops the interim-gap section
  (:53-84) and restores the strong claim — in the same PR that makes it true.
