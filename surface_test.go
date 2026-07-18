// Package mentat_test — US3 public-surface golden gate (spec 007 FR-005).
//
// This is the one-way-door lock: TestPublicSurfaceGolden renders the exported
// surface of the `mentat` facade package (mentat.go + run.go) into a canonical,
// order-independent form and diffs it against a committed golden. Any drift —
// an added, removed, or changed exported symbol — fails the test naming the
// symbol on the side it appears. Regenerating the golden in the SAME PR
// (MENTAT_UPDATE_GOLDEN=1 go test -run TestPublicSurfaceGolden) is the
// deliberate acknowledgment act (FR-005 / public-surface.md stability policy).
//
// Renderer approach (research R4: STDLIB-ONLY — no golang.org/x/tools):
// go/parser parses every NON-TEST source file in the package dir (mentat.go,
// run.go; *_test.go skipped) with mode 0 (comments are NOT surface). We walk
// the top-level declarations and render each EXPORTED symbol via go/printer:
//   - type specs (aliases + facade structs) → "type <name> <RHS-as-written>";
//   - func decls + exported methods on exported types → the signature with the
//     BODY STRIPPED (FuncDecl.Body = nil), never the implementation;
//   - const specs → "const <name> = <RHS-as-written>".
//
// Re-exported INTERFACE aliases (feature 008 F1). A bare alias line like
// "type Correlator = core.Correlator" is BLIND to the aliased interface's method
// set: a signature change to core.Correlator.Resolve produces zero diff. So for
// every EXPORTED alias whose RHS is a selector `pkg.Name`, we also go/parser-parse
// the aliased package's source straight from disk (import path → module-relative
// dir, via go.mod), and if `type Name interface { … }` is found there we render
// each method as a normalized one-line signature receiver-named by the FACADE
// alias, e.g.
//
//	method (Correlator) Resolve(ctx context.Context, store TraceStore, req ResolveRequest) (*trace.Trace, error)
//
// Re-exported STRUCT aliases (feature 009 F1) are expanded the same way and for
// the same reason: "type Verdict = core.Verdict" is blind to a field being added,
// removed or re-typed, so every EXPORTED field of an aliased struct is rendered as
//
//	field (Verdict)[02] Qualifiers []string
//
// The bracketed, zero-padded ORDINAL is the field's position among the struct's
// surface fields, and it makes DECLARATION ORDER part of the frozen surface:
// permuting two fields is a break for unkeyed composite literals
// (`mentat.Verdict{true, nil, …}`) and for positional reflection, neither of which
// errors at the consumer's call site. Without the ordinal, any permutation rendered
// byte-identically (the whole line set is sorted). Unexported fields are omitted
// (not a public promise) and do NOT consume an ordinal, so a purely internal field
// addition does not churn the golden. The field type is printed as written in the
// aliased source.
// Map, func and `= any` aliases stay alias-line-only — their declaration text
// already IS their complete shape (contracts/surface-golden-v2.md rule 3).
//
// This still honors R4: NO go/types / importer / x/tools — the method and field
// sets are read from the aliased package's AST, not resolved by a type checker.
// Non-module aliases (stdlib) have no local source to parse and so stay
// alias-line-only (none such today).
//
// Every rendered symbol is collapsed to a single whitespace-normalized line, and
// the whole set is sort.Strings'd, so the order symbols are DECLARED IN / SPREAD
// ACROSS SOURCE FILES never churns the golden (determinism): moving a func between
// mentat.go and run.go is not surface. That sort is why struct fields carry an
// explicit ordinal — for fields, order IS surface, so it has to survive the sort
// rather than be erased by it. Unexported identifiers (runOptions, driverReg,
// toResults, …) are skipped.
//
// MUTATION REHEARSAL (T014, performed once, reverted):
// With the golden present and green, a scratch file `zz_surface_mutation.go`
// (package mentat) declaring `func WithNothing() {}` was added. Running this test
// went RED with:
//
//	symbols present now but NOT in golden (added/changed):
//	    - func WithNothing()
//
// naming exactly the injected symbol, then the scratch file was deleted and the
// test returned GREEN. This proves the gate catches real surface drift (not only
// a missing golden file); the scratch symbol is NOT left behind.
//
// MUTATION REHEARSAL (feature 008 T028, method-set drift, performed once, reverted):
// With the method-set-aware golden present and green, the receiver-independent
// parameter name in core.Correlator.Resolve was renamed `ctx`→`c` in
// internal/core/core.go (an interface param-name change satisfies no implementer,
// so the whole module still compiles — the gate parses source, it does not need a
// green build of the mutated interface's implementers). Running this test went RED
// with:
//
//	symbols present now but NOT in golden (added/changed):
//	    - method (Correlator) Resolve(c context.Context, store TraceStore, req ResolveRequest) (*trace.Trace, error)
//	symbols in golden but NOT present now (removed/changed):
//	    - method (Correlator) Resolve(ctx context.Context, store TraceStore, req ResolveRequest) (*trace.Trace, error)
//
// naming exactly the changed method on both sides, then core.go was reverted and
// the test returned GREEN. This proves the gate now bites method-set changes on
// re-exported interfaces — the 008 Correlator.Resolve signature change would no
// longer slip through with zero golden diff.
//
// MUTATION REHEARSAL (feature 009 T006, struct-FIELD drift, 2026-07-18, performed
// once, reverted): the golden was blind to struct-alias fields too — a bare
// "type Verdict = core.Verdict" line cannot see a field appear, which is precisely
// how Verdict.Qualifiers and Target.Completeness reached the public surface with
// zero golden diff. With the field-set-aware golden present and green, an exported
// field `XProbe bool` was added to core.Verdict in internal/core/core.go. Running
// this test went RED with:
//
//	public surface drifted from golden "specs/007-public-extension-api/contracts/public-surface.golden".
//	  symbols present now but NOT in golden (added/changed):
//	        - field (Verdict) XProbe bool
//	  symbols in golden but NOT present now (removed/changed):
//	        (none)
//
// naming exactly the drifted type (Verdict) and the injected field, then core.go
// was reverted (byte-identical, `git diff` empty) and the test returned GREEN.
// This proves the gate now bites field-set changes on re-exported structs.
// (That rehearsal predates the field ordinal, so its transcript above shows the
// pre-ordinal line form; the drift it demonstrates is unaffected.)
//
// MUTATION REHEARSAL (field ORDER drift, 2026-07-18, performed once, reverted):
// raised in review of the 009 field expansion — the gate froze the field SET but
// not its ORDER, so permuting fields was a silent break. With the ordinal-bearing
// golden present and green, `Pass` and `Reasons` were swapped in core.Verdict
// (internal/core/core.go) — a pure permutation: no field added, removed or
// re-typed, and the module still compiles because every construction site is
// keyed. Running this test went RED with:
//
//	symbols present now but NOT in golden (added/changed):
//	    - field (Verdict)[00] Reasons []string
//	    - field (Verdict)[01] Pass bool
//	symbols in golden but NOT present now (removed/changed):
//	    - field (Verdict)[00] Pass bool
//	    - field (Verdict)[01] Reasons []string
//
// naming both fields on both sides with their swapped positions, then core.go was
// reverted (byte-identical, `git diff` empty) and the test returned GREEN. The
// ordinal-introducing golden regen was itself proved to be pure re-annotation:
// 105 field lines before and after, and stripping the ordinals reproduced the
// previous set exactly, with no non-field line touched.
package mentat_test

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// surfaceGoldenPath is the committed canonical rendering of the public surface.
// It is a FIXED relative path (go test runs with cwd == package dir) so the gate
// is byte-stable across machines.
const surfaceGoldenPath = "specs/007-public-extension-api/contracts/public-surface.golden"

