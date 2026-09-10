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
// addition does not churn the golden. The field type is printed with go/printer and
// then NORMALIZED to the facade's vocabulary (feature 010 T059): a re-exported
// internal type renders under its facade name, so `*core.JudgeUsage` reads
// `*JudgeUsage` no matter which internal package declares the struct. Alias and const
// DECLARATION lines keep their internal target — that is where a rename of the
// underlying type surfaces as drift.
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
		{name: "field type renders under its facade name, not the declaring package's", line: "field (Verdict)[04] Judge *JudgeUsage", exact: true, want: true},
		// The T059 complement: the SAME type reached from a struct in a DIFFERENT
		// internal package must render identically. Before normalization this line
		// read "*core.JudgeUsage" purely because Results moved to internal/result.
		{name: "same type renders identically from another package", line: "field (Results)[08] JudgeTotal *JudgeUsage", exact: true, want: true},
		// Alias DECLARATION lines are deliberately NOT normalized: naming the target
		// is the line's entire purpose, and it is where a rename of the underlying
		// type shows up as drift now that field lines no longer repeat it.
		{name: "alias declaration keeps its internal target", line: "type JudgeUsage = core.JudgeUsage", exact: true, want: true},
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
	// No facadeNames and no dir: normalizeTypes short-circuits on an empty map, so this
	// helper exercises the raw rendering. The normalization path is covered end-to-end by
	// the golden and by TestNormalizeTypesResolvesInDeclaringPackage.
	c := &surfaceCtx{fset: fset}
	out := c.renderStructFields(t, "", "S", st)
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
	// facadeNames maps an alias target keyed by module-relative DIR + type name
	// ("internal/core.JudgeUsage") to the facade name re-exporting it ("JudgeUsage"),
	// for normalizing rendered types (feature 010 T059).
	//
	// Keyed by dir, not by the facade's local import name: the text being normalized was
	// printed from ANOTHER package's AST, and that package has its own import namespace.
	// A package importing something else as `core` would otherwise have its types
	// rewritten to Mentat's facade names — asserting a foreign type is part of this API.
	facadeNames map[string]string
	// dirImports caches each rendered package's OWN local-import-name -> dir map, so a
	// qualifier in text printed from that package resolves in the right namespace.
	dirImports map[string]map[string]string
	// dupAlias collects "one target, two facade names" collisions, reported by the
	// caller so the failure names them all rather than the first.
	dupAlias []string
}

// importsOf returns dir's local package name -> module-relative dir map, parsed once.
// Only module-internal imports are recorded; a qualifier that resolves to nothing here is
// stdlib or third-party and is left exactly as the author wrote it.
func (c *surfaceCtx) importsOf(t *testing.T, dir string) map[string]string {
	t.Helper()
	if m, ok := c.dirImports[dir]; ok {
		return m
	}
	m := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read aliased package dir %q: %v", dir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(c.fset, filepath.Join(dir, name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse aliased source %s: %v", filepath.Join(dir, name), err)
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			var d string
			switch {
			case path == c.modPath:
				d = "."
			case strings.HasPrefix(path, c.modPath+"/"):
				d = strings.TrimPrefix(path, c.modPath+"/")
			default:
				continue
			}
			local := path[strings.LastIndex(path, "/")+1:]
			if imp.Name != nil {
				local = imp.Name.Name
			}
			// Go imports are FILE-scoped, so two files in one package may legally bind
			// the same local name to different packages. This map is dir-scoped, which
			// is right for every package in this module today but would silently
			// mis-resolve such a collision — and a normalizer that silently resolves to
			// the wrong package is the failure mode this whole feature is about. Detect
			// it and fail loudly rather than pick a winner; if it ever fires, resolve
			// per-file with the concrete case in hand.
			if prior, dup := m[local]; dup && prior != d {
				t.Fatalf("package %q binds the import name %q to two different packages "+
					"(%q in one file, %q in another). Imports are file-scoped, but the "+
					"surface renderer resolves qualifiers per DIRECTORY, so it cannot tell "+
					"these apart. Resolve per-file before proceeding.", dir, local, prior, d)
			}
			m[local] = d
		}
	}
	c.dirImports[dir] = m
	return m
}

// collectFacadeNames records every `type X = pkg.Y` on the facade whose target is a
// module-internal package, so rendered types can be normalized to the name a CALLER
// writes rather than the name the declaring package happens to use.
func (c *surfaceCtx) collectFacadeNames(f *ast.File) {
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || !ts.Name.IsExported() || !ts.Assign.IsValid() {
				continue
			}
			sel, ok := ts.Type.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				continue
			}
			dir, local := c.imports[pkg.Name]
			if !local {
				continue
			}
			key := dir + "." + sel.Sel.Name
			// Two facade names for one target would make a field's rendering depend on
			// declaration order, contradicting the documented property that moving a
			// symbol between mentat.go and run.go is deliberately not a diff. Fail loudly
			// rather than pick one.
			if prior, dup := c.facadeNames[key]; dup && prior != ts.Name.Name {
				c.dupAlias = append(c.dupAlias, key+" is aliased as both "+prior+" and "+ts.Name.Name)
			}
			c.facadeNames[key] = ts.Name.Name
		}
	}
}

// surfaceInternalQualified matches a module-internal package qualifier on a type name.
var surfaceInternalQualified = regexp.MustCompile(`\b([a-z][a-z0-9]*)\.([A-Z][A-Za-z0-9_]*)\b`)

