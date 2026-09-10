package steps

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestDocstringHandlersGuardNil is a mechanical gate: every step handler taking a
// *godog.DocString must reject a nil docstring BEFORE touching it.
//
// It exists because the convention was followed eight times out of nine and the ninth
// shipped a panic. `the response body json-contains:` dereferenced doc.Content
// directly, so a step written without a docstring crashed the run — through ten
// features, past every review — while its siblings returned a descriptive error.
// Library code may not panic except on caller-unreachable invariants, and a malformed
// feature file is reachable by definition.
//
// A per-handler test catches the handler it was written for. This catches the NEXT one,
// which is the only version of this check worth having: godog binds a docstring
// parameter automatically, so the failure mode is silent until someone writes a
// malformed feature file, and nothing about writing a new handler prompts the author to
// remember the guard.
//
// # What it enforces
//
// Not "the guard is the first statement" — that would be brittle, and a handler may
// legitimately do unrelated preamble first. The rule is the one that actually matters:
// the FIRST statement mentioning the docstring parameter must be its nil check. That
// forbids any dereference reaching the parameter before the guard, which is exactly the
// defect, while leaving handler structure free.
//
// # Mutation rehearsals (observed 2026-09-10)
//
// Two, because the check has two halves and one mutation would only prove one.
//
// A. DELETE the guard from responseBodyMatchesSchema — the sibling of the handler this
// gate was written for:
//
//	responseBodyMatchesSchema (steps.go:550): first use of docstring parameter "doc" is
//	not a nil guard. A step written without a docstring will PANIC here.
//
// B. Keep the nil check but let it FALL THROUGH (`if doc == nil { _ = doc }`). Same
// failure, which is the point: a nil check that does not stop is not a guard, it is a
// comment that compiles. Without the return requirement this mutation would pass.
//
// Restoring returns the gate to green in both cases. A gate not observed failing has
// not been tested.
func TestDocstringHandlersGuardNil(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	var checked []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			param, found := docstringParamName(fd)
			if !found {
				continue
			}
			where := name + ":" + strconv.Itoa(fset.Position(fd.Pos()).Line)
			checked = append(checked, fd.Name.Name)

			if param == "_" {
				// A handler that ignores its docstring cannot dereference it; nothing to
				// guard. Recorded rather than silently skipped.
				continue
			}
			if !guardsNilFirst(fd.Body.List, param) {
				t.Errorf("%s (%s): first use of docstring parameter %q is not a nil guard. "+
					"A step written without a docstring will PANIC here. Open with:\n"+
					"\tif %s == nil {\n\t\treturn fmt.Errorf(\"<the step phrase>: expected a docstring ..., got none\")\n\t}",
					fd.Name.Name, where, param, param)
			}
		}
	}

	// A gate that silently matches nothing is worse than no gate: it reports success
	// forever if the detection stops working (a signature change, a moved file). Pin
	// the floor at the family size this was written against.
	if len(checked) < 9 {
		sort.Strings(checked)
		t.Fatalf("found only %d docstring handler(s) %v; expected at least 9. "+
			"Detection has likely broken rather than the handlers having disappeared.",
			len(checked), checked)
	}
}

// docstringParamName returns the name of fd's *godog.DocString parameter. Handlers take
// at most one; the capture arguments that precede it are plain strings.
func docstringParamName(fd *ast.FuncDecl) (string, bool) {
	if fd.Type.Params == nil {
		return "", false
	}
	for _, p := range fd.Type.Params.List {
		star, ok := p.Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		sel, ok := star.X.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "DocString" {
			continue
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "godog" {
			continue
		}
		if len(p.Names) == 0 {
			return "_", true // unnamed parameter: unusable, therefore unguardable
		}
		return p.Names[0].Name, true
	}
	return "", false
}

// guardsNilFirst reports whether the first statement mentioning param is an if that
// compares it to nil and returns.
func guardsNilFirst(body []ast.Stmt, param string) bool {
	for _, stmt := range body {
		if !mentions(stmt, param) {
			continue
		}
		return isNilGuard(stmt, param)
	}
	// The parameter is never used at all, so it can never be dereferenced.
	return true
}

// isNilGuard reports whether stmt is `if param == nil { … return … }`.
func isNilGuard(stmt ast.Stmt, param string) bool {
	ifs, ok := stmt.(*ast.IfStmt)
	if !ok || ifs.Init != nil {
		return false
	}
	bin, ok := ifs.Cond.(*ast.BinaryExpr)
	if !ok || bin.Op != token.EQL {
		return false
	}
	lhs, ok := bin.X.(*ast.Ident)
	if !ok || lhs.Name != param {
		return false
	}
	if rhs, ok := bin.Y.(*ast.Ident); !ok || rhs.Name != "nil" {
		return false
	}
	// The guard must actually stop: a nil check whose body falls through to the
	// dereference below it is not a guard.
	for _, s := range ifs.Body.List {
		if _, isReturn := s.(*ast.ReturnStmt); isReturn {
			return true
		}
	}
	return false
}

// mentions reports whether name appears as an identifier anywhere in stmt.
func mentions(stmt ast.Stmt, name string) bool {
	found := false
	ast.Inspect(stmt, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			found = true
		}
		return !found
	})
	return found
}