// surfaceWSRun collapses any run of whitespace (spaces, tabs, newlines that
// go/printer emits between struct fields / across a signature) into a single
// space, so each rendered symbol is one deterministic line.
var surfaceWSRun = regexp.MustCompile(`\s+`)

// TestPublicSurfaceGolden is the FR-005 surface gate: render the facade's
// exported surface and diff it against the golden, failing (and naming the
// drifted symbols) on any mismatch. Serial by convention (reads source files
// from disk; shares no mutable state but does no parallel-worth I/O).
func TestPublicSurfaceGolden(t *testing.T) {
	lines := surfaceRender(t)
	got := strings.Join(lines, "\n") + "\n"

	// Regeneration is the deliberate acknowledgment act (FR-005). Reuses the
	// MENTAT_UPDATE_GOLDEN convention shared with the hermetic stdout golden.
	if os.Getenv(goldenUpdateEnv) == "1" {
		if err := os.WriteFile(surfaceGoldenPath, []byte(got), 0o644); err != nil {
			t.Fatalf("update surface golden %q: %v", surfaceGoldenPath, err)
		}
		t.Logf("wrote surface golden %q (%d exported symbols)", surfaceGoldenPath, len(lines))
		return
	}

	wantBytes, err := os.ReadFile(surfaceGoldenPath)
	if err != nil {
		t.Fatalf("read surface golden %q (regenerate the golden in this PR only if the surface change was intended, e.g. %s=1 go test -run TestPublicSurfaceGolden): %v", surfaceGoldenPath, goldenUpdateEnv, err)
	}
	if got == string(wantBytes) {
		return
	}

	// Drift: compute the set difference both ways so the message NAMES exactly the
	// symbols that appear on only one side (a changed signature shows on both).
	wantLines := surfaceSplit(string(wantBytes))
	gotSet := surfaceSet(lines)
	wantSet := surfaceSet(wantLines)
	var added, removed []string
	for _, l := range lines {
		if _, ok := wantSet[l]; !ok {
			added = append(added, l)
		}
	}
	for _, l := range wantLines {
		if _, ok := gotSet[l]; !ok {
			removed = append(removed, l)
		}
	}
	t.Fatalf("public surface drifted from golden %q.\n"+
		"  symbols present now but NOT in golden (added/changed):\n%s\n"+
		"  symbols in golden but NOT present now (removed/changed):\n%s\n"+
		"Regenerate the golden in this PR only if the surface change was intended "+
		"(e.g. %s=1 go test -run TestPublicSurfaceGolden).",
		surfaceGoldenPath, surfaceIndent(added), surfaceIndent(removed), goldenUpdateEnv)
}

