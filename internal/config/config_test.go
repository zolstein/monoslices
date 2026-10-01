package config

import (
	"errors"
	"flag"
	"strings"
	"testing"
)

func TestParseCLI(t *testing.T) {
	got, err := ParseCLI(nil)
	if err != nil || got.Out != "" || got.OutSet {
		t.Fatalf("ParseCLI(nil) = %#v, %v", got, err)
	}
	got, err = ParseCLI([]string{"-out=custom_gen.go"})
	if err != nil || got.Out != "custom_gen.go" || !got.OutSet {
		t.Fatalf("ParseCLI(custom) = %#v, %v", got, err)
	}
	for _, args := range [][]string{{"-name=Foos"}, {"-pred=isFoo"}, {"-eq=equalFoo"}, {"-cmp=compareFoo"}, {"-byref=all"}, {"-ops=sort"}, {"-type=int"}, {"./..."}} {
		assertCLIDiagnostic(t, args, "")
	}
	assertCLIDiagnostic(t, []string{"-out=custom.txt"}, "must end in .go")
	assertCLIDiagnostic(t, []string{"-out="}, "must not be empty")
}

func TestValidateOutputBasename(t *testing.T) {
	for _, name := range []string{"_monoslices.go", ".monoslices.go", "monoslices_test.go"} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateOutputBasename(name); err == nil || !strings.Contains(err.Error(), "ordinary production Go source filename") {
				t.Fatalf("ValidateOutputBasename(%q) = %v, want production-source diagnostic", name, err)
			}
		})
	}
	if err := ValidateOutputBasename(DefaultOutputFilename); err != nil {
		t.Fatalf("ValidateOutputBasename(%q) = %v, want valid default basename", DefaultOutputFilename, err)
	}
}

