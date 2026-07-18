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

## Regression guarantee

Any future exported struct field that makes a new internal type reachable forces
either (a) a facade alias + compile-test literal + golden line, or (b) a compile
failure of this test — a reachable-unnameable type can no longer land silently.