// TestSurfaceRenderStructFields is the feature 009 US1 renderer contract
// (contracts/surface-golden-v2.md rendering rules 2 and 3): a re-exported STRUCT
// alias must expand to its exported field set, so an added/removed/re-typed field
// of `core.Verdict` or `config.Target` is drift the gate can see. A bare alias
// line ("type Verdict = core.Verdict") is blind to it — exactly the blindness that
// let Verdict.Qualifiers and Target.Completeness land with zero golden diff.
//
// The rows are the contract's three obligations plus their violation cases:
// expansion happens (Verdict/Target/ExtractConfig exported fields), unexported
// fields are NOT surface (ExtractConfig.compiled), and non-struct aliases stay
// single-line (the map alias Pricing, the interface alias Correlator).
func TestSurfaceRenderStructFields(t *testing.T) {
	t.Parallel()
	lines := surfaceRender(t)

	tests := []struct {
		name  string
		line  string // exact rendered line when exact, else a substring of one
		exact bool
		want  bool // whether the rendering must contain such a line
	}{
		{name: "struct alias expands the drifted Verdict.Qualifiers field", line: "field (Verdict)[02] Qualifiers []string", exact: true, want: true},
		{name: "struct alias expands the drifted Target.Completeness field", line: "field (Target)[07] Completeness Completeness", exact: true, want: true},
		{name: "field type is rendered as written in the aliased source", line: "field (Verdict)[04] Judge *JudgeUsage", exact: true, want: true},
		{name: "struct alias expands ExtractConfig exported fields", line: "field (ExtractConfig)[00] Mode string", exact: true, want: true},
		// Substring, not prefix: the ordinal sits between the alias and the field
		// name, so a prefix of "field (ExtractConfig) compiled" could never match
		// any line and would pass vacuously whether or not `compiled` is rendered.
		{name: "unexported field is not surface", line: "] compiled ", exact: false, want: false},
		{name: "map alias stays single-line", line: "field (Pricing)", exact: false, want: false},
		{name: "interface alias renders methods, not fields", line: "field (Correlator)", exact: false, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got bool
			for _, l := range lines {
				if (tt.exact && l == tt.line) || (!tt.exact && strings.Contains(l, tt.line)) {
					got = true
					break
				}
			}
			if got != tt.want {
				kind := "substring"
				if tt.exact {
					kind = "line"
				}
				t.Fatalf("rendered surface: %s %q present = %v, want %v", kind, tt.line, got, tt.want)
			}
		})
	}
}