func TestHelp(t *testing.T) {
	if _, err := ParseCLI([]string{"-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("ParseCLI(-h) = %v", err)
	}
	if _, err := ParseCLI([]string{"-help"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("ParseCLI(-help) = %v", err)
	}
	var builder strings.Builder
	PrintUsage(&builder)
	for _, detail := range []string{"Usage: monoslices [-out=<file.go>]", "-out string", "current directory", "//monoslices:generate"} {
		if !strings.Contains(builder.String(), detail) {
			t.Errorf("help lacks %q", detail)
		}
	}
}

func TestOperationRegistryCanonicalOrder(t *testing.T) {
	want := []Operation{OperationContains, OperationIndex, OperationDelete, OperationCompact, OperationEqual, OperationCompare, OperationMin, OperationMax, OperationSort, OperationSortStable, OperationIsSorted, OperationBinarySearch}
	if got := SupportedOperations(); !sameOperations(got, want) {
		t.Fatalf("SupportedOperations() = %v, want %v", got, want)
	}
	if got := SupportedOperationSpecs(); len(got) != len(want) {
		t.Fatalf("registry length=%d", len(got))
	}
}

func TestParseDirectiveFunctionRoles(t *testing.T) {
	for _, test := range []struct {
		raw              string
		wantPred, wantEq string
		wantCmp          string
	}{
		{raw: "name=Foos pred=isFoo", wantPred: "isFoo"},
		{raw: "name=Foos eq=equalFoo", wantEq: "equalFoo"},
		{raw: "name=Foos cmp=compareFoo", wantCmp: "compareFoo"},
	} {
		got, err := ParseDirective(test.raw)
		if err != nil {
			t.Fatal(err)
		}
		if got.Pred != test.wantPred || got.Eq != test.wantEq || got.Cmp != test.wantCmp {
			t.Fatalf("ParseDirective(%q) functions = (%q, %q, %q), want (%q, %q, %q)", test.raw, got.Pred, got.Eq, got.Cmp, test.wantPred, test.wantEq, test.wantCmp)
		}
	}
	got, err := ParseDirective("name=Foos pred=isFoo eq=equalFoo cmp=compareFoo")
	if err != nil || got.Pred != "isFoo" || got.Eq != "equalFoo" || got.Cmp != "compareFoo" {
		t.Fatalf("functions=%#v err=%v", got, err)
	}
}

func TestParseDirectiveRejectsTypeOptionAndQuotedValues(t *testing.T) {
	if got, err := ParseDirective("name=Items cmp=compareItem"); err != nil || got.Name != "Items" || got.Cmp != "compareItem" {
		t.Fatalf("ParseDirective(valid)=%#v err=%v", got, err)
	}
	if _, err := ParseDirective("name=Items cmp=compareItem type=Item"); err == nil || !strings.Contains(err.Error(), `unknown directive key "type"`) {
		t.Fatalf("ParseDirective(type=Item) error=%v, want unknown-key diagnostic", err)
	}
	for _, raw := range []string{
		`name="Items" cmp=compareItem`,
		`name=Items cmp="compareItem"`,
		"name=Items cmp=compareItem byref=\"cmp\"",
	} {
		if _, err := ParseDirective(raw); err == nil {
			t.Fatalf("ParseDirective(%q) succeeded, want quoted-value diagnostic", raw)
		}
	}
}

func TestParseDirectiveByRefAndOperations(t *testing.T) {
	for _, mode := range []ByRefMode{ByRefTrue, ByRefFirst, ByRefSecond} {
		raw := "name=Foos pred=isFoo eq=equalFoo cmp=compareFoo byref=" + string(mode) + " ops=binary-search,sort,compare"
		got, err := ParseDirective(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got.ByRef != mode {
			t.Fatalf("byref=%q, want %q", got.ByRef, mode)
		}
		want := []Operation{OperationCompare, OperationSort, OperationBinarySearch}
		if !sameOperations(got.Ops, want) || !got.OpsSet {
			t.Fatalf("ops=%#v", got)
		}
		wantFormatted := "name=Foos pred=isFoo eq=equalFoo cmp=compareFoo byref=" + string(mode) + " ops=compare,sort,binary-search"
		if FormatDirective(got) != wantFormatted {
			t.Fatalf("formatted=%q, want %q", FormatDirective(got), wantFormatted)
		}
	}
	without, err := ParseDirective("name=Foos cmp=compareFoo")
	if err != nil || without.ByRef != ByRefDisabled {
		t.Fatalf("omitted byref = %q, %v", without.ByRef, err)
	}
	for _, raw := range []string{
		"name=Foos cmp=compareFoo byref", "name=Foos cmp=compareFoo byref=", "name=Foos cmp=compareFoo byref=false", "name=Foos cmp=compareFoo byref=all", "name=Foos cmp=compareFoo byref=cmp", "name=Foos cmp=compareFoo byref=first,second", "name=Foos cmp=compareFoo ops=sort,sort", "name=Foos cmp=compareFoo ops=unknown", "name=Foos cmp=compareFoo out=foo.go",
	} {
		assertDirectiveDiagnostic(t, raw)
	}
}

func TestParseDirectiveRejectsMalformedAssignmentsAndIdentifiers(t *testing.T) {
	for _, raw := range []string{"name =Foos cmp=compareFoo", "name=Foos cmp", "name=Foos cmp=compareFoo name=Other", "name=Foos cmp=compareFoo unknown=value", "pred=isFoo", "name=Foos", "name=not-valid cmp=compareFoo", "name=func cmp=compareFoo", "name=Foos cmp=type", "name=Foos cmp=1compare"} {
		assertDirectiveDiagnostic(t, raw)
	}
}

func assertCLIDiagnostic(t *testing.T, args []string, want string) {
	t.Helper()
	_, err := ParseCLI(args)
	if err == nil || !strings.HasPrefix(err.Error(), "monoslices:") || (want != "" && !strings.Contains(err.Error(), want)) {
		t.Fatalf("ParseCLI(%v) error=%v want=%q", args, err, want)
	}
}

func assertDirectiveDiagnostic(t *testing.T, raw string) {
	t.Helper()
	_, err := ParseDirective(raw)
	if err == nil || !strings.HasPrefix(err.Error(), "monoslices:") {
		t.Fatalf("ParseDirective(%q) error=%v", raw, err)
	}
}

func sameOperations(got, want []Operation) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
