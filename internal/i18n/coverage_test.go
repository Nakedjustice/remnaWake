package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestEveryTCallHasEnglishEntry walks the repo and asserts that every constant
// string passed to i18n.T has an entry in the en dictionary, so new texts
// cannot silently ship untranslated.
func TestEveryTCallHasEnglishEntry(t *testing.T) {
	fset := token.NewFileSet()
	err := filepath.WalkDir("../..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "T" {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "i18n" {
				return true
			}
			if len(call.Args) != 1 {
				return true
			}
			key, ok := constString(call.Args[0])
			if !ok {
				// Named constants (e.g. cabinetButtonLabelRU) are covered by
				// their own usage tests; only literals are checked here.
				return true
			}
			if _, found := en[key]; !found {
				t.Errorf("%s: missing en entry for %q", fset.Position(call.Pos()), key)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

// constString evaluates a constant string expression (literals and their
// concatenations).
func constString(e ast.Expr) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind == token.STRING {
			s, err := strconv.Unquote(v.Value)
			return s, err == nil
		}
	case *ast.BinaryExpr:
		if v.Op == token.ADD {
			l, ok1 := constString(v.X)
			r, ok2 := constString(v.Y)
			return l + r, ok1 && ok2
		}
	case *ast.ParenExpr:
		return constString(v.X)
	}
	return "", false
}