// surfaceRenderStructSrc renders the fields of a single `type S struct { … }`
// literal given as source text, using the same renderer the golden uses. It exists
// so field-ORDER behaviour can be pinned against synthetic structs without moving
// fields around in real facade types.
func surfaceRenderStructSrc(t *testing.T, fields string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "synthetic.go", "package p\n\ntype S struct {\n"+fields+"\n}\n", 0)
	if err != nil {
		t.Fatalf("parsing synthetic struct: %v", err)
	}
	var st *ast.StructType
	ast.Inspect(f, func(n ast.Node) bool {
		if s, ok := n.(*ast.StructType); ok && st == nil {
			st = s
		}
		return st == nil
	})
	if st == nil {
		t.Fatalf("no struct literal parsed from:\n%s", fields)
	}
	c := &surfaceCtx{fset: fset}
	out := c.renderStructFields(t, "S", st)
	sort.Strings(out) // mirror surfaceRender's global sort — the golden is sorted
	return out
}

// TestSurfaceRenderStructFieldOrder pins DECLARATION ORDER of a re-exported
// struct's exported fields as part of the frozen surface.
//
// Why order is surface, not an internal detail: an external module may write an
// UNKEYED composite literal of a re-exported struct (`mentat.Verdict{true, nil,
// …}`), and reflection (`reflect.Type.Field(i)`) indexes fields positionally.
// Permuting two same-typed fields therefore silently changes the meaning of
// already-compiled consumer code — a break with no compiler error at the call
// site. Before this test, renderStructFields emitted no ordinal and
// surfaceRender sorted the whole line set, so ANY permutation produced a
// byte-identical golden. Found in review of the feature-009 field expansion.
func TestSurfaceRenderStructFieldOrder(t *testing.T) {
	t.Parallel()

	t.Run("permuting two exported fields changes the rendering", func(t *testing.T) {
		t.Parallel()
		before := surfaceRenderStructSrc(t, "\tAlpha string\n\tBeta string")
		after := surfaceRenderStructSrc(t, "\tBeta string\n\tAlpha string")
		if strings.Join(before, "\n") == strings.Join(after, "\n") {
			t.Fatalf("permuting exported fields produced an identical rendering; order is not frozen:\n%s",
				strings.Join(before, "\n"))
		}
	})

	t.Run("ordinal sorts numerically past nine fields", func(t *testing.T) {
		t.Parallel()
		// Names descend (Fl, Fk, … Fa) while positions ascend, so sorting by NAME
		// yields the exact reverse of declaration order. This subtest can only
		// pass if a zero-padded positional ordinal drives the sort — an unpadded
		// ordinal additionally misplaces [10] and [11] before [2].
		var fields []string
		want := make([]string, 0, 12)
		for i := 0; i < 12; i++ {
			name := "F" + string(rune('l'-i))
			fields = append(fields, "\t"+name+" int")
			want = append(want, name)
		}
		got := surfaceRenderStructSrc(t, strings.Join(fields, "\n"))
		if len(got) != len(want) {
			t.Fatalf("rendered %d field lines, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
		}
		// The lines are SORTED; if the ordinal is zero-padded, sorted order and
		// declaration order coincide. An unpadded ordinal puts [10] before [2].
		for i, name := range want {
			if !strings.HasSuffix(got[i], " "+name+" int") {
				t.Fatalf("sorted line %d = %q, want the field declared at position %d (%s)\nall:\n%s",
					i, got[i], i, name, strings.Join(got, "\n"))
			}
		}
	})
}

// surfaceCtx carries the cross-file state the renderer needs to resolve a
// re-exported interface alias (e.g. `type Correlator = core.Correlator`) into its
// METHOD SET. R4 stays honored — STDLIB ONLY, no go/types / importer / x/tools:
// the aliased package (internal/core, …) is go/parser-parsed straight from disk
// and its `type X interface { … }` declarations are indexed by name.
type surfaceCtx struct {
	fset    *token.FileSet
	modPath string
	// imports maps a facade-file import's local package name (e.g. "core") to the
	// module-relative source dir (e.g. "internal/core"). Non-module imports (stdlib)
	// are absent — they have no local source to parse, so their aliases stay
	// alias-line-only (there is no such interface alias today).
	imports map[string]string
	// ifaces caches, per parsed aliased dir, its exported interface type literals by
	// name. A dir is parsed lazily on first lookup.
	ifaces map[string]map[string]*ast.InterfaceType
	// structs caches, per parsed aliased dir, its exported struct type literals by
	// name (feature 009 F1 — struct-alias field expansion). Same lazy-parse shape as
	// ifaces.
	structs map[string]map[string]*ast.StructType
	// typeSpecs caches the exported top-level type specs of each aliased dir, so the
	// dir is read and parsed once for both indexers.
	typeSpecs map[string][]*ast.TypeSpec
}

// surfaceRender parses every non-test source file in the package dir and returns
// the sorted, canonical single-line rendering of every exported symbol (facade
// symbols plus the method set of every re-exported interface alias).
func surfaceRender(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	c := &surfaceCtx{
		fset:      token.NewFileSet(),
		modPath:   surfaceModulePath(t),
		imports:   map[string]string{},
		ifaces:    map[string]map[string]*ast.InterfaceType{},
		structs:   map[string]map[string]*ast.StructType{},
		typeSpecs: map[string][]*ast.TypeSpec{},
	}
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		// Mode 0: no ParseComments — doc/inline comments are NOT part of the surface.
		f, err := parser.ParseFile(c.fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		c.addImports(f)
		files = append(files, f)
	}
	// Render only after every facade import is recorded, so an alias in run.go
	// resolves against imports declared in any facade file.
	var lines []string
	for _, f := range files {
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				lines = append(lines, surfaceRenderFunc(t, c.fset, d)...)
			case *ast.GenDecl:
				lines = append(lines, c.renderGenDecl(t, d)...)
			}
		}
	}
	sort.Strings(lines)
	return lines
}