// normalizeTypes rewrites internal-package-qualified type names in a rendered line to the
// FACADE name that re-exports them: `*core.JudgeUsage` becomes `*JudgeUsage` (T059).
//
// The golden is a record of the PUBLIC surface, so it should speak the caller's
// vocabulary. Method signatures already did — `method (Correlator) Resolve(…, store
// TraceStore, …)`, never `core.TraceStore` — because those types are declared in the same
// package as the interface and so were written bare. Field types did not, because whether
// a qualifier appears depended on which internal package happened to declare the STRUCT,
// not on the type itself. That produced two renderings of one type
// (`field (Verdict)[04] Judge *JudgeUsage` vs `field (Results)[08] JudgeTotal
// *core.JudgeUsage`) and, worse, made MOVING a type between internal packages churn the
// golden in a way that reads like an API change. Feature 010 hit exactly that when Results
// moved to internal/result.
//
// Stdlib qualifiers (time.Duration, *regexp.Regexp, *trace.Trace where unaliased) are left
// alone — they are not the facade's to rename, and a caller writes them exactly so.
// Alias and const DECLARATION lines are not passed through here: `type Comparator =
// core.Comparator` must keep its target, since naming it is the line's entire purpose.
func (c *surfaceCtx) normalizeTypes(t *testing.T, dir, s string) string {
	if len(c.facadeNames) == 0 {
		return s
	}
	imports := c.importsOf(t, dir)
	return surfaceInternalQualified.ReplaceAllStringFunc(s, func(m string) string {
		qual, typ, ok := strings.Cut(m, ".")
		if !ok {
			return m
		}
		// Resolve the qualifier in the package the text was PRINTED FROM, not in the
		// facade's namespace. A qualifier that names nothing module-internal there is
		// stdlib and stays exactly as written.
		qdir, local := imports[qual]
		if !local {
			return m
		}
		if name, ok := c.facadeNames[qdir+"."+typ]; ok {
			return name
		}
		return m
	})
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
		fset:        token.NewFileSet(),
		modPath:     surfaceModulePath(t),
		imports:     map[string]string{},
		ifaces:      map[string]map[string]*ast.InterfaceType{},
		structs:     map[string]map[string]*ast.StructType{},
		typeSpecs:   map[string][]*ast.TypeSpec{},
		facadeNames: map[string]string{},
		dirImports:  map[string]map[string]string{},
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
	// Collect the facade's alias targets before rendering, so a type can be normalized
	// to the name a CALLER writes regardless of which file declared the alias (T059).
	for _, f := range files {
		c.collectFacadeNames(f)
	}
	if len(c.dupAlias) > 0 {
		sort.Strings(c.dupAlias)
		t.Fatalf("the facade aliases one internal target under two names, so a field's "+
			"rendering would depend on declaration order:\n%s", surfaceIndent(c.dupAlias))
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
							out = append(out, c.renderInterfaceMethods(t, c.imports[pkg.Name], s.Name.Name, iface)...)
						}
						// Symmetrically, a re-exported STRUCT alias renders its EXPORTED FIELD
						// SET (feature 009 F1): the alias line alone is blind to a field being
						// added, removed or re-typed — which is how Verdict.Qualifiers and
						// Target.Completeness reached users with zero golden diff.
						if st := c.lookupStruct(t, pkg.Name, sel.Sel.Name); st != nil {
							out = append(out, c.renderStructFields(t, c.imports[pkg.Name], s.Name.Name, st)...)
							// …and its EXPORTED METHOD SET (feature 010 F1). A method on a
							// LOCALLY-declared type is caught by the FuncDecl branch above, but
							// once that type becomes an alias its methods live in the aliased
							// package and that branch never sees them. Feature 010 hit this
							// exactly: moving Results to internal/result silently dropped
							// `func (r Results) ExitCode() int` — the documented 130/1/0
							// exit-status contract — from the golden with the gate still green.
							out = append(out, c.renderStructMethods(t, pkg.Name, s.Name.Name, sel.Sel.Name)...)
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
	return c.ifaceInDir(t, dir, name)
}

// ifaceInDir is the dir-keyed form, for the sweep. See structInDir.
func (c *surfaceCtx) ifaceInDir(t *testing.T, dir, name string) *ast.InterfaceType {
	t.Helper()
	if dir == "" {
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
	return c.structInDir(t, dir, name)
}

// structInDir is the dir-keyed form. The nameability sweep works in canonical
// module-relative dirs rather than a package's local import name, so it uses this.
func (c *surfaceCtx) structInDir(t *testing.T, dir, name string) *ast.StructType {
	t.Helper()
	if dir == "" {
		return nil
	}
	if c.structs[dir] == nil {
		c.structs[dir] = c.indexStructs(t, dir)
	}
	return c.structs[dir][name]
}

// typeParamsInDir returns the names of dir.name's generic type PARAMETERS — {"T"} for
// `type Box[T any] struct{ Item T }`.
//
// A type parameter is a placeholder the caller supplies, not a type anyone can name,
// so it must never be reported as unnameable. Without this filter the generic
// traversal reports `internal/core.T — reached by field (Box) Item` alongside the real
// offenders, and a gate that emits noise is a gate that gets suppressed.
func (c *surfaceCtx) typeParamsInDir(t *testing.T, dir, name string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	if dir == "" {
		return out
	}
	for _, ts := range c.exportedTypeSpecs(t, dir) {
		if ts.Name.Name != name || ts.TypeParams == nil {
			continue
		}
		for _, f := range ts.TypeParams.List {
			for _, n := range f.Names {
				out[n.Name] = true
			}
		}
	}
	return out
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

// renderStructMethods renders the EXPORTED methods declared on a re-exported struct's
// target type, alias-named so the line reads as the caller writes it:
//
//	method (Results) ExitCode() int
//
// This is the method-side complement of renderStructFields. Without it a struct's
// behaviour contract is frozen only while the type is declared IN the facade: aliasing
// it moves the methods into another package, where the FuncDecl branch of renderGenDecl's
// caller never looks, and every method line silently leaves the golden.
//
// Rules mirror the field side: exported methods only, receiver type matched by name
// (pointer or value receiver alike, since both are reachable through the alias),
// bodies stripped, emitted in declaration order.
//
// Mutation rehearsal (010, observed 2026-09-09) — same discipline as the 009 field-side
// rehearsal. Appending a probe method to internal/result:
//
//	func (r Results) XProbe() bool { return r.Failed == 0 }
//
// gives:
//
//	--- FAIL: TestPublicSurfaceGolden
//	      symbols present now but NOT in golden (added/changed):
//	            - method (Results) XProbe() bool
//
// and removing it returns the gate to green. Before this function existed the same
// experiment produced no diff at all.
func (c *surfaceCtx) renderStructMethods(t *testing.T, pkg, alias, target string) []string {
	t.Helper()
	dir, ok := c.imports[pkg]
	if !ok {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read aliased package dir %q: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(c.fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse aliased source %s: %v", filepath.Join(dir, name), err)
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || len(fd.Recv.List) == 0 || !fd.Name.IsExported() {
				continue
			}
			if surfaceReceiverTypeName(fd.Recv.List[0].Type) != target {
				continue
			}
			// Reuse the interface-method line shape so a method reads identically
			// whether it arrived via an interface alias or a struct alias.
			stripped := *fd
			stripped.Body = nil
			stripped.Recv = nil
			stripped.Doc = nil
			sig := strings.TrimPrefix(surfacePrint(t, c.fset, &stripped), "func "+fd.Name.Name)
			out = append(out, c.normalizeTypes(t, dir, "method ("+alias+") "+fd.Name.Name+sig))
		}
	}
	return out
}

// structMethodSigs returns the exported methods declared on `target` in dir, as
// (name, signature) pairs. Shared by renderStructMethods (which prints them) and
// TestFacadeNameabilitySweep (which walks their types) so the set the golden FREEZES and
// the set the sweep CHECKS cannot diverge — the two halves disagreeing is the defect
// class this feature exists to close.
func (c *surfaceCtx) structMethodSigs(t *testing.T, dir, target string) []struct {
	Name       string
	Sig        *ast.FuncType
	RecvParams map[string]bool
} {
	t.Helper()
	var out []struct {
		Name       string
		Sig        *ast.FuncType
		RecvParams map[string]bool
	}
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
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || len(fd.Recv.List) == 0 || !fd.Name.IsExported() {
				continue
			}
			if surfaceReceiverTypeName(fd.Recv.List[0].Type) != target {
				continue
			}
			out = append(out, struct {
				Name       string
				Sig        *ast.FuncType
				RecvParams map[string]bool
			}{
				Name:       fd.Name.Name,
				Sig:        fd.Type,
				RecvParams: surfaceReceiverTypeParams(fd.Recv.List[0].Type),
			})
		}
	}
	return out
}

// surfaceReceiverTypeName reduces a receiver expression to its bare type name, so a
// value receiver (Results) and a pointer receiver (*Results) both match the aliased
// target — a caller reaches both through the alias, so both are public surface.
// A GENERIC receiver (Box[T], *Pair[K, V]) is unwrapped to its base name too. Without
// that it returned "", so methods on a generic type matched no target and were never
// collected — their parameter and result types went unswept entirely.
func surfaceReceiverTypeName(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	switch v := expr.(type) {
	case *ast.IndexExpr:
		expr = v.X
	case *ast.IndexListExpr:
		expr = v.X
	}
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// surfaceReceiverTypeParams returns the type-parameter names a METHOD's receiver
// declares — {"x"} for `func (b Box[x]) …`, even when the declaration reads
// `type Box[t any]`.
//
// Go lets a receiver rename its type parameters, and those names are scoped to the
// method. So filtering a method's references by the DECLARATION's parameter names is
// wrong in both directions: it can drop a reference to a real package-level type whose
// name happens to match a declaration parameter, and it can fail to drop the receiver's
// actual placeholder. Each method is filtered by its own receiver's names.
func surfaceReceiverTypeParams(expr ast.Expr) map[string]bool {
	out := map[string]bool{}
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	var idx []ast.Expr
	switch v := expr.(type) {
	case *ast.IndexExpr:
		idx = []ast.Expr{v.Index}
	case *ast.IndexListExpr:
		idx = v.Indices
	}
	for _, e := range idx {
		if id, ok := e.(*ast.Ident); ok {
			out[id.Name] = true
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
// printed by go/printer and then normalized to its facade name (T059), so a rename of
// the underlying type surfaces on the ALIAS line rather than here — field lines no
// longer repeat the internal name; embedded fields are rendered as written (the
// embedded type, when public, is frozen by its own entry); fields are emitted in
// declaration order.
// Struct TAGS are deliberately not rendered — the contract freezes name + type.
func (c *surfaceCtx) renderStructFields(t *testing.T, dir, alias string, st *ast.StructType) []string {
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
			out = append(out, c.normalizeTypes(t, dir, ordinal()+typ))
			continue
		}
		for _, n := range field.Names {
			if !n.IsExported() {
				continue
			}
			out = append(out, c.normalizeTypes(t, dir, ordinal()+n.Name+" "+typ))
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
// The receiver is the FACADE alias name (Correlator), not core.Correlator. Parameter
// and result types are printed by go/printer and then normalized to their facade names
// (T059) — most were already bare, being same-package references, but four qualified
// ones (trace.Trace x3, core.ExtractPolicy) were not, and now render as Trace and
// ExtractPolicy alongside the fields.
func (c *surfaceCtx) renderInterfaceMethods(t *testing.T, dir, alias string, iface *ast.InterfaceType) []string {
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
			out = append(out, c.normalizeTypes(t, dir, "method ("+alias+") "+surfacePrint(t, c.fset, field.Type)))
			continue
		}
		// go/printer renders a bare FuncType as "func(params) results"; strip the
		// leading "func" and splice in "method (Alias) Name" so the line reads as the
		// method signature receiver-named by the facade alias.
		sig := strings.TrimPrefix(surfacePrint(t, c.fset, ft), "func")
		for _, mname := range field.Names {
			out = append(out, c.normalizeTypes(t, dir, "method ("+alias+") "+mname.Name+sig))
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

// --- Feature 010 US4: mechanical nameability sweep ---------------------------

// TestFacadeNameabilitySweep is the root-cause fix for stability boundary 4.
//
// Feature 009 verified nameability BY HAND (its T018 sweep) and froze the result only in
// the hand-written composite literals of mentat_external_test.go. That is a gate a human
// has to remember to run, and it walked outward from Config and Results only — never
// through seam signatures. Four types were consequently frozen on the public surface while
// being unwritable from outside the module, and Reporter was unimplementable because of it.
//
// This test replaces the human. It walks the reachable set mechanically and fails on any
// member the facade cannot name, reporting BOTH the offending type and the position that
// reaches it — a message naming only the type would send the author source-spelunking.
//
// Reachable set (contracts/facade-nameability-v2.md):
//
//  1. data reachability   — exported fields, transitively, from every facade struct alias
//  2. seam reachability   — every parameter and result type of every facade interface
//     alias's method set, transitively by rule 1
//
// Terminals are types with no local source: stdlib (io.Writer, context.Context, time.Time,
// *regexp.Regexp) is nameable by its own import and is not the facade's to re-export.
// c.imports holds only module-internal packages, which is exactly that distinction.
//
// # Mutation rehearsal (010 T046, observed 2026-09-09)
//
// The rehearsal is recorded at length because the FIRST attempt at it found a bug in this
// very test. A probe type reachable only through a seam signature was added:
//
//	type ResolveRequest struct { …; Probe ProbeSpec }
//	type ProbeSpec struct{ Note string }   // no facade alias
//
// and the sweep reported ZERO offenders. The cause: `Probe ProbeSpec` inside
// internal/core is a bare *ast.Ident, not a `core.ProbeSpec` SelectorExpr, and the walker
// collected only SelectorExprs — so every same-package hop was invisible. That blind spot
// would have missed the original 010 gaps too (`Verdict.Detail *AggregateDetail` is bare
// in core.go). surfaceNamedRefs now qualifies bare exported identifiers with the package
// they were declared in.
//
// With that fixed, the same probe gives:
//
//	--- FAIL: TestFacadeNameabilitySweep
//	    1 type(s) are reachable from the public surface but have NO facade name…
//	        - core.ProbeSpec — reached by field (ResolveRequest) Probe
//
// naming the offending type AND the position that reaches it, and reverting returns the
// sweep to green. ResolveRequest is reachable only through Correlator.Resolve — never from
// Config or Results — so this is precisely the class the 009 hand-sweep could not see.
//
// Two earlier rehearsal attempts are worth recording as negative results: deleting the
// `RunSpec` alias, and adding a method to a seam interface, both fail to COMPILE before
// this test can run — existing tests reference mentat.RunSpec, and the real correlator
// stops satisfying an extended interface. The compile-level witness in
// mentat_external_test.go is doing real work; this sweep covers what it cannot, namely a
// type nobody happened to write a literal for.
//
// # Mutation rehearsal (011 T006, observed 2026-09-10)
//
// ExpectationParser is the first seam added since 010 closed stability boundary 4, so it is
// the first live test that this sweep catches a new unnameable seam type with nobody
// remembering to check. Probe planted in internal/core, WITH the facade alias left in place:
//
//	type XProbeSpec struct{ Note string }   // deliberately no facade alias
//
//	type ExpectationParser interface {
//		ParseExpectation(text string) (Expectation, error)
//		XProbe() XProbeSpec                 // temporary second method
//	}
//
// giving:
//
//	--- FAIL: TestFacadeNameabilitySweep
//	    1 type(s) are reachable from the public surface but have NO facade name…
//	        - internal/core.XProbeSpec — reached by method (ExpectationParser) XProbe
//
// Reverting both edits returns the sweep to green.
//
// # Why the probe, and not "delete the alias"
//
// Removing `type ExpectationParser = core.ExpectationParser` from mentat.go is NOT a
// rehearsal of this gate, and believing otherwise would have recorded a proof that proves
// nothing. The seed loop above (:1191-1198) queues ONLY facade alias targets, so an
// interface with no alias is never queued, never walked, and yields ZERO offenders — it
// leaves the gate rather than tripping it.
//
// That is the gate's stated contract, not a defect: it verifies that everything PUBLISHED is
// nameable. It cannot tell you that you forgot to publish something. mentat_external_test.go
// is what covers that direction, by failing to compile.
//
// # Two blind spots closed (CodeRabbit pass 4 on PR #38; observed 2026-09-10)
//
// Both were found by review rather than by the gate, and both were LATENT — no live instance
// existed on the surface — so each was proven by planting a probe and watching the sweep
// report ZERO offenders before the fix.
//
//  1. GENERICS. surfaceNamedRefs' type switch had no *ast.IndexExpr case, so a field written
//     `XProbe XBox[XProbeSpec]` on the facade-aliased ResolveRequest fell through the switch
//     entirely and contributed nothing. Sweep: PASS, with two unnameable types on the
//     surface. With IndexExpr/IndexListExpr added it reports both XBox and XProbeSpec.
//
//     The first version of that fix ALSO reported `internal/core.T — reached by field (XBox)
//     Item`: the generic type PARAMETER, which is a placeholder no facade could name. A gate
//     that emits noise is a gate that gets suppressed, so typeParamsInDir now filters those.
//
//  2. UNEXPORTED LOCAL TYPES. `case *ast.Ident: if v.IsExported()` skipped them silently, so
//     `XProbe xHiddenSpec` — a type an external module cannot even SPELL — was invisible.
//     Sweep: PASS. Inverting it needs care, because IsExported was doing double duty: it also
//     filtered Go's predeclared identifiers, which are unexported idents too. Collecting
//     those would key every `Name string` field as "internal/core.string". surfacePredeclared
//     is that discriminator.
//
// One assertion in TestSurfaceNamedRefsQualifiesBareIdents was inverted as part of (2). It
// read "unexported ident is not surface", want: nil — which encoded the blind spot rather
// than a contract. Noted explicitly because editing a gate's own assertions is normally the
// wrong move; the justification is that the row asserted the absence of a check, not the
// presence of a guarantee, and the predeclared rows beside it are what bound the change.
//
// # Three more, from CodeRabbit on PR #39 (observed 2026-09-10)
//
// The first fix for (1) walked generic types but left three related holes, all of which
// were also latent and all proven the same way — plant, watch the sweep report ZERO, fix,
// watch it report the offender:
//
//  3. INSTANTIATED ALIASES were dropped whole. surfaceAliases asserted *ast.SelectorExpr on
//     the alias target, so `type XPublicBox = core.XBox[int]` matched nothing and was
//     skipped — not merely unnameable but UNSEEDED, so the entire subtree behind it went
//     unwalked. This is the seed-side twin of (1); fixing only the walk side was half a fix.
//     surfaceAliasBase unwraps it. Type ARGUMENTS are tracked separately, because a caller
//     writes mentat.XPublicBox and never spells the argument, yet lands in fields typed by
//     it — and substitution happens nowhere, so `Item T` never reveals the concrete type.
//
//  4. GENERIC RECEIVERS matched no target. surfaceReceiverTypeName returned "" for
//     `func (b Box[T]) …`, so methods on a generic type were never collected at all and
//     their signatures went unswept.
//
//  5. RECEIVER TYPE PARAMETERS may be RENAMED. Go scopes a receiver's parameter names to
//     the method, so filtering a method's references by the DECLARATION's names is wrong:
//     given `type XGen[xcollide any]` with `func (g XGen[q]) XDo() xcollide`, the result
//     type is the package-level `xcollide`, and the declaration-name filter dropped it.
//     Each method is now filtered by its own receiver's names. Cross-checked: swapping
//     m.RecvParams back to declParams makes that offender disappear while everything else
//     stays green, so the distinction is what catches it.
//
//  6. TYPE ARGUMENTS were matched only as a DIRECT selector, so `Box[*core.Thing]`,
//     `Box[[]core.Thing]` and `Box[core.Pair[string, core.Thing]]` slipped past and never
//     entered the queue. Reduced with surfaceNamedRefs now — the same traversal every
//     other position uses. Any position that unwraps type expressions by hand drifts from
//     the ones that do not; there is one traversal, and (6) is the second time that
//     lesson arrived. Covered by TestSurfaceAliasArgsTraversesNestedArguments.
//
//  7. FACADE-LOCAL arguments were dropped wholesale. (6) discarded every ref that
//     resolved to the facade itself, justified by "a bare ident at the facade names a
//     facade-declared type, which is nameable by definition" — true only for an
//     EXPORTED ident. `core.Box[hidden]`, with an unexported facade-local type, is the
//     strictest gap there is and was silently discarded by the very filter meant to
//     remove noise. Now dropped only when exported, or when it is one of a parameterized
//     alias's own type parameters (`type Public[T any] = core.Box[T]`, legal since Go
//     1.24) — those are placeholders whatever their case.
//
//     Facade refs are reported but never QUEUED: the sentinel is not a directory, and
//     walking it tries to read one. Nothing is lost, since the only facade refs that
//     survive the filter are types an external module can neither spell nor construct.
func TestFacadeNameabilitySweep(t *testing.T) {
	c, aliases := surfaceAliases(t)

	// nameable keys the facade's alias TARGETS as "pkg.Name" — what an internal type must
	// match to be writable from outside.
	// Everything below is keyed on the CANONICAL module-relative dir, never on a
	// package's local import name: an alias target, a field's type and a method
	// signature's type are each written in a different package's namespace, and only the
	// dir means the same thing in all of them.
	nameable := map[string]bool{}
	for _, a := range aliases {
		nameable[c.imports[a.pkg]+"."+a.target] = true
	}

	type reach struct{ typ, via string }
	var queue []reach
	seen := map[string]bool{}

	// Seed with every alias target, and record how each is reached.
	for _, a := range aliases {
		key := c.imports[a.pkg] + "." + a.target
		if seen[key] {
			continue
		}
		seen[key] = true
		queue = append(queue, reach{typ: key, via: "alias " + a.name})
	}

	var offenders []string

	// A generic alias's TYPE ARGUMENTS are reachable without ever being spelled by the
	// caller: `type PublicBox = core.Box[core.Thing]` lets an external module write
	// mentat.PublicBox and land in fields typed core.Thing. So each argument must be
	// nameable in its own right, and is walked like any other reachable type.
	// Substitution is not performed anywhere — the AST of Box says `Item T` — so
	// without this the concrete argument is never visited at all.
	for _, a := range aliases {
		for _, arg := range a.args {
			if !nameable[arg] {
				offenders = append(offenders, fmt.Sprintf("%s — reached by type argument of alias %s", arg, a.name))
			}
			// A facade-local argument is REPORTED but never queued: the sentinel is not
			// a directory, so walking it would try to read one. Nothing is lost —
			// the only facade refs that survive surfaceAliasArgs are unexported types,
			// which an external module can neither spell nor construct.
			if strings.HasPrefix(arg, surfaceAliasFacadeSelf+".") {
				continue
			}
			if !seen[arg] {
				seen[arg] = true
				queue = append(queue, reach{typ: arg, via: "type argument of alias " + a.name})
			}
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		dir, name, ok := surfaceCutDirType(cur.typ)
		if !ok {
			continue
		}
		// Every type in this package's source resolves through ITS OWN imports.
		resolve := func(q string) (string, bool) {
			d, ok := c.importsOf(t, dir)[q]
			return d, ok
		}
		// A struct contributes its exported field types; an interface contributes its
		// method parameter and result types. Everything else is a leaf.
		var refs []reach

		// A generic type's type PARAMETERS are placeholders, not types the facade could
		// name, so they are dropped rather than reported. The filter is applied PER
		// POSITION, not once over the whole batch: a method receiver may rename the
		// declaration's parameters, and those names are scoped to the method — so a
		// method's references are filtered by its own receiver's names, and everything
		// else by the declaration's.
		declParams := c.typeParamsInDir(t, dir, name)
		keep := func(ref string, params map[string]bool) bool {
			if len(params) == 0 {
				return true
			}
			d, n, cut := surfaceCutDirType(ref)
			if !cut || d != dir {
				return true // another package's type can never be this one's parameter
			}
			return !params[n]
		}
		add := func(names []string, via string, params map[string]bool) {
			for _, ref := range names {
				if keep(ref, params) {
					refs = append(refs, reach{typ: ref, via: via})
				}
			}
		}
		if st := c.structInDir(t, dir, name); st != nil && st.Fields != nil {
			for _, f := range st.Fields.List {
				// EVERY name in a multi-name declaration, filtered individually — the
				// same discipline renderStructFields already uses. Testing only
				// f.Names[0] means `private, Public HiddenType` is skipped whole: the
				// renderer would still freeze Public in the golden while the sweep never
				// checked HiddenType, so the two halves of the gate would disagree and
				// recreate the frozen-but-unwritable asymmetry this feature exists to
				// remove. No such declaration exists today; the gate is for tomorrow's.
				var vias []string
				for _, n := range f.Names {
					if n.IsExported() {
						vias = append(vias, "field ("+name+") "+n.Name)
					}
				}
				if len(f.Names) == 0 {
					vias = append(vias, "field ("+name+") <embedded>")
				}
				for _, via := range vias {
					add(surfaceNamedRefs(f.Type, dir, resolve), via, declParams)
				}
			}
			// …and the struct's exported METHOD signatures. renderStructMethods puts
			// these on the frozen surface (method (Results) ExitCode() int,
			// method (ExtractConfig) Policy() ExtractPolicy, …), so a parameter or
			// result type there is exactly as public as a field's — and was exactly as
			// unswept until now. This gap was introduced by this feature: adding
			// renderStructMethods created a surface position the sweep did not walk.
			for _, m := range c.structMethodSigs(t, dir, name) {
				// m.RecvParams, not declParams: the receiver may rename them.
				add(surfaceFuncRefs(m.Sig, dir, resolve), "method ("+name+") "+m.Name, m.RecvParams)
			}
		}
		if iface := c.ifaceInDir(t, dir, name); iface != nil && iface.Methods != nil {
			for _, m := range iface.Methods.List {
				if fn, isFunc := m.Type.(*ast.FuncType); isFunc && len(m.Names) > 0 {
					// An interface method has no receiver, so the declaration's own type
					// parameters are the ones in scope.
					add(surfaceFuncRefs(fn, dir, resolve), "method ("+name+") "+m.Names[0].Name, declParams)
					continue
				}
				// An EMBEDDED interface contributes its methods to the published seam's
				// method set, so the types in those signatures are just as reachable as
				// the ones declared inline. Skipping non-func fields made anything
				// reachable only through an embed invisible. Queued as a reachable type
				// so the BFS traverses it with the same rules — which also handles an
				// embed of an embed without recursing here. Embeds of stdlib interfaces
				// (io.Writer) resolve to no local package and terminate naturally.
				add(surfaceNamedRefs(m.Type, dir, resolve), "embedded interface in "+name, declParams)
			}
		}
		// Type parameters were already dropped per position by add(), which is where the
		// receiver-vs-declaration distinction lives.
		for _, r := range refs {
			// surfaceNamedRefs only emits module-internal refs now — a qualifier that
			// resolved to nothing local was dropped at the source, so there is no
			// stdlib filtering left to do here.
			if !nameable[r.typ] {
				offenders = append(offenders, fmt.Sprintf("%s — reached by %s", r.typ, r.via))
			}
			if !seen[r.typ] {
				seen[r.typ] = true
				queue = append(queue, r)
			}
		}
	}

	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Fatalf("%d type(s) are reachable from the public surface but have NO facade name, "+
			"so an external module cannot write them:\n%s\n"+
			"Each is shown as <module-relative-dir>.<Type>. Fix by adding an alias to "+
			"mentat.go with a justification — e.g. a type in internal/core becomes "+
			"`type X = core.X` — or by removing the position that reaches it from the "+
			"surface. See contracts/facade-nameability-v2.md.",
			len(offenders), surfaceIndent(offenders))
	}
}

// surfaceAlias is one `type Name = pkg.Target` declaration on the facade.
//
// args carries the canonical "dir.Type" keys of a generic instantiation's TYPE
// ARGUMENTS — `type PublicBox = core.Box[core.Thing]` yields target "Box" and args
// ["internal/core.Thing"]. They are tracked separately because they are reachable
// without being spelled: a caller writes mentat.PublicBox and never names Thing, yet
// lands in fields typed Thing, so an argument the facade cannot name is a gap. Empty
// for the ordinary non-generic alias.
type surfaceAlias struct {
	name, pkg, target string
	args              []string
}

// surfaceAliasFacadeSelf marks a bare identifier written at the FACADE. A bare
// exported ident there names a facade-declared type, which is nameable by construction,
// so those refs are dropped rather than reported.
const surfaceAliasFacadeSelf = "<facade>"

// surfaceAliasArgs reduces a generic alias's type ARGUMENTS to canonical "dir.Type"
// keys, dropping anything nameable by construction (predeclared, facade-declared).
//
// It delegates to surfaceNamedRefs rather than matching *ast.SelectorExpr directly. An
// earlier version did the latter and silently skipped every argument that was not a
// bare selector — `Box[*core.Thing]`, `Box[[]core.Thing]`, `Box[core.Pair[string,
// core.Thing]]` — so those types never entered the reachability queue at all. Any
// position that unwraps type expressions by hand drifts from every other position that
// does; there is one traversal, and this is it.
func surfaceAliasArgs(targs []ast.Expr, aliasParams map[string]bool, resolve func(string) (string, bool)) []string {
	var args []string
	for _, a := range targs {
		for _, ref := range surfaceNamedRefs(a, surfaceAliasFacadeSelf, resolve) {
			name, isFacade := strings.CutPrefix(ref, surfaceAliasFacadeSelf+".")
			if !isFacade {
				args = append(args, ref)
				continue
			}
			// A bare ident written at the facade is dropped in exactly two cases, and
			// an earlier version dropped ALL of them on reasoning that only holds for
			// the first:
			//
			//   - EXPORTED: it names a facade-declared type, and declaring it there is
			//     what makes it nameable.
			//   - the alias declaration's OWN type parameter: a parameterized alias
			//     (`type Public[T any] = core.Box[T]`, legal since Go 1.24) passes its
			//     placeholders through as arguments, whatever their case.
			//
			// Anything else is an UNEXPORTED facade-local type an external module
			// cannot spell — the strictest gap there is, and precisely what the blanket
			// drop was hiding.
			if aliasParams[name] || token.IsExported(name) {
				continue
			}
			args = append(args, ref)
		}
	}
	return args
}

// surfaceAliasBase unwraps a generic instantiation to the alias's base target and its
// type arguments: `core.Box[int]` → (core.Box, [int]); `core.Pair[K, V]` → (core.Pair,
// [K, V]); a plain `core.Thing` → (core.Thing, nil).
//
// Without this an instantiated alias fell through the *ast.SelectorExpr assertion and
// was dropped WHOLE — not merely unnameable but unseeded, so everything reachable
// through it went unwalked and the sweep reported zero offenders.
func surfaceAliasBase(e ast.Expr) (*ast.SelectorExpr, []ast.Expr) {
	switch v := e.(type) {
	case *ast.SelectorExpr:
		return v, nil
	case *ast.IndexExpr:
		sel, _ := surfaceAliasBase(v.X)
		return sel, []ast.Expr{v.Index}
	case *ast.IndexListExpr:
		sel, _ := surfaceAliasBase(v.X)
		return sel, v.Indices
	}
	return nil, nil
}

// surfaceAliases parses the facade package and returns its context plus every type-alias
// declaration whose target is a module-internal package.
func surfaceAliases(t *testing.T) (*surfaceCtx, []surfaceAlias) {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	c := &surfaceCtx{
		fset:        token.NewFileSet(),
		modPath:     surfaceModulePath(t),
		imports:     map[string]string{},
		ifaces:      map[string]map[string]*ast.InterfaceType{},
		structs:     map[string]map[string]*ast.StructType{},
		typeSpecs:   map[string][]*ast.TypeSpec{},
		facadeNames: map[string]string{},
		dirImports:  map[string]map[string]string{},
	}
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(c.fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		c.addImports(f)
		files = append(files, f)
	}
	var out []surfaceAlias
	for _, f := range files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() || !ts.Assign.IsValid() {
					continue
				}
				sel, targs := surfaceAliasBase(ts.Type)
				if sel == nil {
					continue
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok {
					continue
				}
				if _, local := c.imports[pkg.Name]; !local {
					continue
				}
				aliasParams := map[string]bool{}
				if ts.TypeParams != nil {
					for _, f := range ts.TypeParams.List {
						for _, n := range f.Names {
							aliasParams[n.Name] = true
						}
					}
				}
				args := surfaceAliasArgs(targs, aliasParams, func(q string) (string, bool) {
					d, ok := c.imports[q]
					return d, ok
				})
				out = append(out, surfaceAlias{name: ts.Name.Name, pkg: pkg.Name, target: sel.Sel.Name, args: args})
			}
		}
	}
	return c, out
}

// surfaceNamedRefs reduces a type expression to the named types it references, qualified
// as "pkg.Type", unwrapping pointers, slices, arrays, maps, channels and func types.
//
// self is the package the expression was DECLARED in, and it is load-bearing: a type
// written `*AggregateDetail` inside internal/core is a bare *ast.Ident, not a
// SelectorExpr, because it is a same-package reference. An earlier version of this
// function collected only SelectorExprs on the theory that bare identifiers were
// "covered by that package's own walk" — there is no such walk, so every same-package
// hop was invisible and the sweep reported zero offenders while being blind to the exact
// gaps feature 010 exists to close. The mutation rehearsal below is what caught it.
//
// Unexported identifiers are NOT skipped: a local type an external module cannot even
// spell is the strictest nameability failure there is, so it is collected and reported.
// Go's predeclared types are unexported idents too — string, int, error, any — and those
// ARE skipped, via surfacePredeclared. Conflating the two classes under IsExported was
// the blind spot; separating them is what makes reporting the first class safe.
// surfacePredeclared is Go's set of predeclared type and constant identifiers. They
// parse as unexported *ast.Idents but are universally nameable, so they terminate the
// walk rather than being reported as unnameable local types.
var surfacePredeclared = map[string]bool{
	"bool": true, "byte": true, "complex64": true, "complex128": true,
	"error": true, "float32": true, "float64": true, "int": true,
	"int8": true, "int16": true, "int32": true, "int64": true,
	"rune": true, "string": true, "uint": true, "uint8": true,
	"uint16": true, "uint32": true, "uint64": true, "uintptr": true,
	"any": true, "comparable": true,
	"true": true, "false": true, "iota": true, "nil": true,
}

func surfaceNamedRefs(expr ast.Expr, self string, resolve func(string) (string, bool)) []string {
	var out []string
	var walk func(ast.Expr)
	walk = func(e ast.Expr) {
		switch v := e.(type) {
		case *ast.StarExpr:
			walk(v.X)
		case *ast.ArrayType:
			walk(v.Elt)
		case *ast.Ellipsis:
			walk(v.Elt)
		case *ast.MapType:
			walk(v.Key)
			walk(v.Value)
		case *ast.ChanType:
			walk(v.Value)
		case *ast.IndexExpr:
			// A generic instantiation with ONE type argument: Box[Payload]. Both the
			// generic type and its argument are reachable and both must be nameable —
			// an external module writing this field needs to spell both.
			walk(v.X)
			walk(v.Index)
		case *ast.IndexListExpr:
			// Two or more type arguments: Pair[K, V].
			walk(v.X)
			for _, idx := range v.Indices {
				walk(idx)
			}
		case *ast.SelectorExpr:
			id, ok := v.X.(*ast.Ident)
			if !ok {
				return
			}
			// Resolve the qualifier in the namespace of the package this expression was
			// WRITTEN IN. `core.X` inside internal/result means whatever internal/result
			// imports as `core` — not whatever the facade does. Anything that resolves
			// to nothing module-internal is stdlib and terminates here.
			if dir, local := resolve(id.Name); local {
				out = append(out, dir+"."+v.Sel.Name)
			}
		case *ast.Ident:
			// An UNEXPORTED local type in a published position is the strictest
			// nameability failure there is: an external module cannot even spell it, so
			// it can never construct the surrounding value. Reporting it requires care,
			// because IsExported() was doing double duty here — it also filtered out
			// Go's PREDECLARED identifiers (string, int, error, any, …), which are
			// unexported idents too. Collecting those would key every `Name string`
			// field as "internal/core.string" and bury the gate in noise.
			if v.IsExported() || !surfacePredeclared[v.Name] {
				out = append(out, self+"."+v.Name)
			}
		case *ast.FuncType:
			out = append(out, surfaceFuncRefs(v, self, resolve)...)
		}
	}
	walk(expr)
	return out
}

// surfaceFuncRefs returns the named types in a function signature's parameters AND
// results. This is the half feature 009's sweep never walked — and the half that left
// Reporter unimplementable from outside the module.
func surfaceFuncRefs(fn *ast.FuncType, self string, resolve func(string) (string, bool)) []string {
	var out []string
	if fn.Params != nil {
		for _, p := range fn.Params.List {
			out = append(out, surfaceNamedRefs(p.Type, self, resolve)...)
		}
	}
	if fn.Results != nil {
		for _, r := range fn.Results.List {
			out = append(out, surfaceNamedRefs(r.Type, self, resolve)...)
		}
	}
	return out
}

// --- T059 / gate-audit follow-ups: unit-test the normalization primitives ----------

// TestSurfaceNamedRefsQualifiesBareIdents pins the canonical key contract: every
// reference comes back as "module-relative-dir.TypeName", with bare identifiers taking
// the DECLARING package's dir and qualifiers resolved through that package's OWN import
// table. Local import names are never a key, because `core` means whatever the package
// doing the writing says it means.
//
// It is also the regression test for the bug the 010
// nameability rehearsal caught: the first version of surfaceNamedRefs collected only
// qualified selectors, so a same-package reference — which is how internal/core writes
// `Detail *AggregateDetail` — was invisible, and the sweep reported zero offenders while
// blind. That bug was found by a mutation rehearsal; this pins it so the next one is
// found by a test.
func TestSurfaceNamedRefsQualifiesBareIdents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		expr string // a type expression as written inside package "core"
		want []string
	}{
		{name: "bare exported ident takes its own package's dir", expr: "AggregateDetail", want: []string{"internal/core.AggregateDetail"}},
		{name: "pointer to bare ident", expr: "*AggregateDetail", want: []string{"internal/core.AggregateDetail"}},
		{name: "slice of bare ident", expr: "[]RunRecord", want: []string{"internal/core.RunRecord"}},
		{name: "selector resolves through the DECLARING package's imports", expr: "*trace.Trace", want: []string{"internal/trace.Trace"}},
		{name: "a qualifier the declaring package does not import is stdlib and is dropped", expr: "time.Duration", want: nil},
		{name: "predeclared types are not references", expr: "string", want: nil},
		{name: "predeclared any is not a reference", expr: "any", want: nil},
		// This row previously asserted want: nil — "an unexported ident is not
		// surface". That encoded the blind spot rather than a contract: an unexported
		// local type in a published position is the STRICTEST nameability failure
		// there is, because an external module cannot even spell it. The row is
		// inverted deliberately, and the two predeclared rows above are what keep the
		// change from over-reaching (IsExported was filtering both classes at once).
		{name: "unexported local ident IS an unnameable reference", expr: "compiled", want: []string{"internal/core.compiled"}},
		// Generic instantiations: both the generic type and its type ARGUMENTS are
		// reachable, and an external module must be able to spell each. The type
		// PARAMETER of a generic declaration is filtered separately, at the sweep, by
		// typeParamsInDir — it is a placeholder, not a type.
		{name: "generic instantiation contributes type and argument", expr: "Box[Payload]", want: []string{"internal/core.Box", "internal/core.Payload"}},
		{name: "multi-argument generic contributes all arguments", expr: "Pair[Kind, Detail]", want: []string{"internal/core.Pair", "internal/core.Kind", "internal/core.Detail"}},
		{name: "map contributes key and value", expr: "map[Kind]Detail", want: []string{"internal/core.Kind", "internal/core.Detail"}},
		{name: "func type contributes params and results", expr: "func(Evidence) (Verdict, error)", want: []string{"internal/core.Evidence", "internal/core.Verdict"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			expr, err := parser.ParseExpr(tt.expr)
			if err != nil {
				t.Fatalf("parse %q: %v", tt.expr, err)
			}
			// Stands in for internal/core's own import table.
			resolve := func(q string) (string, bool) {
				d, ok := map[string]string{"trace": "internal/trace"}[q]
				return d, ok
			}
			got := surfaceNamedRefs(expr, "internal/core", resolve)
			if len(got) != len(tt.want) {
				t.Fatalf("surfaceNamedRefs(%q) = %v, want %v", tt.expr, got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("surfaceNamedRefs(%q)[%d] = %q, want %q", tt.expr, i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestNormalizeTypesResolvesInDeclaringPackage pins the property the gate audit flagged:
// a qualifier in rendered text must be resolved in the namespace of the package the text
// was PRINTED FROM, never the facade's. Keying on the facade's local import names would
// let any package that imports something else as `core` have its types silently rewritten
// to Mentat's facade names — the golden would then assert a foreign type is part of this
// API.
func TestNormalizeTypesResolvesInDeclaringPackage(t *testing.T) {
	t.Parallel()

	c := &surfaceCtx{
		facadeNames: map[string]string{"internal/core.JudgeUsage": "JudgeUsage"},
		dirImports: map[string]map[string]string{
			// The real case: internal/result imports internal/core as "core".
			"internal/result": {"core": "internal/core"},
			// The hazard: a package that binds the SAME local name to something else.
			"internal/decoy": {"core": "internal/somewhere-else"},
			// And one that does not import it at all.
			"internal/lonely": {},
		},
	}
	tests := []struct {
		name string
		dir  string
		in   string
		want string
	}{
		{name: "resolves to the facade name in the real package", dir: "internal/result", in: "field (Results)[08] JudgeTotal *core.JudgeUsage", want: "field (Results)[08] JudgeTotal *JudgeUsage"},
		{name: "same local name bound elsewhere is left alone", dir: "internal/decoy", in: "field (X)[00] Bogus *core.JudgeUsage", want: "field (X)[00] Bogus *core.JudgeUsage"},
		{name: "unimported qualifier is left alone", dir: "internal/lonely", in: "field (X)[00] Y *core.JudgeUsage", want: "field (X)[00] Y *core.JudgeUsage"},
		{name: "stdlib qualifiers are never touched", dir: "internal/result", in: "field (Results)[06] Duration time.Duration", want: "field (Results)[06] Duration time.Duration"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := c.normalizeTypes(t, tt.dir, tt.in); got != tt.want {
				t.Errorf("normalizeTypes(%q, %q)\n got  %q\n want %q", tt.dir, tt.in, got, tt.want)
			}
		})
	}
}

// surfaceCutDirType splits a canonical "some/dir.TypeName" key. The dir may itself
// contain dots in principle, so the split is on the LAST dot, not the first.
func surfaceCutDirType(key string) (dir, name string, ok bool) {
	i := strings.LastIndex(key, ".")
	if i < 0 {
		return "", "", false
	}
	return key[:i], key[i+1:], true
}

// TestSurfaceAliasArgsTraversesNestedArguments pins the fix for the second CodeRabbit
// finding on PR #39: a generic alias's type ARGUMENTS were matched only as a direct
// *ast.SelectorExpr, so `core.Box[*core.Thing]`, `core.Box[[]core.Thing]` and a nested
// instantiation all slipped past and never entered the reachability queue.
//
// Arguments are reduced with the same traversal every other position uses, so pointers,
// slices, maps, channels, func types and nested generics unwrap identically. Anything
// nameable by construction — predeclared types, and bare idents naming a
// facade-DECLARED type — is dropped rather than reported.
func TestSurfaceAliasArgsTraversesNestedArguments(t *testing.T) {
	t.Parallel()

	resolve := func(q string) (string, bool) {
		d, ok := map[string]string{"core": "internal/core", "trace": "internal/trace"}[q]
		return d, ok
	}

	tests := []struct {
		name        string
		expr        string          // the full alias target, e.g. core.Box[...]
		aliasParams map[string]bool // the ALIAS declaration's own type parameters
		want        []string
	}{
		{name: "direct selector", expr: "core.Box[core.Thing]", want: []string{"internal/core.Thing"}},
		{name: "pointer argument", expr: "core.Box[*core.Thing]", want: []string{"internal/core.Thing"}},
		{name: "slice argument", expr: "core.Box[[]core.Thing]", want: []string{"internal/core.Thing"}},
		{name: "map argument contributes key and value", expr: "core.Box[map[core.Kind]core.Thing]", want: []string{"internal/core.Kind", "internal/core.Thing"}},
		{name: "nested instantiation contributes base and inner argument", expr: "core.Box[core.Pair[string, core.Thing]]", want: []string{"internal/core.Pair", "internal/core.Thing"}},
		{name: "multiple arguments", expr: "core.Pair[core.Kind, *core.Thing]", want: []string{"internal/core.Kind", "internal/core.Thing"}},
		{name: "cross-package argument resolves through its own qualifier", expr: "core.Box[*trace.Trace]", want: []string{"internal/trace.Trace"}},
		{name: "predeclared argument is nameable by construction", expr: "core.Box[int]", want: nil},
		{name: "stdlib argument is not the facade's to re-export", expr: "core.Box[time.Duration]", want: nil},
		{name: "non-generic alias has no arguments", expr: "core.Thing", want: nil},
		// A bare EXPORTED ident at the facade names a facade-declared type, which is
		// nameable because declaring it there is what makes it nameable.
		{name: "exported facade argument is nameable by construction", expr: "core.Box[Verdict]", want: nil},
		// An UNEXPORTED one is the opposite: an external module cannot spell it at all,
		// so discarding it hid the strictest kind of gap. The earlier blanket drop of
		// every <facade>.* ref was justified by reasoning that only holds for exported
		// idents.
		{name: "unexported facade argument is NOT nameable", expr: "core.Box[hidden]", want: []string{"<facade>.hidden"}},
		// A parameterized alias (`type Public[T any] = core.Box[T]`, legal since Go
		// 1.24) passes its OWN type parameters through as arguments. Those are
		// placeholders, not types, whatever their case.
		{name: "alias type parameter is not a reference", expr: "core.Box[T]", aliasParams: map[string]bool{"T": true}, want: nil},
		{name: "lowercase alias type parameter is not a reference either", expr: "core.Box[t]", aliasParams: map[string]bool{"t": true}, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e, err := parser.ParseExpr(tt.expr)
			if err != nil {
				t.Fatalf("parse %q: %v", tt.expr, err)
			}
			_, targs := surfaceAliasBase(e)
			got := surfaceAliasArgs(targs, tt.aliasParams, resolve)
			if len(got) != len(tt.want) {
				t.Fatalf("surfaceAliasArgs(%q) = %v, want %v", tt.expr, got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("surfaceAliasArgs(%q)[%d] = %q, want %q", tt.expr, i, got[i], tt.want[i])
				}
			}
		})
	}
}
