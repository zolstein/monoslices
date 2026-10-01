package benchmarks

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestGeneratedSourcesUseStaticSpecFuncs(t *testing.T) {
	filename := filepath.Join(sourceDirectory(t), "monoslices_gen.go")
	file, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, name := range []string{
		"record008Predicate", "record008Equal", "record008Compare", "record008SearchCompare",
		"record016Predicate", "record016Equal", "record016Compare", "record016SearchCompare",
		"record016PredicateByref", "record016EqualByref", "record016CompareByref", "record016SearchCompareByref",
		"record064PredicateByref", "record064EqualByref", "record064CompareByref", "record064SearchCompareByref",
		"record256PredicateByref", "record256EqualByref", "record256CompareByref", "record256SearchCompareByref",
	} {
		want[name] = false
	}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		if ok {
			if _, exists := want[ident.Name]; exists {
				want[ident.Name] = true
			}
		}
		return true
	})
	for name, found := range want {
		if !found {
			t.Errorf("generated source never calls %s directly", name)
		}
	}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			ast.Inspect(fn.Type, func(node ast.Node) bool {
				if _, ok := node.(*ast.FuncType); ok && node != fn.Type {
					t.Errorf("%s has function-typed parameter", fn.Name.Name)
				}
				return true
			})
		}
	}
	source, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(source), "// monoslices:"); got != 10 {
		t.Fatalf("provenance blocks = %d, want 10", got)
	}
}

func TestBenchmarkVariantNamesAreParallel(t *testing.T) {
	for _, prefix := range []string{
		"Index/record-008-value/size-4096/miss",
		"Compare/record-016-byref/size-4096/equal",
		"BinarySearch/record-064-byref-record-key/size-4096/varied",
		"SortStable/record-256-byref/duplicates/size-1024",
	} {
		if got, want := benchmarkVariantNames(prefix), [2]string{prefix + "/impl=generated", prefix + "/impl=stdlib"}; got != want {
			t.Errorf("variants for %s = %v, want %v", prefix, got, want)
		}
	}
}
func TestBenchmarkInputsAreDeterministic(t *testing.T) {
	for _, pattern := range []int{patternRandom, patternSorted, patternReverse, patternDuplicates} {
		if !slices.Equal(recordPattern(257, pattern, makeRecord008), recordPattern(257, pattern, makeRecord008)) {
			t.Errorf("8-byte pattern %d changed", pattern)
		}
		if !slices.Equal(recordPattern(257, pattern, makeRecord256), recordPattern(257, pattern, makeRecord256)) {
			t.Errorf("256-byte pattern %d changed", pattern)
		}
	}
	queries := variedSearchTargets(257)
	if len(queries) != variedSearchQueryCount || !slices.Equal(queries, variedSearchTargets(257)) {
		t.Fatal("varied queries changed")
	}
	var found, below, inside, above int
	for _, q := range queries {
		switch {
		case q.Key < 0:
			below++
		case q.Key >= 514:
			above++
		case q.Key&1 != 0:
			inside++
		default:
			found++
		}
	}
	perClass := variedSearchQueryCount / 4
	if found != perClass || below != perClass || inside != perClass || above != perClass {
		t.Errorf("query classes = %d %d %d %d, want %d each", found, below, inside, above, perClass)
	}
}

func sourceDirectory(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(filename)
}