// surfaceModulePath reads the module path from go.mod (cwd == package dir == module
// root under `go test`), so an import path can be mapped to its on-disk source dir
// without go/build or x/tools (R4).
func surfaceModulePath(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	t.Fatalf("no module line in go.mod")
	return ""
}

// addImports records every module-local import of a facade file as local-name→dir
// (the local name is the import alias when present, else the path's last element).
func (c *surfaceCtx) addImports(f *ast.File) {
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		var dir string
		switch {
		case path == c.modPath:
			dir = "."
		case strings.HasPrefix(path, c.modPath+"/"):
			dir = strings.TrimPrefix(path, c.modPath+"/")
		default:
			continue // stdlib / third-party: no local source to render
		}
		name := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		c.imports[name] = dir
	}
}

// renderGenDecl renders the exported type/const specs of a GenDecl, handling
// grouped decls (a `type ( … )` block) spec-by-spec so unexported members are
// dropped individually. A re-exported interface alias additionally renders its
// method set (feature 008 F1).
func (c *surfaceCtx) renderGenDecl(t *testing.T, d *ast.GenDecl) []string {
	t.Helper()
	var out []string
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			if !s.Name.IsExported() {
				continue
			}
			out = append(out, "type "+surfacePrint(t, c.fset, s))
			// A re-exported interface alias (`type X = pkg.Iface`) additionally renders
			// its METHOD SET, so a signature change to a re-exported interface method is
			// caught as drift — the alias line alone is blind to it (feature 008 F1).
			if s.Assign.IsValid() {
				if sel, ok := s.Type.(*ast.SelectorExpr); ok {
					if pkg, ok := sel.X.(*ast.Ident); ok {
						if iface := c.lookupInterface(t, pkg.Name, sel.Sel.Name); iface != nil {
							out = append(out, c.renderInterfaceMethods(t, s.Name.Name, iface)...)
						}
						// Symmetrically, a re-exported STRUCT alias renders its EXPORTED FIELD
						// SET (feature 009 F1): the alias line alone is blind to a field being
						// added, removed or re-typed — which is how Verdict.Qualifiers and
						// Target.Completeness reached users with zero golden diff.
						if st := c.lookupStruct(t, pkg.Name, sel.Sel.Name); st != nil {
							out = append(out, c.renderStructFields(t, s.Name.Name, st)...)
						}
					}
				}
			}
		case *ast.ValueSpec:
			for i, n := range s.Names {
				if !n.IsExported() {
					continue
				}
				line := d.Tok.String() + " " + n.Name
				if s.Type != nil {
					line += " " + surfacePrint(t, c.fset, s.Type)
				}
				if i < len(s.Values) {
					line += " = " + surfacePrint(t, c.fset, s.Values[i])
				}
				out = append(out, line)
			}
		}
	}
	return out
}

