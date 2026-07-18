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
   - fields are emitted in declaration order, but see the scope note below —
     declaration order is deliberately NOT frozen.

**Scope note (amended 2026-07-18 during T004, corrected against the source).**
This rule originally claimed "reordering is a golden diff by design — declaration
order is visible surface". That is false, and it contradicts a deliberate
pre-existing decision: `surfaceRender` `sort.Strings`'s the whole line set
(`surface_test.go:294`), which the T014-era comment at `surface_test.go:48`
documents as intentional — "the whole set is sort.Strings'd, so source
declaration order never churns the golden". Reordering fields therefore produces
**zero** diff, exactly as it already did for T028 interface methods. Nothing in
[spec.md](../spec.md) requires reorder detection, so the sort stands and the
claim is withdrawn rather than the code changed.

**Second scope boundary: struct tags are not rendered.** The gate freezes field
name + type only, so renaming a `yaml:"..."` tag — a real config-surface break —
remains invisible. Both boundaries are stated in `docs/extending/stability.md`
rather than left as tribal knowledge.
3. **Explicitly out**: aliases of map, func, and `any` types keep single-line
   rendering. This scope boundary is restated in `stability.md`.

   **Rationale corrected 2026-07-18 (T007).** This rule originally justified itself
   with "their declaration text already is their complete shape". That holds only
   when the right-hand side is written inline (`type ComparatorFactory =
   func(Config) (Comparator, error)`). It is **false** for an alias to a *named*
   type in an internal package: `type Pricing = config.Pricing` renders as exactly
   that line and records `map[string]ModelRate` nowhere, so re-typing
   `config.Pricing` produces zero golden diff. The single-line rendering stands;
   the justification does not cover the named-type case, which `stability.md`
   states explicitly instead.

4. **Depth**: expansion is one level deep and only through facade aliases. A struct
   reachable solely as the *type of a field* is not expanded in turn — verified
   instances at `2f4073d` + US1: `AggregateDetail`, `ExtractPolicy`, `HTTPSpec`.
   The mechanical closure is a facade alias, which pulls the field set into the
   golden with no renderer change; US3's reachable-set sweep (FR-006/FR-007) is
   what determines whether these get aliased.

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
