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

1. **Update the golden surface file.** `contracts/public-surface.golden` records the
   exact exported surface. Regenerating it to match your change is the
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
surface test renders the package's exported surface — every exported type, function
signature, and struct field — into a canonical text form and diffs it against
`contracts/public-surface.golden`. It runs under plain `go test` (part of the
standard gate), so:

- If you change the surface but forget to regenerate the golden, the test **fails
  and names the drifted symbol** — the mismatch points straight at what moved.
- To make it pass you regenerate the golden (act 1 above), which forces the change
  into the reviewed diff.

That is the whole mechanism: the golden file is the source of truth for "what is
exported", and the test makes any divergence from it loud. There is no way to change
the surface quietly — the acknowledgment act (regenerating the golden) is exactly the
step CI demands.

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