// lookupInterface resolves a re-exported alias target `pkg.name` to its interface
// type literal in the aliased package's source, or nil when pkg is non-local or
// name is not an interface (a struct/map/`= any` alias stays alias-line-only).
func (c *surfaceCtx) lookupInterface(t *testing.T, pkg, name string) *ast.InterfaceType {
	t.Helper()
	dir, ok := c.imports[pkg]
	if !ok {
		return nil
	}
	if c.ifaces[dir] == nil {
		c.ifaces[dir] = c.indexInterfaces(t, dir)
	}
	return c.ifaces[dir][name]
}

// indexInterfaces indexes dir's exported `type X interface { … }` declarations by name.
func (c *surfaceCtx) indexInterfaces(t *testing.T, dir string) map[string]*ast.InterfaceType {
	t.Helper()
	out := map[string]*ast.InterfaceType{}
	for _, ts := range c.exportedTypeSpecs(t, dir) {
		if iface, ok := ts.Type.(*ast.InterfaceType); ok {
			out[ts.Name.Name] = iface
		}
	}
	return out
}

// exportedTypeSpecs parses every non-test .go file in an aliased package dir and
// returns its exported top-level type specs, cached per dir so a dir is walked
// once no matter how many alias kinds (interface, struct) index it. Both indexers
// share this walk so they can never drift apart on which files count as source.
func (c *surfaceCtx) exportedTypeSpecs(t *testing.T, dir string) []*ast.TypeSpec {
	t.Helper()
	if cached, ok := c.typeSpecs[dir]; ok {
		return cached
	}
	var out []*ast.TypeSpec
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read aliased package dir %q: %v", dir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(c.fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse aliased source %s: %v", filepath.Join(dir, name), err)
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() {
					continue
				}
				out = append(out, ts)
			}
		}
	}
	c.typeSpecs[dir] = out
	return out
}

