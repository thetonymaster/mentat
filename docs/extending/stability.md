# Stability policy (pre-1.0)

This is the compatibility contract for the public `github.com/thetonymaster/mentat`
facade — the seam interfaces, the `With*` registration options, `Run`/`Results`/
`ScenarioResult`, `LoadConfig`, and the `Config` surface. Read it before you build
an extension on top of the facade so you know what "v0" promises and what it does
not.

## The module is v0

Mentat is pre-1.0. Under [semver](https://semver.org/) v0, **breaking changes to the
public surface are permitted** — a seam signature may change, a field may be added
or removed, a type may be renamed. So pin a specific module version in your own
`go.mod` and read the changelog before upgrading; do not assume a minor bump is
always non-breaking while the module is v0.

What v0 does promise is that a breaking change is never *accidental*. Every change to
the exported surface must be **deliberate**, and "deliberate" has a concrete,
enforced definition below.

## A deliberate change: the three acts

A PR that changes the public surface MUST, in the *same* PR:

1. **Update the golden surface file.**
   `specs/007-public-extension-api/contracts/public-surface.golden` records the
   exported surface — every exported symbol, every seam interface's method set, and
   every exported field of every re-exported struct (see the reach section below).
   Regenerating it to match your change is the
   *acknowledgment act* — the diff to that file is the reviewer-visible record that
   the surface change was intended, not incidental.
2. **Add a CHANGELOG entry.** Describe the change under the appropriate heading
   (`Added` / `Changed` / `Removed`) in [`CHANGELOG.md`](../../CHANGELOG.md), naming
   the affected symbol.
3. **Write a migration note.** For a breaking change, tell an extension author what
   to do — the old shape, the new shape, and the mechanical edit to move between
   them.

A surface change that skips these is not a valid change; it is drift.

## The golden gate (how drift is caught)

Silent surface drift is a **CI failure**, not a review judgment call. A golden
surface test (`TestPublicSurfaceGolden` in `surface_test.go`) renders the facade's
exported surface into a canonical text form and diffs it against
[`specs/007-public-extension-api/contracts/public-surface.golden`](../../specs/007-public-extension-api/contracts/public-surface.golden).
It runs under plain `go test` (part of the standard gate), so:

- If you change the surface but forget to regenerate the golden, the test **fails
  and names the drifted symbol** — the mismatch points straight at what moved.
- To make it pass you regenerate the golden (act 1 above), which forces the change
  into the reviewed diff.

### What the gate catches

- Exported symbols **added, removed, or renamed** at the facade level.
- **Function and method signatures** — a changed parameter or result is a diff.
- **Interface method sets.** Each seam interface's methods are rendered
  individually (e.g. `method (Comparator) Compare(ctx context.Context, ev Evidence,
  e Expectation) (Verdict, error)`), so adding, removing, or re-signing a seam
  method is loud.
- **Struct fields of types declared in the facade package itself** — `Results` and
  `ScenarioResult` render with their fields inline.
- **Exported fields of re-exported structs.** Most of the public surface is built
  from **zero-cost type aliases** to internal types (`type Verdict = core.Verdict`,
  `type Target = config.Target`). The golden does not stop at the alias
  declaration: it expands each aliased struct into **one line per exported field**,
  named by the facade alias.

  ```text
  field (Verdict)[02] Qualifiers []string
  field (Target)[07] Completeness Completeness
  ```

  **Adding, removing, or re-typing an exported field of a re-exported struct fails
  `TestPublicSurfaceGolden`, and the failure names the drifted type** in
  parentheses. The field type is rendered exactly as written in the aliased
  package's source, so renaming a named field type is drift too. Unexported fields
  are omitted — they are not a public promise.

- **The declaration ORDER of those fields**, via the bracketed zero-padded ordinal.
  Permuting two fields changes no field name and no field type, but it is a real
  break: an unkeyed composite literal (`mentat.Verdict{true, nil, …}`) and
  positional reflection both bind by position, and neither errors at the consumer's
  call site. **Reordering fails `TestPublicSurfaceGolden`, naming both fields on
  both sides with their swapped positions.** Unexported fields do not consume an
  ordinal, so adding one is still invisible to the gate — an internal change stays
  internal.

### What the gate does not catch

Four boundaries, stated here so they are not tribal knowledge. All four are known
and accepted; none is a TODO.

1. **Aliases of map, func, and `any` types stay single-line.** Only *struct* and
   *interface* aliases are expanded. A declaration like `type ComparatorFactory =
   func(Config) (Comparator, error)` is rendered as written and needs no expansion —
   the declaration text already *is* the complete shape. This is by design
   ([`surface-golden-v2.md`](../../specs/009-extension-surface-integrity/contracts/surface-golden-v2.md)
   rule 3). Note the narrower case it also covers: an alias to a *named* map or func
   type in an internal package, such as `type Pricing = config.Pricing`, renders as
   just that line, so re-typing `config.Pricing` from `map[string]ModelRate` to
   something else is **not** a golden diff.

2. **Struct tags are not rendered.** The gate freezes field **name and type only**.
   Renaming a `yaml:"…"` tag on a `Config` or `Target` field is a real break for
   every user's `mentat.yaml` — and it produces **zero golden diff**. Tag changes
   are governed by author discipline and review, not by CI.

3. **Expansion is one level deep, and only through facade aliases.** A struct's
   fields are frozen when the facade itself re-exports that struct. A struct
   reachable only as the *type of a field* is not expanded in turn: `field
   (Verdict)[03] Detail *AggregateDetail`, `field (RunSpec)[…] Extract
   ExtractPolicy`, and `field (RunSpec)[…] HTTP HTTPSpec` freeze those field
   names, positions and type names, but the field
   sets of `AggregateDetail`, `ExtractPolicy`, and `HTTPSpec` are not themselves in
   the golden, because no `type AggregateDetail = core.AggregateDetail` alias exists
   on the facade. Adding a field to one of them is invisible to the gate. The
   mechanical fix, when one of these matters, is to re-export it from the facade —
   the alias line brings its field set into the golden with it.

4. **Types reachable through *seam* signatures are not guaranteed nameable.** The
   nameability sweep that feature 009 froze walks outward from `Config` and
   `Results` (see
   [`facade-nameability.md`](../../specs/009-extension-surface-integrity/contracts/facade-nameability.md));
   it does not walk seam-interface parameter and result types. Four types are
   therefore frozen in the golden but cannot be named from outside the module:
   `AggregateDetail`, `ExtractPolicy`, `HTTPSpec`, and `RunReport`. The practical
   consequence is sharpest for `RunReport`: **`Reporter` is an aliased seam
   interface that an external module cannot implement**, because it cannot write
   the type of its own method parameter. Likewise a Comparator author cannot
   construct a `Verdict` with `Detail` set, and a Driver author cannot build a
   `RunSpec` with `Extract` or `HTTP` set.

   This is a verified, deliberately-deferred gap, not an oversight: closing it
   means widening the public surface, which is a one-way door and gets its own
   spec rather than riding along in 009. Recorded as input to spec 010.

> Every symbol on the surface earns its place: the manifest rule is that a symbol
> appears in the contract *with a justification, or it does not get exported*. See
> [`contracts/public-surface.md`](../../specs/007-public-extension-api/contracts/public-surface.md)
> for the surface groups and per-symbol justification model.

## Tagging v1.0 is out of scope

Freezing the surface by tagging **v1.0** is an explicit future decision, deliberately
out of scope here. Until then the surface stays governed-but-mutable: changeable, but
only through the three acts above.

## See also

The seam guides under [`docs/extending`](.) show how to implement against each part
of this surface:

- [Writing a custom Driver](driver.md)
- [Writing a custom TraceStore](store.md)
- [Writing a custom Comparator](comparator.md)
- [Writing a custom Judge](judge.md)
- [The Evidence a comparator inspects](evidence.md)
- [Adding a new seam](new-seam.md) — the canonical seam taxonomy and the checklist
  a new seam must satisfy, including the surface acts above.
