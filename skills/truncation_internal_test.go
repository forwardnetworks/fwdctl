package skills

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Every list a skill cuts must be cut through result.Cap, result.CapRow or result.Page (or window, which uses Page), so the cut is reported in the envelope's Omitted list and
// in limits: a caller comparing two answers must never miss rows because a skill dropped them silently (the v0.5.76 reachability collapse was this). This test type-checks the
// package and fails on any prefix slice of a non-string, e.g. rows[:25], outside the allowlist. A cut that is not a cut of results (a sort key, a scratch buffer) is allowed with its reason.
//
// This replaces an earlier word-matching heuristic: it does not ask whether a nearby comment sounds like an omission, it forbids the unreported cut itself.
var allowedListCuts = map[string]string{
	"route.go": "Route is a ranking: limit is the caller's own top-N, not a cut of a result; the CLI says it shows the best matches",
}

func TestListsAreCutOnlyThroughReportedHelpers(t *testing.T) {
	if testing.Short() {
		t.Skip("type-checks the package")
	}
	fset := token.NewFileSet()
	names, _ := filepath.Glob("*.go")
	var files []*ast.File
	for _, n := range names {
		if strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, n, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil), Error: func(error) {}}
	if _, err := conf.Check("skills", fset, files, info); err != nil && len(info.Types) == 0 {
		t.Fatalf("type check: %v", err)
	}
	var found []string
	for _, f := range files {
		file := filepath.Base(fset.Position(f.Pos()).Filename)
		if _, ok := allowedListCuts[file]; ok {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			se, ok := n.(*ast.SliceExpr)
			if !ok || se.High == nil || se.Low != nil || se.Slice3 {
				return true // not a prefix cut, or the x[:0:0] empty-slice idiom
			}
			if be, isBin := se.High.(*ast.BinaryExpr); isBin && be.Op == token.SUB {
				return true // x[:len(x)-1]: dropping the tail, not capping a result
			}
			tv, ok := info.Types[se.X]
			if !ok {
				return true
			}
			if b, isBasic := tv.Type.Underlying().(*types.Basic); isBasic && b.Info()&types.IsString != 0 {
				return true // text cut, not a list
			}
			if _, isSlice := tv.Type.Underlying().(*types.Slice); !isSlice {
				return true
			}
			found = append(found, fset.Position(se.Pos()).String())
			return true
		})
	}
	sort.Strings(found)
	for _, pos := range found {
		t.Errorf("%s: a list is cut with a bare prefix slice; use result.Cap, result.CapRow or window so the cut is reported (or allowlist the file with a reason)", pos)
	}
}