// lookupStruct resolves a re-exported alias target `pkg.name` to its struct type
// literal in the aliased package's source, or nil when pkg is non-local or name is
// not a struct (an interface/map/func/`= any` alias stays alias-line-only — rule 3
// of contracts/surface-golden-v2.md).
func (c *surfaceCtx) lookupStruct(t *testing.T, pkg, name string) *ast.StructType {
	t.Helper()
	dir, ok := c.imports[pkg]
	if !ok {
		return nil
	}
	if c.structs[dir] == nil {
		c.structs[dir] = c.indexStructs(t, dir)
	}
	return c.structs[dir][name]
}

// indexStructs indexes dir's exported `type X struct { … }` declarations by name.
func (c *surfaceCtx) indexStructs(t *testing.T, dir string) map[string]*ast.StructType {
	t.Helper()
	out := map[string]*ast.StructType{}
	for _, ts := range c.exportedTypeSpecs(t, dir) {
		if st, ok := ts.Type.(*ast.StructType); ok {
			out[ts.Name.Name] = st
		}
	}
	return out
}

// renderStructFields renders each EXPORTED field of a re-exported struct as a
// normalized, alias-named one-line declaration:
//
//	field (Verdict) Qualifiers []string
//
// so adding, removing or re-typing an exported field is caught as drift, and the
// failure message names the drifted type via the alias in parentheses. Rules
// (contracts/surface-golden-v2.md rule 2): exported fields only (an unexported
// field like config.ExtractConfig.compiled is not a public promise); the type is
// printed exactly as written in the aliased package's source, so a rename of the
// named type is drift; embedded fields are rendered as written (the embedded type,
// when public, is frozen by its own entry); fields are emitted in declaration order.
// Struct TAGS are deliberately not rendered — the contract freezes name + type.
func (c *surfaceCtx) renderStructFields(t *testing.T, alias string, st *ast.StructType) []string {
	t.Helper()
	if st.Fields == nil {
		return nil
	}
	var out []string
	// pos is the field's position AMONG THE RENDERED (surface) fields, not among all
	// fields. Unexported fields are skipped and do not consume a position: they are
	// not a public promise (see the named-field branch), a struct carrying them
	// cannot be composite-literal'd from another package at all, and counting them
	// would churn the golden on purely internal edits. The ordinal is what freezes
	// declaration ORDER — see TestSurfaceRenderStructFieldOrder for why order is
	// surface. It is zero-padded so the global sort.Strings in surfaceRender orders
	// positions numerically ([02] before [10]) and thus keeps each alias's field
	// block in declaration order.
	pos := 0
	ordinal := func() string {
		if pos > 99 {
			t.Fatalf("struct alias %s has %d surface fields; the 2-digit field ordinal "+
				"no longer sorts numerically — widen the padding and regenerate the golden", alias, pos+1)
		}
		s := fmt.Sprintf("field (%s)[%02d] ", alias, pos)
		pos++
		return s
	}
	for _, field := range st.Fields.List {
		typ := surfacePrint(t, c.fset, field.Type)
		if len(field.Names) == 0 {
			// Embedded field: the type IS the field, so render it as written.
			//
			// Deliberately NOT filtered by IsExported, unlike the named-field
			// branch below. An embedded type promotes its exported members into
			// the outer struct's surface even when the embedded type itself is
			// unexported, so the embedding is a public promise either way.
			// Skipping it would under-report drift; rendering it can only
			// over-report, and for a drift gate over-reporting is the safe
			// direction. No unexported embedded type exists on the surface
			// today, so this branch changes no current golden line.
			out = append(out, ordinal()+typ)
			continue
		}
		for _, n := range field.Names {
			if !n.IsExported() {
				continue
			}
			out = append(out, ordinal()+n.Name+" "+typ)
		}
	}
	return out
}

