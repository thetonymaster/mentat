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
// Every rendered symbol is collapsed to a single whitespace-normalized line, and
// the whole set is sort.Strings'd, so source declaration order never churns the
// golden (determinism). Unexported identifiers (runOptions, driverReg, toResults,
// …) are skipped. AST was sufficient — no go/types / importer needed, because the
// RHS-as-written rendering never has to resolve an alias target.
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
package mentat_test

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
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

// surfaceRender parses every non-test source file in the package dir and returns
// the sorted, canonical single-line rendering of every exported symbol.
func surfaceRender(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	var lines []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		// Mode 0: no ParseComments — doc/inline comments are NOT part of the surface.
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				lines = append(lines, surfaceRenderFunc(t, fset, d)...)
			case *ast.GenDecl:
				lines = append(lines, surfaceRenderGenDecl(t, fset, d)...)
			}
		}
	}
	sort.Strings(lines)
	return lines
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

// surfaceRenderGenDecl renders the exported type/const specs of a GenDecl,
// handling grouped decls (a `type ( … )` block) spec-by-spec so unexported
// members are dropped individually.
func surfaceRenderGenDecl(t *testing.T, fset *token.FileSet, d *ast.GenDecl) []string {
	t.Helper()
	var out []string
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			if !s.Name.IsExported() {
				continue
			}
			out = append(out, "type "+surfacePrint(t, fset, s))
		case *ast.ValueSpec:
			for i, n := range s.Names {
				if !n.IsExported() {
					continue
				}
				line := d.Tok.String() + " " + n.Name
				if s.Type != nil {
					line += " " + surfacePrint(t, fset, s.Type)
				}
				if i < len(s.Values) {
					line += " = " + surfacePrint(t, fset, s.Values[i])
				}
				out = append(out, line)
			}
		}
	}
	return out
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
