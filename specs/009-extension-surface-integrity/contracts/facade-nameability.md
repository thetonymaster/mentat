# Contract: Facade nameability — every reachable type is constructible

**Consumers**: external extension authors (internal imports are
compiler-forbidden outside the module and policed for `examples/` by
`Makefile:32`); `mentat_external_test.go`; `examples/kafkaecho`.
**Fulfils**: FR-006..FR-007, SC-002. Decision: [research.md R3](../research.md).

## Definition

**Reachable set**: the transitive closure of exported struct types found in
exported fields starting from `mentat.Config` and `mentat.Results` (including
slice/map/pointer element types and embedded types).

**Nameability invariant**: every member of the reachable set has a facade name
(`mentat.X`), so an external module can write a composite literal for it
importing only `github.com/thetonymaster/mentat`.

## Known gap (verified at `2f4073d`)

- `mentat.Target.Completeness` has type `config.Completeness`
  (`internal/config/config.go:115,124-136`); the facade aliases only
  `CompletenessContract` (a different type, `core`-side). Fix:
  `type Completeness = config.Completeness` in `mentat.go`.
- The implementation sweep walks the full reachable set and aliases any further
  gap found; each addition is also a new golden line
  ([surface-golden-v2](./surface-golden-v2.md)) and a literal in the compile test.

## Proof obligation

Extend `mentat_external_test.go` (facade-only imports — precedent at :63-68)
with a compile-level test: one composite literal per reachable exported struct,
each setting at least one field (so a field whose *own type* is un-aliased
internal also breaks the build — spec edge case). The test compiling **is** the
proof; it needs no runtime assertions beyond keeping the values referenced.

`examples/kafkaecho` (separate module, `replace`-directive, facade-only imports)
remains the external-module witness; it MUST keep compiling untouched.

## Verified gap, deferred to spec 010 (recorded 2026-07-18, T018)

The T018 sweep walked the reachable set as defined above (14 members) and found
`Completeness` to be the only gap inside that definition — it is now aliased, and
T018 added no others. The sweep also mechanically extracted the type positions
appearing in golden *field and method* lines and compile-probed them, which
surfaced four types that are frozen on the public surface but **not nameable**:

| Type | Reached via | Consequence for an external author |
|---|---|---|
| `RunReport` | `method (Reporter) Report(rep RunReport, w io.Writer) error` | **`Reporter` cannot be implemented at all** — the method parameter type is unnameable |
| `AggregateDetail` | `field (Verdict) Detail *AggregateDetail` | cannot construct a `Verdict` with `Detail` set |
| `ExtractPolicy` | `field (RunSpec) Extract ExtractPolicy` | cannot build a `RunSpec` with `Extract` set |
| `HTTPSpec` | `field (RunSpec) HTTP HTTPSpec` | cannot build a `RunSpec` with `HTTP` set |

All four hang off **seam** types (`Reporter`, `Verdict`, `RunSpec`), not off
`Config`/`Results`, so they fall outside this contract's reachable-set definition
and outside US3's scope. Aliasing them is a public-surface widening — a one-way
door — so it is **deferred to spec 010** rather than taken here, and the boundary
is stated in `docs/extending/stability.md` (boundary 4) so it is not tribal
knowledge. Spec 010 should consider extending the reachable-set definition to
cover seam-interface parameter and result types.

## Regression guarantee

Any future exported struct field that makes a new internal type reachable forces
either (a) a facade alias + compile-test literal + golden line, or (b) a compile
failure of this test — a reachable-unnameable type can no longer land silently.