// renderInterfaceMethods renders each method of a re-exported interface as a
// normalized, alias-named one-line signature:
//
//	method (Correlator) Resolve(ctx context.Context, store TraceStore, req ResolveRequest) (*trace.Trace, error)
//
// so a signature change to ANY re-exported interface method is caught as drift.
// The receiver is the FACADE alias name (Correlator), not core.Correlator; the
// parameter/result types are rendered exactly as written in the aliased source.
func (c *surfaceCtx) renderInterfaceMethods(t *testing.T, alias string, iface *ast.InterfaceType) []string {
	t.Helper()
	if iface.Methods == nil {
		return nil
	}
	var out []string
	for _, field := range iface.Methods.List {
		ft, ok := field.Type.(*ast.FuncType)
		if !ok {
			// Embedded interface (Ident / SelectorExpr): render the embedded name so an
			// embedding change is not silently dropped. None of the six re-exported
			// interfaces embed today, but a future embed must still churn the golden.
			out = append(out, "method ("+alias+") "+surfacePrint(t, c.fset, field.Type))
			continue
		}
		// go/printer renders a bare FuncType as "func(params) results"; strip the
		// leading "func" and splice in "method (Alias) Name" so the line reads as the
		// method signature receiver-named by the facade alias.
		sig := strings.TrimPrefix(surfacePrint(t, c.fset, ft), "func")
		for _, mname := range field.Names {
			out = append(out, "method ("+alias+") "+mname.Name+sig)
		}
	}
	return out
}

// surfaceRenderFunc renders an exported func decl or an exported method on an
// exported type as its signature with the body stripped (never the impl).
func surfaceRenderFunc(t *testing.T, fset *token.FileSet, d *ast.FuncDecl) []string {
	t.Helper()
	if !d.Name.IsExported() {
		return nil
	}
	// A method is surface only when its receiver type is also exported.
	if d.Recv != nil && !surfaceExportedReceiver(d.Recv) {
		return nil
	}
	stripped := *d
	stripped.Body = nil // the surface is the signature, never the implementation
	stripped.Doc = nil
	return []string{surfacePrint(t, fset, &stripped)}
}

// surfaceExportedReceiver reports whether a method receiver's base type is exported
// (handles both value and pointer receivers).
func surfaceExportedReceiver(recv *ast.FieldList) bool {
	if recv == nil || len(recv.List) == 0 {
		return false
	}
	typ := recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	id, ok := typ.(*ast.Ident)
	return ok && id.IsExported()
}

// surfacePrint prints an AST node with go/printer and collapses it to one line.
func surfacePrint(t *testing.T, fset *token.FileSet, node ast.Node) string {
	t.Helper()
	var buf bytes.Buffer
	cfg := printer.Config{Mode: printer.UseSpaces, Tabwidth: 8}
	if err := cfg.Fprint(&buf, fset, node); err != nil {
		t.Fatalf("print node: %v", err)
	}
	return strings.TrimSpace(surfaceWSRun.ReplaceAllString(buf.String(), " "))
}

// surfaceSplit splits a golden file into its non-empty symbol lines.
func surfaceSplit(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// surfaceSet builds a membership set from a slice of lines.
func surfaceSet(lines []string) map[string]struct{} {
	m := make(map[string]struct{}, len(lines))
	for _, l := range lines {
		m[l] = struct{}{}
	}
	return m
}

// surfaceIndent formats a symbol list one-per-line for the failure message.
func surfaceIndent(syms []string) string {
	if len(syms) == 0 {
		return "        (none)"
	}
	var b strings.Builder
	for i, s := range syms {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("        - " + s)
	}
	return b.String()
}
