package model

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/zolstein/monoslices/internal/config"
	"github.com/zolstein/monoslices/internal/loadpkg"
)

func TestBuildPackageResolvesAllSpecFuncsAndByrefRoles(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item struct{ ID int }
func pred(item *Item) bool { return item.ID > 0 }
func equal(left *Item, right *Item) bool { return left.ID == right.ID }
func compare(left *Item, right *Item) int { return left.ID - right.ID }

//monoslices:generate name=Items pred=pred eq=equal cmp=compare byref=true
`,
	})
	plan := buildPackageForTest(t, dir)
	if len(plan.Directives) != 1 {
		t.Fatalf("directive count = %d, want 1", len(plan.Directives))
	}
	got := plan.Directives[0]
	if len(got.Funcs) != 3 {
		t.Fatalf("function count = %d, want 3", len(got.Funcs))
	}
	if len(got.Operations) != len(config.SupportedOperations()) {
		t.Fatalf("operation count = %d, want %d", len(got.Operations), len(config.SupportedOperations()))
	}
	for _, operation := range got.Operations {
		if operation.Op == config.OperationBinarySearch {
			if !operation.Arg1ByRef || operation.Arg2ByRef {
				t.Fatalf("binary-search byref roles = (%v, %v), want (true, false)", operation.Arg1ByRef, operation.Arg2ByRef)
			}
			if rendered := plan.Types.Render(operation.Target); rendered != "*Item" {
				t.Fatalf("binary-search target = %q, want *Item", rendered)
			}
		} else if operation.Op == config.OperationSort || operation.Op == config.OperationSortStable {
			if !operation.Arg1ByRef || !operation.Arg2ByRef {
				t.Fatalf("%s byref roles = (%v, %v), want both true", operation.Op, operation.Arg1ByRef, operation.Arg2ByRef)
			}
		}
	}
}

func TestBuildPackageAcceptsSpecFuncBeforeDirective(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int

func compare(a, b Item) int { return int(a - b) }

//monoslices:generate name=Items cmp=compare ops=compare
`,
	})
	plan := buildPackageForTest(t, dir)
	fn := plan.Directives[0].Funcs[config.SemanticCmp]
	if fn == nil || fn.Params[0].Source.String() != "Item" {
		t.Fatalf("function before directive = %#v, want source Item", fn)
	}
}

func TestBuildPackageAcceptsSpecFuncAfterDirective(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int

//monoslices:generate name=Items cmp=compare ops=compare

func compare(a, b Item) int { return int(a - b) }
`,
	})
	plan := buildPackageForTest(t, dir)
	fn := plan.Directives[0].Funcs[config.SemanticCmp]
	if fn == nil || fn.Params[0].Source.String() != "Item" {
		t.Fatalf("function after directive = %#v, want source Item", fn)
	}
}

func TestBuildPackageRejectsAmbiguousSpecFuncDeclarations(t *testing.T) {
	for _, test := range []struct {
		name  string
		first string
		other string
	}{
		{
			name:  "distinct parameter types",
			first: "func compare(a, b Item) int { return 0 }",
			other: "func compare(a, b Other) int { return 0 }",
		},
		{
			name:  "one matching and one mismatching shape",
			first: "func compare(a, b Item) int { return 0 }",
			other: "func compare(a Item) int { return 0 }",
		},
		{
			name:  "identical declarations",
			first: "func compare(a, b Item) int { return 0 }",
			other: "func compare(a, b Item) int { return 0 }",
		},
	} {
		for _, reverse := range []bool{false, true} {
			order := "first declaration first"
			first, second := test.first, test.other
			if reverse {
				order = "second declaration first"
				first, second = second, first
			}
			t.Run(test.name+"/"+order, func(t *testing.T) {
				source := fmt.Sprintf(`package sample

type Item int
type Other int

%s
%s

//monoslices:generate name=Items cmp=compare ops=compare
`, first, second)
				dir := temporaryPackage(t, map[string]string{"records.go": source})
				_, err := BuildPackage(loadPackageForTest(t, dir))
				if err == nil || !strings.Contains(err.Error(), `specialization name "compare" has multiple package-level function declarations in the directive file`) || !strings.Contains(err.Error(), "cannot be resolved unambiguously") {
					t.Fatalf("BuildPackage() error = %v, want ambiguous function diagnostic", err)
				}
				if !strings.Contains(err.Error(), "records.go:6:") || !strings.Contains(err.Error(), "records.go:7:") {
					t.Fatalf("ambiguity diagnostic = %v, want both declaration locations", err)
				}
			})
		}
	}
}

func TestBuildPackagePlansWithInvalidSpecializationBody(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int
func compare(a, b Item) int { return unresolved(a, b) }

//monoslices:generate name=Items cmp=compare ops=sort
`,
	})
	plan := buildPackageForTest(t, dir)
	if got, want := operationNames(plan.Directives[0].Operations), []config.Operation{config.OperationSort}; !reflect.DeepEqual(got, want) {
		t.Fatalf("operation plan = %v, want %v", got, want)
	}
	if len(plan.Directives[0].Funcs[config.SemanticCmp].Params) != 2 {
		t.Fatalf("comparator parameters = %d, want 2", len(plan.Directives[0].Funcs[config.SemanticCmp].Params))
	}
}

func TestBuildPackageCountsLogicalSpecFuncParameters(t *testing.T) {
	for _, test := range []struct {
		name     string
		function string
	}{
		{name: "grouped", function: "func compare(a, b Item) int { return int(a - b) }"},
		{name: "separate", function: "func compare(a Item, b Item) int { return int(a - b) }"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := temporaryPackage(t, map[string]string{
				"records.go": "package sample\ntype Item int\n" + test.function + "\n//monoslices:generate name=Items cmp=compare ops=compare\n",
			})
			plan := buildPackageForTest(t, dir)
			params := plan.Directives[0].Funcs[config.SemanticCmp].Params
			if len(params) != 2 || params[0].Source.String() != "Item" || params[1].Source.String() != "Item" {
				t.Fatalf("comparator parameters = %#v, want two Item parameters", params)
			}
		})
	}
}

func TestBuildPackageAcceptsPredeclaredSpecFuncResultSpelling(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int

func pred(item Item) bool { return item > 0 }
func equal(a, b Item) bool { return a == b }
func compare(a, b Item) int { return int(a - b) }

//monoslices:generate name=Items pred=pred eq=equal cmp=compare ops=contains,equal,sort
`,
	})
	plan := buildPackageForTest(t, dir)
	for _, kind := range []config.SemanticKind{config.SemanticPred, config.SemanticEq, config.SemanticCmp} {
		if plan.Directives[0].Funcs[kind] == nil {
			t.Fatalf("specialization function %s was not resolved", kind)
		}
	}
}

func TestBuildPackageAcceptsNamedPredeclaredSpecFuncResults(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int

func pred(item Item) (ok bool) { return item > 0 }
func equal(a, b Item) (same bool) { return a == b }
func compare(a, b Item) (result int) { return int(a - b) }

//monoslices:generate name=Items pred=pred eq=equal cmp=compare ops=contains,equal,sort
`,
	})
	plan := buildPackageForTest(t, dir)
	for _, kind := range []config.SemanticKind{config.SemanticPred, config.SemanticEq, config.SemanticCmp} {
		if plan.Directives[0].Funcs[kind] == nil {
			t.Fatalf("named specialization function %s was not resolved", kind)
		}
	}
}

func TestBuildPackageRejectsSpecFuncResultAlias(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		kind  string
		want  string
	}{
		{
			name: "local alias",
			files: map[string]string{
				"records.go": `package sample

type Item int
type Result = bool
func pred(item Item) Result { return item > 0 }

//monoslices:generate name=Items pred=pred ops=contains
`,
			},
			kind: "bool",
		},
		{
			name: "local int alias",
			files: map[string]string{
				"records.go": `package sample

type Item int
type Result = int
func compare(a, b Item) Result { return int(a - b) }

//monoslices:generate name=Items cmp=compare ops=compare
`,
			},
			kind: "int",
		},
		{
			name: "imported bool alias",
			files: map[string]string{
				"helper/helper.go": `package helper

type Result = bool
`,
				"records.go": `package sample

import helper "example.com/sample/helper"

type Item int
func pred(item Item) helper.Result { return item > 0 }

//monoslices:generate name=Items pred=pred ops=contains
`,
			},
			kind: "bool",
		},
		{
			name: "imported int alias",
			files: map[string]string{
				"helper/helper.go": `package helper

type Result = int
`,
				"records.go": `package sample

import helper "example.com/sample/helper"

type Item int
func compare(a, b Item) helper.Result { return int(a - b) }

//monoslices:generate name=Items cmp=compare ops=compare
`,
			},
			kind: "int",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := temporaryPackage(t, test.files)
			loaded := loadPackageForTest(t, dir)
			_, err := BuildPackage(loaded)
			if err == nil || !strings.Contains(err.Error(), "must spell its result type "+test.kind) {
				t.Fatalf("BuildPackage() error = %v, want source result spelling diagnostic", err)
			}
		})
	}
}

func TestBuildPackageRejectsBuildVaryingSpecFuncResultAlias(t *testing.T) {
	otherGOOS := "windows"
	if runtime.GOOS == otherGOOS {
		otherGOOS = "linux"
	}
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int

func pred(item Item) Result { return platformPred(item) }

//monoslices:generate name=Items pred=pred ops=contains
`,
		"result_" + runtime.GOOS + ".go": `package sample

type Result = bool
func platformPred(Item) bool { return true }
`,
		"result_" + otherGOOS + ".go": `package sample

type Result = int
func platformPred(Item) int { return 1 }
`,
	})
	loaded := loadPackageForTest(t, dir)
	_, err := BuildPackage(loaded)
	if err == nil || !strings.Contains(err.Error(), "must spell its result type bool") {
		t.Fatalf("BuildPackage() error = %v, want source result spelling diagnostic", err)
	}
}

func TestBuildPackageRejectsCrossFileSpecFunc(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int

//monoslices:generate name=Items cmp=compare ops=compare
`,
		"specializations.go": `package sample

func compare(a, b Item) int { return int(a - b) }
`,
	})
	loaded := loadPackageForTest(t, dir)
	_, err := BuildPackage(loaded)
	if err == nil || !strings.Contains(err.Error(), "cmp=compare must be declared in the same source file as the monoslices directive") {
		t.Fatalf("BuildPackage() error = %v, want same-file specialization-function diagnostic", err)
	}
}

func TestBuildPackageResolvesMultipleSpecFuncsInDirectiveFile(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int

func pred(item Item) bool { return item > 0 }
func equal(a, b Item) bool { return a == b }
func compare(a, b Item) int { return int(a - b) }

//monoslices:generate name=Items pred=pred eq=equal cmp=compare ops=contains,equal,sort
`,
	})
	plan := buildPackageForTest(t, dir)
	if got := len(plan.Directives[0].Funcs); got != 3 {
		t.Fatalf("function count = %d, want 3", got)
	}
	for _, kind := range []config.SemanticKind{config.SemanticPred, config.SemanticEq, config.SemanticCmp} {
		if plan.Directives[0].Funcs[kind] == nil {
			t.Fatalf("specialization function %s was not resolved", kind)
		}
	}
}

func TestBuildPackageRejectsOneSpecFuncOutsideDirectiveFile(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int

func pred(item Item) bool { return item > 0 }
func equal(a, b Item) bool { return a == b }

//monoslices:generate name=Items pred=pred eq=equal cmp=compare ops=contains,equal,sort
`,
		"specializations.go": `package sample

func compare(a, b Item) int { return int(a - b) }
`,
	})
	loaded := loadPackageForTest(t, dir)
	_, err := BuildPackage(loaded)
	if err == nil || !strings.Contains(err.Error(), "cmp=compare must be declared in the same source file as the monoslices directive") {
		t.Fatalf("BuildPackage() error = %v, want same-file specialization-function diagnostic", err)
	}
}

func TestBuildPackageSelectsSameFileSpecFuncOverConstrainedDeclaration(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int

func compare(a, b Item) int { return int(a - b) }

//monoslices:generate name=Items cmp=compare ops=compare
`,
		"other.go": `package sample

type OtherItem int
`,
		"compare_alternate.go": `//go:build alternate

package sample

func compare(a, b OtherItem) int { return int(a - b) }
`,
	})
	plan := buildPackageForTest(t, dir)
	operation := plan.Directives[0].Operations[0]
	if operation.Elem1.String() != "Item" || operation.Elem2.String() != "Item" {
		t.Fatalf("source types = %q, %q; want same-file Item", operation.Elem1, operation.Elem2)
	}
}

func TestBuildPackagePreservesConstrainedSpecFuncTypeSymbolically(t *testing.T) {
	typeFile := "types_" + runtime.GOOS + ".go"
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

func compare(left, right Item) int { return int(left - right) }
//monoslices:generate name=Items cmp=compare ops=compare
`,
		typeFile: `package sample

type Item int
`,
	})
	plan := buildPackageForTest(t, dir)
	operation := plan.Directives[0].Operations[0]
	if operation.Elem1.String() != "Item" || operation.Elem2.String() != "Item" {
		t.Fatalf("symbolic specialization function elements = %q, %q; want Item, Item", operation.Elem1, operation.Elem2)
	}
}

func TestBuildPackageRejectsCrossFileSpecFuncBeforeSemanticValidation(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

//monoslices:generate name=Items cmp=compare ops=compare
`,
		"specializations.go": `package sample

func compare(left, right int) bool { return left < right }
`,
	})
	loaded := loadPackageForTest(t, dir)
	_, err := BuildPackage(loaded)
	if err == nil || !strings.Contains(err.Error(), "cmp=compare must be declared in the same source file as the monoslices directive") {
		t.Fatalf("BuildPackage() error = %v, want same-file specialization-function diagnostic", err)
	}
}

func TestBuildPackageTreatsDifferentAliasesForOneImportPathAsHeterogeneous(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"helper/item.go": `package helper

type Item int
`,
		"records.go": `package sample

import (
	h "example.com/sample/helper"
	helper "example.com/sample/helper"
)

func compare(left h.Item, right helper.Item) int { return int(left - right) }
//monoslices:generate name=Items cmp=compare
`,
	})
	plan := buildPackageForTest(t, dir)
	operations := operationNames(plan.Directives[0].Operations)
	if len(operations) != 2 || operations[0] != config.OperationCompare || operations[1] != config.OperationBinarySearch {
		t.Fatalf("applicable operations = %v, want compare and binary-search", operations)
	}
	compare := plan.Directives[0].Operations[0]
	if compare.Elem1.String() != "h.Item" || compare.Elem2.String() != "helper.Item" {
		t.Fatalf("source types = %q, %q; want h.Item, helper.Item", compare.Elem1, compare.Elem2)
	}
}

func TestBuildPackageKeepsHeterogeneousEqualitySpecFuncApplicable(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"helper/item.go": `package helper

type Item int
`,
		"records.go": `package sample

import (
	h "example.com/sample/helper"
	helper "example.com/sample/helper"
)

func equal(left h.Item, right helper.Item) bool { return left == right }
//monoslices:generate name=Items eq=equal
`,
	})
	plan := buildPackageForTest(t, dir)
	operations := operationNames(plan.Directives[0].Operations)
	if len(operations) != 1 || operations[0] != config.OperationEqual {
		t.Fatalf("applicable operations = %v, want equal only", operations)
	}
}

func TestBuildPackageAcceptsFormattingAndParenthesesForHomogeneousComparator(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int
func compare(left map[string]Item, right (map[ string ]Item)) int { return 0 }
//monoslices:generate name=Items cmp=compare ops=sort
`,
	})
	plan := buildPackageForTest(t, dir)
	if len(plan.Directives[0].Operations) != 1 || plan.Directives[0].Operations[0].Op != config.OperationSort {
		t.Fatalf("operations = %v, want sort", operationNames(plan.Directives[0].Operations))
	}
}

func TestBuildPackageRejectsHeterogeneousSortBeforeUnusedAliasConflict(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"helper/item.go": `package helper

type Value int
`,
		"records.go": `package sample

import (
	item "example.com/sample/helper"
	len "example.com/sample/helper"
)

func compare(left item.Value, right len.Value) int { return int(left - right) }
//monoslices:generate name=Items cmp=compare ops=sort
`,
	})
	loaded := loadPackageForTest(t, dir)
	_, err := BuildPackage(loaded)
	if err == nil || !strings.Contains(err.Error(), `operation "sort" requires a homogeneous comparator`) {
		t.Fatalf("BuildPackage() error = %v, want homogeneous comparator diagnostic", err)
	}
	if strings.Contains(err.Error(), `source-type import alias "len"`) {
		t.Fatalf("BuildPackage() reported an unused second source-type alias conflict: %v", err)
	}
}

func TestBuildPackageRejectsCrossFileSpecFuncBeforeVariantChecks(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int
//monoslices:generate name=Items cmp=compare ops=compare
`,
		"compare_default.go": `//go:build !alternate

package sample

func compare(a, b Item) int { return int(a - b) }
`,
		"compare_alternate.go": `//go:build alternate

package sample

func compare(a, b string) int { return len(a) - len(b) }
`,
	})
	loaded := loadPackageForTest(t, dir)
	_, err := BuildPackage(loaded)
	if err == nil || !strings.Contains(err.Error(), "cmp=compare must be declared in the same source file as the monoslices directive") {
		t.Fatalf("BuildPackage() error = %v, want same-file specialization-function diagnostic", err)
	}
}

func TestBuildPackagePreservesSymbolicArrayLengthSpecFuncType(t *testing.T) {
	otherGOOS := "windows"
	if runtime.GOOS == otherGOOS {
		otherGOOS = "linux"
	}
	constantFile := "length_" + runtime.GOOS + ".go"
	otherConstantFile := "length_" + otherGOOS + ".go"
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

func compare(left, right [N]int) int { return 0 }
//monoslices:generate name=Items cmp=compare ops=compare
`,
		constantFile: `package sample

const N = 4
`,
		otherConstantFile: `package sample

const N = 8
`,
	})
	plan := buildPackageForTest(t, dir)
	operation := plan.Directives[0].Operations[0]
	if operation.Elem1.String() != "[N]int" || operation.Elem2.String() != "[N]int" {
		t.Fatalf("symbolic specialization function elements = %q, %q; want [N]int, [N]int", operation.Elem1, operation.Elem2)
	}
}

func TestBuildPackageRejectsPointerAliasForByref(t *testing.T) {
	otherGOOS := "windows"
	if runtime.GOOS == otherGOOS {
		otherGOOS = "linux"
	}
	activeAliasFile := "item_" + runtime.GOOS + ".go"
	otherAliasFile := "item_" + otherGOOS + ".go"
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

func compare(left, right ItemPtr) int { return 0 }
//monoslices:generate name=Items cmp=compare byref=true ops=compare
`,
		activeAliasFile: `package sample

type ItemPtr = *int
`,
		otherAliasFile: `package sample

type ItemPtr = *string
`,
	})
	loaded := loadPackageForTest(t, dir)
	_, err := BuildPackage(loaded)
	if err == nil || !strings.Contains(err.Error(), "explicit pointer syntax") {
		t.Fatalf("BuildPackage() error = %v, want explicit pointer syntax diagnostic", err)
	}
}

func TestBuildPackageUsesSourceStableComparatorHomogeneity(t *testing.T) {
	otherGOOS := "windows"
	if runtime.GOOS == otherGOOS {
		otherGOOS = "linux"
	}
	activeTypesFile := "types_" + runtime.GOOS + ".go"
	otherTypesFile := "types_" + otherGOOS + ".go"
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Left = outerLeft
type outerLeft = platformLeft
type Right = outerRight
type outerRight = platformRight

func compare(left Left, right Right) int { return 0 }
//monoslices:generate name=Items cmp=compare
`,
		activeTypesFile: `package sample

type platformLeft = int
type platformRight = int
`,
		otherTypesFile: `package sample

type platformLeft = int
type platformRight = string
`,
	})
	plan := buildPackageForTest(t, dir)
	var operations []config.Operation
	for _, operation := range plan.Directives[0].Operations {
		operations = append(operations, operation.Op)
		if operation.Op == config.OperationCompare {
			if operation.Elem1.String() != "Left" || operation.Elem2.String() != "Right" {
				t.Fatalf("compare source types = %q, %q; want Left, Right", operation.Elem1, operation.Elem2)
			}
		}
		if operation.Op == config.OperationBinarySearch {
			if operation.Elem1.String() != "Left" || operation.Target.String() != "Right" {
				t.Fatalf("binary-search source types = %q, %q; want Left, Right", operation.Elem1, operation.Target)
			}
		}
		if operation.Op == config.OperationSort || operation.Op == config.OperationMin || operation.Op == config.OperationMax || operation.Op == config.OperationIsSorted {
			t.Fatalf("%s unexpectedly inferred from source-distinct specialization function types", operation.Op)
		}
	}
	if len(operations) != 2 || operations[0] != config.OperationCompare || operations[1] != config.OperationBinarySearch {
		t.Fatalf("applicable operations = %v, want compare and binary-search", operations)
	}
}

func TestBuildPackageRejectsPointerAliasChainForByref(t *testing.T) {
	otherGOOS := "windows"
	if runtime.GOOS == otherGOOS {
		otherGOOS = "linux"
	}
	activeTypesFile := "types_" + runtime.GOOS + ".go"
	otherTypesFile := "types_" + otherGOOS + ".go"
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type ItemPtr = outerPtr
type outerPtr = platformPtr

func compare(left ItemPtr, right ItemPtr) int { return 0 }
//monoslices:generate name=Items cmp=compare byref=true ops=sort
`,
		activeTypesFile: `package sample

type platformPtr = *int
`,
		otherTypesFile: `package sample

type platformPtr = *string
`,
	})
	loaded := loadPackageForTest(t, dir)
	_, err := BuildPackage(loaded)
	if err == nil || !strings.Contains(err.Error(), "explicit pointer syntax") {
		t.Fatalf("BuildPackage() error = %v, want explicit pointer syntax diagnostic", err)
	}
}

func TestBuildPackagePreservesImportedGenericSourceType(t *testing.T) {
	typeFile := "platform_" + runtime.GOOS + ".go"
	dir := temporaryPackage(t, map[string]string{
		"helper/box.go": `package helper

type Box[T any] struct{ Value T }
`,
		"records.go": `package sample

import helper "example.com/sample/helper"

func compare(left, right helper.Box[platformInt]) int { return 0 }
//monoslices:generate name=Items cmp=compare ops=compare
`,
		typeFile: `package sample

type platformInt int
`,
	})
	plan := buildPackageForTest(t, dir)
	operation := plan.Directives[0].Operations[0]
	if operation.Elem1.String() != "helper.Box[platformInt]" || operation.Elem2.String() != "helper.Box[platformInt]" {
		t.Fatalf("symbolic generic elements = %q, %q", operation.Elem1, operation.Elem2)
	}
	if got := plan.Types.Render(operation.Elem1); got != "helper.Box[platformInt]" {
		t.Fatalf("rendered generic element = %q, want helper.Box[platformInt]", got)
	}
}

func TestBuildPackageRejectsDefaultImportedSpecFuncType(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"helper/item.go": `package helper

type Item int
`,
		"records.go": `package sample

import "example.com/sample/helper"

func compare(left, right helper.Item) int { return int(left - right) }
//monoslices:generate name=Items cmp=compare ops=compare
`,
	})
	loaded := loadPackageForTest(t, dir)
	_, err := BuildPackage(loaded)
	if err == nil || !strings.Contains(err.Error(), `specialization function parameter type uses qualifier "helper" that is not bound by an explicit import alias`) || !strings.Contains(err.Error(), "imported qualifiers used in specialization function parameter types must have explicit aliases") || strings.Contains(err.Error(), "example.com/sample/helper") {
		t.Fatalf("BuildPackage() error = %v, want unresolved explicit-alias specialization function type diagnostic", err)
	}
}

func TestBuildPackageAcceptsLocalSymbolicTypeOverDefaultImport(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"helper/item.go": `package helper

type Item int
`,
		"records.go": `package sample

import "example.com/sample/helper"

type Item = helper.Item
func compare(left, right Item) int { return int(left - right) }
//monoslices:generate name=Items cmp=compare ops=compare
`,
	})
	plan := buildPackageForTest(t, dir)
	operation := plan.Directives[0].Operations[0]
	if operation.Elem1.String() != "Item" || len(operation.Elem1.ImportBindings()) != 0 {
		t.Fatalf("local symbolic specialization function type = %q with bindings %#v, want Item with no imports", operation.Elem1, operation.Elem1.ImportBindings())
	}
}

func TestBuildPackageAllowsDefaultImportUsedOnlyBySpecFuncBody(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

import "fmt"

type Item int
func compare(left, right Item) int {
	fmt.Println(left, right)
	return int(left - right)
}
//monoslices:generate name=Items cmp=compare ops=compare
`,
	})
	plan := buildPackageForTest(t, dir)
	if len(plan.Directives[0].Operations) != 1 || plan.Directives[0].Operations[0].Elem1.String() != "Item" {
		t.Fatalf("specialization function body default import plan = %#v, want one Item operation", plan.Directives[0].Operations)
	}
}

func TestBuildPackageAllowsUnconstrainedNamedTypeWithConstrainedRepresentation(t *testing.T) {
	platformFile := "platform_" + runtime.GOOS + ".go"
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item struct{ value platformInt }
func compare(left, right *Item) int {
	if left.value < right.value {
		return -1
	}
	if left.value > right.value {
		return 1
	}
	return 0
}
//monoslices:generate name=Items cmp=compare byref=true ops=compare
`,
		platformFile: `package sample

type platformInt int
`,
	})
	plan := buildPackageForTest(t, dir)
	operation := plan.Directives[0].Operations[0]
	if got := plan.Types.Render(operation.Elem1); got != "Item" {
		t.Fatalf("generated element type = %q, want Item", got)
	}
	if !operation.Arg1ByRef || !operation.Arg2ByRef {
		t.Fatalf("by-reference roles = (%v, %v), want both true", operation.Arg1ByRef, operation.Arg2ByRef)
	}
}

func TestBuildPackageRejectsUnsupportedSpecFuncs(t *testing.T) {
	tests := []struct {
		name           string
		functionSource string
		nameRef        string
		want           string
	}{
		{
			name:           "non-function",
			functionSource: "var value = 1",
			nameRef:        "value",
			want:           "must be declared in the same source file as the monoslices directive",
		},
		{
			name:           "wrong result spelling",
			functionSource: "func wrongResult(value int) int { return value }",
			nameRef:        "wrongResult",
			want:           "must spell its result type bool",
		},
		{
			name:           "wrong arity",
			functionSource: "func wrongArity(left, right int) bool { return left > right }",
			nameRef:        "wrongArity",
			want:           "must have signature func(E) bool",
		},
		{
			name:           "zero results",
			functionSource: "func noResult(value int) {}",
			nameRef:        "noResult",
			want:           "must have signature func(E) bool",
		},
		{
			name:           "multiple result fields",
			functionSource: "func multipleResults(value int) (bool, bool) { return true, true }",
			nameRef:        "multipleResults",
			want:           "must have signature func(E) bool",
		},
		{
			name:           "grouped multiple results",
			functionSource: "func groupedResults(value int) (first, second bool) { return true, true }",
			nameRef:        "groupedResults",
			want:           "must have signature func(E) bool",
		},
		{
			name:           "variadic",
			functionSource: "func variadic(values ...int) bool { return len(values) > 0 }",
			nameRef:        "variadic",
			want:           "must have signature func(E) bool",
		},
		{
			name:           "generic",
			functionSource: "func generic[T any](value int) bool { return value > 0 }",
			nameRef:        "generic",
			want:           "generic semantic functions are unsupported",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := temporaryPackage(t, map[string]string{
				"records.go": "package sample\ntype Item int\n" + test.functionSource + "\n//monoslices:generate name=Items pred=" + test.nameRef + " ops=contains\n",
			})
			loaded := loadPackageForTest(t, dir)
			_, err := BuildPackage(loaded)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("BuildPackage() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestBuildPackagePlansExplicitPointersForByrefOrdinaryAndSort(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

 type Item struct{ ID int }
 type ItemPtr = *Item
 func pred(item *Item) bool { return item.ID > 0 }
 func compare(left *Item, right *Item) int { return left.ID - right.ID }

 //monoslices:generate name=Items pred=pred cmp=compare byref=true ops=contains,sort
`,
	})
	plan := buildPackageForTest(t, dir)
	if len(plan.Directives[0].Operations) != 2 || len(plan.SortGroups) != 1 {
		t.Fatalf("operations=%v, sort groups=%d; want contains, sort and one group", operationNames(plan.Directives[0].Operations), len(plan.SortGroups))
	}
	for _, operation := range plan.Directives[0].Operations {
		if got := plan.Types.Render(operation.Elem1); got != "Item" {
			t.Fatalf("%s primary element = %q, want Item", operation.Op, got)
		}
		if !operation.Arg1ByRef {
			t.Fatalf("%s parameter 1 is not by-reference", operation.Op)
		}
		if operation.Op == config.OperationSort {
			if !operation.Arg2ByRef || !plan.SortGroups[0].Arg1ByRef || !plan.SortGroups[0].Arg2ByRef {
				t.Fatalf("sort byref roles/group = (%v, %v, %v), want all true", operation.Arg2ByRef, plan.SortGroups[0].Arg1ByRef, plan.SortGroups[0].Arg2ByRef)
			}
		}
	}
}

func TestBuildPackagePreservesBinarySearchPointerAliasTarget(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

 type Item struct{ ID int }
 type ItemPtr = *Item
 func compare(left *Item, right ItemPtr) int { return left.ID - right.ID }

 //monoslices:generate name=Items cmp=compare byref=true ops=binary-search
`,
	})
	plan := buildPackageForTest(t, dir)
	operation := plan.Directives[0].Operations[0]
	if operation.Op != config.OperationBinarySearch || !operation.Arg1ByRef || operation.Arg2ByRef {
		t.Fatalf("binary-search plan = %#v, want first argument by-reference and literal target", operation)
	}
	if !operation.Target.SameSpelling(operation.Func.Params[1].Source) {
		t.Fatal("binary-search target was not preserved as the specialization function's literal second parameter source type")
	}
	if got := plan.Types.Render(operation.Target); got != "ItemPtr" {
		t.Fatalf("binary-search target = %q, want ItemPtr", got)
	}
}

func TestBuildPackageRejectsDefinedPointerTypesForByref(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

 type Item int
 type ItemPtr *Item
 func pred(item ItemPtr) bool { return *item > 0 }

 //monoslices:generate name=Items pred=pred byref=true ops=contains
`,
	})
	loaded := loadPackageForTest(t, dir)
	_, err := BuildPackage(loaded)
	if err == nil || !strings.Contains(err.Error(), `operation "contains" requires pred=pred parameter 1`) {
		t.Fatalf("BuildPackage() error = %v, want defined pointer type rejection", err)
	}
}

func TestBuildPackageSelectiveByrefAndCanonicalOperations(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int
func pred(item *Item) bool { return *item > 0 }
func compare(left *Item, right *Item) int { return int(*left - *right) }

//monoslices:generate name=Items pred=pred cmp=compare byref=true ops=binary-search,contains,sort
`,
	})
	plan := buildPackageForTest(t, dir)
	operations := plan.Directives[0].Operations
	want := []config.Operation{config.OperationContains, config.OperationSort, config.OperationBinarySearch}
	if len(operations) != len(want) {
		t.Fatalf("operations = %v, want %v", operationNames(operations), want)
	}
	for index, operation := range operations {
		if operation.Op != want[index] {
			t.Fatalf("operation[%d] = %q, want %q", index, operation.Op, want[index])
		}
		if operation.Op == config.OperationBinarySearch && operation.Arg2ByRef {
			t.Fatal("binary-search target incorrectly marked by-reference")
		}
	}
}

func TestBuildPackagePlansByrefByLiteralParameterPosition(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		params      string
		wantElem1   string
		wantElem2   string
		wantArg1Ref bool
		wantArg2Ref bool
	}{
		{name: "all", mode: "true", params: "left *Large, right **Key", wantElem1: "Large", wantElem2: "*Key", wantArg1Ref: true, wantArg2Ref: true},
		{name: "first", mode: "first", params: "left *Large, right *Key", wantElem1: "Large", wantElem2: "*Key", wantArg1Ref: true},
		{name: "second", mode: "second", params: "left Large, right **Key", wantElem1: "Large", wantElem2: "*Key", wantArg2Ref: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := temporaryPackage(t, map[string]string{
				"records.go": "package sample\ntype Large struct{ N int }\ntype Key int\nfunc compare(" + test.params + ") int { return 0 }\n//monoslices:generate name=Items cmp=compare byref=" + test.mode + " ops=compare\n",
			})
			plan := buildPackageForTest(t, dir)
			operation := plan.Directives[0].Operations[0]
			if got := plan.Types.Render(operation.Elem1); got != test.wantElem1 {
				t.Fatalf("first element = %q, want %q", got, test.wantElem1)
			}
			if got := plan.Types.Render(operation.Elem2); got != test.wantElem2 {
				t.Fatalf("second element = %q, want %q", got, test.wantElem2)
			}
			if operation.Arg1ByRef != test.wantArg1Ref || operation.Arg2ByRef != test.wantArg2Ref {
				t.Fatalf("byref arguments = (%v, %v), want (%v, %v)", operation.Arg1ByRef, operation.Arg2ByRef, test.wantArg1Ref, test.wantArg2Ref)
			}
		})
	}
}

func TestBuildPackageValidatesPositionalByrefAgainstGeneratedSliceOperands(t *testing.T) {
	t.Run("applies to one of several operations", func(t *testing.T) {
		dir := temporaryPackage(t, map[string]string{
			"records.go": `package sample

type Item int
func pred(item Item) bool { return item > 0 }
func compare(left Item, right *Item) int { return int(left - *right) }
//monoslices:generate name=Items pred=pred cmp=compare byref=second ops=contains,sort
`,
		})
		plan := buildPackageForTest(t, dir)
		if len(plan.Directives[0].Operations) != 2 {
			t.Fatalf("operation count = %d, want 2", len(plan.Directives[0].Operations))
		}
		if plan.Directives[0].Operations[0].Arg1ByRef || !plan.Directives[0].Operations[1].Arg2ByRef {
			t.Fatalf("positional selection was not evaluated independently: %#v", plan.Directives[0].Operations)
		}
	})

	for _, test := range []struct {
		name   string
		source string
	}{
		{
			name: "unary function has no second parameter",
			source: `package sample

type Item int
func pred(item Item) bool { return item > 0 }
//monoslices:generate name=Items pred=pred byref=second ops=contains
`,
		},
		{
			name: "binary search target is not a slice operand",
			source: `package sample

type Item int
func compare(item Item, target *Item) int { return int(item - *target) }
//monoslices:generate name=Items cmp=compare byref=second ops=binary-search
`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := temporaryPackage(t, map[string]string{"records.go": test.source})
			_, err := BuildPackage(loadPackageForTest(t, dir))
			if err == nil || !strings.Contains(err.Error(), "does not select an eligible slice-element parameter") {
				t.Fatalf("BuildPackage() error = %v, want ineffective positional-selector diagnostic", err)
			}
		})
	}
}

func TestBuildPackageAllowsDisjointDuplicateNames(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int
func pred(item Item) bool { return item > 0 }
func equal(left Item, right Item) bool { return left == right }
func compare(left Item, right Item) int { return int(left - right) }

//monoslices:generate name=Items pred=pred ops=contains
//monoslices:generate name=Items eq=equal ops=equal
//monoslices:generate name=Items cmp=compare ops=compare
//monoslices:generate name=Sorted cmp=compare ops=sort
//monoslices:generate name=Sorted cmp=compare ops=binary-search
`,
	})
	plan := buildPackageForTest(t, dir)
	if len(plan.Directives) != 5 {
		t.Fatalf("directive count = %d, want 5", len(plan.Directives))
	}
	seen := make(map[string]bool)
	for _, directive := range plan.Directives {
		for _, operation := range directive.Operations {
			if seen[operation.PublicName] {
				t.Fatalf("duplicate planned name %q", operation.PublicName)
			}
			seen[operation.PublicName] = true
		}
	}
}

func TestBuildPackageReportsDuplicateGeneratedLocations(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"a.go": `package sample

type Item int
func compare(left Item, right Item) int { return int(left - right) }
//monoslices:generate name=Items cmp=compare ops=sort
`,
		"b.go": `package sample

func compareOther(left Item, right Item) int { return int(left - right) }
//monoslices:generate name=Items cmp=compareOther ops=sort
`,
	})
	loaded := loadPackageForTest(t, dir)
	_, err := BuildPackage(loaded)
	if err == nil || !strings.Contains(err.Error(), "ItemsSort") || !strings.Contains(err.Error(), "a.go:") || !strings.Contains(err.Error(), "b.go:") {
		t.Fatalf("BuildPackage() error = %v, want both duplicate directive locations", err)
	}
}

func TestBuildPackageRejectsPublicNameFromImportBinding(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

import ItemsSort "fmt"

var _ = ItemsSort.Sprintf

type Item int
func compare(left Item, right Item) int { return int(left - right) }
//monoslices:generate name=Items cmp=compare ops=sort
`,
	})
	loaded := loadPackageForTest(t, dir)
	_, err := BuildPackage(loaded)
	if err == nil || !strings.Contains(err.Error(), "ItemsSort") || !strings.Contains(err.Error(), "import binding") || !strings.Contains(err.Error(), "records.go:") {
		t.Fatalf("BuildPackage() error = %v, want import-binding conflict with source position", err)
	}
}

func TestBuildPackageRenamesHelperForImportBinding(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

import monoslices_FoosSort_PDQSort_helper "fmt"

var _ = monoslices_FoosSort_PDQSort_helper.Sprintf

type Item int
func compare(left Item, right Item) int { return int(left - right) }
//monoslices:generate name=Foos cmp=compare ops=sort
`,
	})
	plan := buildPackageForTest(t, dir)
	if got := plan.SortGroups[0].Helpers["PDQSort"]; got != "monoslices_FoosSort_PDQSort_helper2" {
		t.Fatalf("PDQSort helper = %q, want import-binding suffix", got)
	}
}

func TestBuildPackageRenamesHelperForSamePackageTestImportBinding(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int
func compare(left Item, right Item) int { return int(left - right) }
//monoslices:generate name=Foos cmp=compare ops=sort
`,
		"records_test.go": `package sample

import monoslices_FoosSort_PDQSort_helper "fmt"

var _ = monoslices_FoosSort_PDQSort_helper.Sprintf
`,
	})
	plan := buildPackageForTest(t, dir)
	if got := plan.SortGroups[0].Helpers["PDQSort"]; got != "monoslices_FoosSort_PDQSort_helper2" {
		t.Fatalf("PDQSort helper = %q, want same-package test import suffix", got)
	}
}

func TestBuildPackageRejectsPublicNameFromSamePackageTestDeclaration(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int
func compare(left Item, right Item) int { return int(left - right) }
//monoslices:generate name=Items cmp=compare ops=sort
`,
		"records_test.go": `package sample

func ItemsSort() {}
`,
	})
	loaded := loadPackageForTest(t, dir)
	_, err := BuildPackage(loaded)
	if err == nil || !strings.Contains(err.Error(), "ItemsSort") || !strings.Contains(err.Error(), "records.go:") || !strings.Contains(err.Error(), "records_test.go:") {
		t.Fatalf("BuildPackage() error = %v, want same-package test declaration conflict with both locations", err)
	}
}

func TestBuildPackageRejectsSamePackageTestDotImport(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"names/names.go": `package names

func ItemsSort() {}
`,
		"records.go": `package sample

type Item int
func compare(left Item, right Item) int { return int(left - right) }
//monoslices:generate name=Items cmp=compare ops=sort
`,
		"records_test.go": `package sample

import . "example.com/sample/names"

var _ = ItemsSort
`,
	})
	loaded := loadPackageForTest(t, dir)
	found := false
	for _, imported := range includedSourceImportsForTest(loaded) {
		if imported.Filename == "records_test.go" && imported.Dot {
			if !imported.Test || imported.Constrained {
				t.Fatalf("same-package test dot import = %#v, want source provenance", imported)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("source imports = %#v, want same-package test dot import", includedSourceImportsForTest(loaded))
	}
	_, err := BuildPackage(loaded)
	if err == nil || !strings.Contains(err.Error(), "ItemsSort") || !strings.Contains(err.Error(), "dot-imported identifier") || !strings.Contains(err.Error(), "records.go:") || !strings.Contains(err.Error(), "records_test.go:") {
		t.Fatalf("BuildPackage() error = %v, want direct same-package test dot-import diagnostic", err)
	}
}

func TestBuildPackageDoesNotResolveInactiveDefaultImportForHelperNames(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"helper/value.go": `package monoslices_FoosSort_PDQSort_helper

type Value int
`,
		"records.go": `package sample

type Item int
func compare(left Item, right Item) int { return int(left - right) }
//monoslices:generate name=Foos cmp=compare ops=sort
`,
		"hand_windows.go": `//go:build windows

package sample

import "example.com/sample/helper"
var _ = monoslices_FoosSort_PDQSort_helper.Value(0)
`,
	})
	plan := buildPackageForTest(t, dir)
	if got := plan.SortGroups[0].Helpers["PDQSort"]; got != "monoslices_FoosSort_PDQSort_helper" {
		t.Fatalf("PDQSort helper = %q, want unresolved default import not to occupy generated namespace", got)
	}
}

func TestBuildPackageRejectsPublicNameFromInactiveImportBinding(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int
func compare(left Item, right Item) int { return int(left - right) }
//monoslices:generate name=Items cmp=compare ops=sort
`,
		"hand_windows.go": `//go:build windows

package sample

import ItemsSort "fmt"
var _ = ItemsSort.Sprintf
`,
	})
	loaded := loadPackageForTest(t, dir)
	_, err := BuildPackage(loaded)
	if err == nil || !strings.Contains(err.Error(), "ItemsSort") || !strings.Contains(err.Error(), "import binding") || !strings.Contains(err.Error(), "hand_windows.go:") {
		t.Fatalf("BuildPackage() error = %v, want inactive import-binding conflict with source position", err)
	}
}

func TestBuildPackageRejectsPublicNameFromActiveDotImport(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"names/names.go": `package names

func ItemsSort() {}
`,
		"records.go": `package sample

import . "example.com/sample/names"
var _ = ItemsSort

type Item int
func compare(left Item, right Item) int { return int(left - right) }
//monoslices:generate name=Items cmp=compare ops=sort
`,
	})
	loaded := loadPackageForTest(t, dir)
	_, err := BuildPackage(loaded)
	if err == nil || !strings.Contains(err.Error(), "ItemsSort") || !strings.Contains(err.Error(), "dot-imported identifier") || !strings.Contains(err.Error(), "records.go:") {
		t.Fatalf("BuildPackage() error = %v, want active dot-import conflict with source position", err)
	}
}

func TestBuildPackageRejectsProductionDotImportWithoutNamespaceReconstruction(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"dep/common.go": `package dep

var Common int
`,
		"dep/items_windows.go": `package dep

var ItemsSort int
`,
		"records.go": `package sample

import . "example.com/sample/dep"
var _ = Common

type Item int
func compare(left Item, right Item) int { return int(left - right) }
//monoslices:generate name=Items cmp=compare ops=sort
`,
	})
	loaded := loadPackageForTest(t, dir)
	foundNamespace := false
	for _, imported := range includedSourceImportsForTest(loaded) {
		if imported.Filename == "records.go" && imported.Dot {
			foundNamespace = true
		}
	}
	if !foundNamespace {
		t.Fatalf("source imports = %#v, want ItemsSort from windows dependency variant", includedSourceImportsForTest(loaded))
	}
	_, err := BuildPackage(loaded)
	if err == nil || !strings.Contains(err.Error(), "ItemsSort") || !strings.Contains(err.Error(), "dot-imported identifier") || !strings.Contains(err.Error(), "records.go:") {
		t.Fatalf("BuildPackage() error = %v, want direct production dot-import diagnostic", err)
	}
}

func TestBuildPackageRejectsPublicNameFromInactiveDotImport(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int
func compare(left Item, right Item) int { return int(left - right) }
//monoslices:generate name=Items cmp=compare ops=sort
`,
		"hand_windows.go": `//go:build windows

package sample

import . "example.com/sample/names"
`,
	})
	loaded := loadPackageForTest(t, dir)
	_, err := BuildPackage(loaded)
	if err == nil || !strings.Contains(err.Error(), "dot imports in inactive source files are not supported") || !strings.Contains(err.Error(), "hand_windows.go:") {
		t.Fatalf("BuildPackage() error = %v, want explicit inactive dot-import policy", err)
	}
}

func TestBuildPackageRejectsConstrainedDotImportFromActiveFile(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skip("the regression uses an active _amd64.go source file")
	}
	dir := temporaryPackage(t, map[string]string{
		"dep/common.go": `package dep

var Common int
`,
		"dep/extra_windows.go": `package dep

var ItemsSort int
`,
		"records.go": `package sample

type Item int
func compare(left Item, right Item) int { return int(left - right) }
//monoslices:generate name=Items cmp=compare ops=sort
`,
		"hand_amd64.go": `package sample

import . "example.com/sample/dep"

var _ = Common
`,
	})
	loaded := loadPackageForTest(t, dir)
	foundConstrainedDot := false
	for _, imported := range includedSourceImportsForTest(loaded) {
		if imported.Filename == "hand_amd64.go" && imported.Dot && imported.Constrained {
			foundConstrainedDot = true
			break
		}
	}
	if !foundConstrainedDot {
		t.Fatalf("source imports = %#v, want constrained dot import from active file", includedSourceImportsForTest(loaded))
	}
	_, err := BuildPackage(loaded)
	if err == nil || !strings.Contains(err.Error(), "dot imports in inactive source files are not supported") || !strings.Contains(err.Error(), "hand_amd64.go:") {
		t.Fatalf("BuildPackage() error = %v, want conservative constrained dot-import rejection", err)
	}
}

func TestBuildPackageRejectsPublicNameFromInactiveSource(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int
func compare(left Item, right Item) int { return int(left - right) }
//monoslices:generate name=Items cmp=compare ops=sort
`,
		"hand_windows.go": `//go:build windows

package sample

func ItemsSort() {}
`,
	})
	loaded := loadPackageForTest(t, dir)
	_, err := BuildPackage(loaded)
	if err == nil || !strings.Contains(err.Error(), "ItemsSort") || !strings.Contains(err.Error(), "hand_windows.go:") {
		t.Fatalf("BuildPackage() error = %v, want inactive declaration conflict", err)
	}
}

func TestBuildPackageRenamesHelperForInactiveSourceDeclaration(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int
func compare(left Item, right Item) int { return int(left - right) }
//monoslices:generate name=Foos cmp=compare ops=sort
`,
		"hand_windows.go": `//go:build windows

package sample

func monoslices_FoosSort_PDQSort_helper() {}
`,
	})
	plan := buildPackageForTest(t, dir)
	if got := plan.SortGroups[0].Helpers["PDQSort"]; got != "monoslices_FoosSort_PDQSort_helper2" {
		t.Fatalf("PDQSort helper = %q, want inactive declaration suffix", got)
	}
}

func TestBuildPackageRejectsPredeclaredNameFromInactiveSource(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int
func compare(left Item, right Item) int { return int(left - right) }
//monoslices:generate name=Items cmp=compare ops=sort
`,
		"hand_windows.go": `//go:build windows

package sample

func len() int { return 0 }
`,
	})
	loaded := loadPackageForTest(t, dir)
	_, err := BuildPackage(loaded)
	if err == nil || !strings.Contains(err.Error(), "shadows predeclared identifier") || !strings.Contains(err.Error(), "len") {
		t.Fatalf("BuildPackage() error = %v, want inactive predeclared conflict", err)
	}
}

func TestBuildPackageReportsHandwrittenConflictAndIgnoresOwnedOutput(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int
func compare(left Item, right Item) int { return int(left - right) }
//monoslices:generate name=Items cmp=compare ops=sort
`,
		"hand.go": `package sample
func ItemsSort() {}
`,
	})
	loaded := loadPackageForTest(t, dir)
	_, err := BuildPackage(loaded)
	if err == nil || !strings.Contains(err.Error(), "ItemsSort") || !strings.Contains(err.Error(), "records.go:") || !strings.Contains(err.Error(), "hand.go:") {
		t.Fatalf("BuildPackage() error = %v, want directive and declaration locations", err)
	}

	ownedOutputDir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int
func compare(left Item, right Item) int { return int(left - right) }
//monoslices:generate name=Items cmp=compare ops=sort
`,
		config.DefaultOutputFilename: "package sample\nfunc ItemsSort() {}\n",
	})
	loaded = loadPackageForTest(t, ownedOutputDir)
	if _, err := BuildPackage(loaded); err != nil {
		t.Fatalf("BuildPackage() with owned output = %v, want no conflict", err)
	}
}

func TestBuildPackageDirectiveDiagnosticsUseDirectiveTerminology(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "inapplicable operation",
			source: `package sample

type Item int
func compare(left, right Item) int { return int(left - right) }

//monoslices:generate name=Items cmp=compare ops=contains
`,
			want: `operation "contains" requires pred`,
		},
		{
			name: "by-reference parameter",
			source: `package sample

type Item int
func compare(left, right Item) int { return int(left - right) }

//monoslices:generate name=Items cmp=compare byref=true ops=sort
`,
			want: `operation "sort" requires cmp=compare parameter 1`,
		},
		{
			name: "homogeneous equality function",
			source: `package sample

type First int
type Second int
func equal(left First, right Second) bool { return false }

//monoslices:generate name=Items eq=equal ops=compact
`,
			want: `operation "compact" requires a homogeneous equality function`,
		},
		{
			name: "homogeneous comparator",
			source: `package sample

type First int
type Second int
func compare(left First, right Second) int { return 0 }

//monoslices:generate name=Items cmp=compare ops=sort
`,
			want: `operation "sort" requires a homogeneous comparator`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := temporaryPackage(t, map[string]string{"records.go": test.source})
			loaded := loadPackageForTest(t, dir)
			_, err := BuildPackage(loaded)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("BuildPackage() error = %v, want %q", err, test.want)
			}
			for _, stale := range []string{"-pred", "-eq", "-cmp"} {
				if strings.Contains(err.Error(), stale) {
					t.Fatalf("BuildPackage() error = %v, contains stale CLI flag %q", err, stale)
				}
			}
		})
	}
}

func TestBuildPackagePlansSortGroupsAndSharedSupport(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item struct{ Key, Seq int }
func byID(left, right Item) int { return left.Key - right.Key }
func byName(left, right Item) int { return right.Key - left.Key }

//monoslices:generate name=Foos cmp=byID ops=sort
//monoslices:generate name=Foos cmp=byName ops=sort-stable
`,
	})
	plan := buildPackageForTest(t, dir)
	if len(plan.SortGroups) != 2 {
		t.Fatalf("sort group count = %d, want 2", len(plan.SortGroups))
	}
	if plan.SortGroups[0].BaseName != "FoosSort" || plan.SortGroups[1].BaseName != "FoosSortStable" {
		t.Fatalf("sort group bases = %q, %q", plan.SortGroups[0].BaseName, plan.SortGroups[1].BaseName)
	}
	if plan.SortGroups[0].Helpers["PDQSort"] == plan.SortGroups[1].Helpers["Stable"] {
		t.Fatal("sorting groups reused a private helper name")
	}
	if plan.SortSupport == nil || len(plan.SortSupport.Helpers) != 6 {
		t.Fatalf("shared unstable support = %#v, want six helpers", plan.SortSupport)
	}
	if got := plan.Types.ImportAlias("math/bits"); got != "bits" {
		t.Fatalf("math/bits alias = %q, want bits", got)
	}
}

func TestBuildPackageSharesInsertionWithinOneSortGroup(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int
func compare(left, right Item) int { return int(left - right) }

//monoslices:generate name=Foos cmp=compare ops=sort,sort-stable
`,
	})
	plan := buildPackageForTest(t, dir)
	if len(plan.SortGroups) != 1 || !plan.SortGroups[0].Unstable || !plan.SortGroups[0].Stable {
		t.Fatalf("sort groups = %#v, want one combined group", plan.SortGroups)
	}
	if len(plan.SortGroups[0].Helpers) != 17 {
		t.Fatalf("combined helper count = %d, want 17", len(plan.SortGroups[0].Helpers))
	}
	if plan.SortGroups[0].Helpers["InsertionSort"] == "" {
		t.Fatal("combined group has no insertion helper")
	}
}

func TestBuildPackageRenamesSortHelperCollision(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int
func compare(left, right Item) int { return int(left - right) }
func monoslices_FoosSort_PDQSort_helper() {}

//monoslices:generate name=Foos cmp=compare ops=sort
`,
	})
	plan := buildPackageForTest(t, dir)
	if got := plan.SortGroups[0].Helpers["PDQSort"]; got != "monoslices_FoosSort_PDQSort_helper2" {
		t.Fatalf("PDQSort helper = %q, want deterministic suffix", got)
	}
}

func TestBuildPackagePreservesSourceTypeImportAliasWhenPlanningSortHelpers(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"helper/helper.go": `package monoslices_FoosSort_PDQSort_helper

type Value int
`,
		"records.go": `package sample

import helper "example.com/sample/helper"
func compare(left, right helper.Value) int { return int(left - right) }

//monoslices:generate name=Foos cmp=compare ops=sort
`,
	})
	plan := buildPackageForTest(t, dir)
	imports := plan.Types.AllImports()
	if len(imports) != 2 {
		t.Fatalf("planned imports = %#v, want external and math/bits", imports)
	}
	for _, imported := range imports {
		if imported.Path == "example.com/sample/helper" && imported.Name != "helper" {
			t.Fatalf("external helper import alias = %q, want preserved source-type alias", imported.Name)
		}
	}
}

func TestBuildPackageStableOnlyOmitsUnstableSupport(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int
func compare(left, right Item) int { return int(left - right) }

//monoslices:generate name=Foos cmp=compare ops=sort-stable
`,
	})
	plan := buildPackageForTest(t, dir)
	if plan.SortSupport != nil {
		t.Fatal("stable-only package planned unstable support")
	}
	if got := plan.Types.ImportAlias("math/bits"); got != "" {
		t.Fatalf("stable-only math/bits alias = %q", got)
	}
}

func TestBuildPackagePlansOneImportNamespace(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"one/one.go": `package shared
 type Value int
`,
		"two/two.go": `package shared
 type Value int
`,
		"records.go": `package sample

import one "example.com/sample/one"
import two "example.com/sample/two"

func compareOne(left one.Value, right one.Value) int { return int(left - right) }
func compareTwo(left two.Value, right two.Value) int { return int(left - right) }
//monoslices:generate name=One cmp=compareOne ops=compare
//monoslices:generate name=Two cmp=compareTwo ops=compare
`,
	})
	plan := buildPackageForTest(t, dir)
	imports := plan.Types.AllImports()
	if len(imports) != 2 || imports[0].Path == imports[1].Path || imports[0].Name == imports[1].Name {
		t.Fatalf("planned imports = %#v, want two distinct aliases", imports)
	}
}

func TestBuildPackagePreservesSourceTypeImportAliasWhenDependencyPackageNameShadowsPredeclaredType(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"dependency/value.go": `package string

type Value int
`,
		"records.go": `package sample

import s "example.com/sample/dependency"

func compare(left, right map[string]s.Value) int { return 0 }
//monoslices:generate name=Items cmp=compare ops=compare
`,
	})
	plan := buildPackageForTest(t, dir)
	operation := plan.Directives[0].Operations[0]
	if got := plan.Types.Render(operation.Elem1); got != "map[string]s.Value" {
		t.Fatalf("rendered specialization function type = %q, want preserved source binding", got)
	}
	imports := plan.Types.AllImports()
	if len(imports) != 1 || imports[0] != (Import{Name: "s", Path: "example.com/sample/dependency"}) {
		t.Fatalf("planned imports = %#v, want one preserved s binding", imports)
	}
}

func TestBuildPackageRejectsConflictingSourceTypeImportAliases(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"one/value.go": `package one

type Value int
`,
		"two/value.go": `package two

type Value int
`,
		"first.go": `package sample

import h "example.com/sample/one"

func compareFirst(left, right h.Value) int { return 0 }
//monoslices:generate name=First cmp=compareFirst ops=compare
`,
		"second.go": `package sample

import h "example.com/sample/two"

func compareSecond(left, right h.Value) int { return 0 }
//monoslices:generate name=Second cmp=compareSecond ops=compare
`,
	})
	loaded := loadPackageForTest(t, dir)
	_, err := BuildPackage(loaded)
	if err == nil || !strings.Contains(err.Error(), `source-type import alias "h"`) || !strings.Contains(err.Error(), "example.com/sample/one") || !strings.Contains(err.Error(), "example.com/sample/two") {
		t.Fatalf("BuildPackage() error = %v, want incompatible aggregate alias diagnostic", err)
	}
}

func TestBuildPackageSupportsDifferentSourceTypeImportAliasesForOnePath(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"dependency/value.go": `package helper

type Value int
`,
		"first.go": `package sample

import h "example.com/sample/dependency"

func compareFirst(left, right h.Value) int { return 0 }
//monoslices:generate name=First cmp=compareFirst ops=compare
`,
		"second.go": `package sample

import helper "example.com/sample/dependency"

func compareSecond(left, right helper.Value) int { return 0 }
//monoslices:generate name=Second cmp=compareSecond ops=compare
`,
	})
	plan := buildPackageForTest(t, dir)
	imports := plan.Types.AllImports()
	want := map[string]bool{"h": false, "helper": false}
	for _, imported := range imports {
		if imported.Path == "example.com/sample/dependency" {
			want[imported.Name] = true
		}
	}
	if len(imports) != 2 || !want["h"] || !want["helper"] {
		t.Fatalf("planned imports = %#v, want both preserved aliases", imports)
	}
}

func TestBuildPackageCoalescesRepeatedSpecFuncImportBinding(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"dependency/value.go": `package helper

type Value int
`,
		"records.go": `package sample

import h "example.com/sample/dependency"

func compareFirst(left, right h.Value) int { return 0 }
func compareSecond(left, right h.Value) int { return 0 }
//monoslices:generate name=First cmp=compareFirst ops=compare
//monoslices:generate name=Second cmp=compareSecond ops=compare
`,
	})
	plan := buildPackageForTest(t, dir)
	imports := plan.Types.AllImports()
	if len(imports) != 1 || imports[0] != (Import{Name: "h", Path: "example.com/sample/dependency"}) {
		t.Fatalf("planned imports = %#v, want one coalesced binding", imports)
	}
}

func TestBuildPackageRejectsSourceTypeImportAliasConflictingWithGeneratorName(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"dependency/value.go": `package dep

type Value int
`,
		"records.go": `package sample

import len "example.com/sample/dependency"

func compare(left, right len.Value) int { return 0 }
//monoslices:generate name=Items cmp=compare ops=compare
`,
	})
	loaded := loadPackageForTest(t, dir)
	_, err := BuildPackage(loaded)
	if err == nil || !strings.Contains(err.Error(), `source-type import alias "len"`) || !strings.Contains(err.Error(), "generated implementation") {
		t.Fatalf("BuildPackage() error = %v, want fixed implementation-name conflict", err)
	}
}

func TestBuildPackageRejectsSourceTypeImportAliasConflictingWithSourceTypePredeclaredUse(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"dependency/value.go": `package dep

type Value int
`,
		"first.go": `package sample

import string "example.com/sample/dependency"

func compareFirst(left, right string.Value) int { return 0 }
//monoslices:generate name=First cmp=compareFirst ops=compare
`,
		"second.go": `package sample

func compareSecond(left, right string) int { return 0 }
//monoslices:generate name=Second cmp=compareSecond ops=compare
`,
	})
	loaded := loadPackageForTest(t, dir)
	_, err := BuildPackage(loaded)
	if err == nil || !strings.Contains(err.Error(), "predeclared identifier") || !strings.Contains(err.Error(), "source-type import alias") {
		t.Fatalf("BuildPackage() error = %v, want source-type predeclared conflict", err)
	}
}

func TestBuildPackageRejectsCgoSourceSpecFuncAsCrossFile(t *testing.T) {
	requireCgoForTest(t)
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

//monoslices:generate name=Items cmp=compare ops=compare
`,
		"compare_cgo.go": `package sample

/*
#include <stdint.h>
*/
import "C"

func compare(left, right C.int) int { return int(left - right) }
`,
	})
	loaded := loadPackageForTest(t, dir)
	_, err := BuildPackage(loaded)
	if err == nil || !strings.Contains(err.Error(), "cmp=compare must be declared in the same source file as the monoslices directive") {
		t.Fatalf("BuildPackage() error = %v, want same-file specialization-function diagnostic", err)
	}
}

func TestImportPlannerReusesFixedAliasForGeneratorImport(t *testing.T) {
	namespace := NewNamespace(nil)
	planner := NewImportPlanner(namespace)
	typ := SourceType{
		text:       "h.Item",
		references: []SourceReference{{Start: 0, End: 1, Name: "h", ImportPath: "example.com/helper"}},
	}
	if err := planner.Add(typ); err != nil {
		t.Fatal(err)
	}
	planner.AddGeneratorImport("example.com/helper", "helper")
	planner.AddGeneratorImport("math/bits", "bits")
	planner.Finalize()
	if got := planner.ImportAlias("example.com/helper"); got != "h" {
		t.Fatalf("reused generator alias = %q, want fixed source-type alias h", got)
	}
	imports := planner.AllImports()
	want := []Import{{Name: "h", Path: "example.com/helper"}, {Name: "bits", Path: "math/bits"}}
	if len(imports) != len(want) || imports[0] != want[0] || imports[1] != want[1] {
		t.Fatalf("planned imports = %#v, want %#v", imports, want)
	}
}

func TestImportPlannerRenderAfterPlanningIsPure(t *testing.T) {
	planner := NewImportPlanner(NewNamespace(nil))
	planned := SourceType{
		text:       "h.Item",
		references: []SourceReference{{Start: 0, End: 1, Name: "h", ImportPath: "example.com/helper"}},
	}
	unplanned := SourceType{
		text:       "other.Item",
		references: []SourceReference{{Start: 0, End: 5, Name: "other", ImportPath: "example.com/other"}},
	}
	if err := planner.Add(planned); err != nil {
		t.Fatal(err)
	}
	planner.Finalize()
	before := planner.AllImports()
	if got := planner.Render(unplanned); got != unplanned.String() {
		t.Fatalf("Render() = %q, want %q", got, unplanned.String())
	}
	if got := planner.AllImports(); !reflect.DeepEqual(got, before) {
		t.Fatalf("imports after render = %#v, want unchanged %#v", got, before)
	}
	if len(before) != 1 || before[0] != (Import{Name: "h", Path: "example.com/helper"}) {
		t.Fatalf("planned imports = %#v, want exact source-type binding", before)
	}
}
func requireCgoForTest(t *testing.T) {
	t.Helper()
	output, err := exec.Command("go", "env", "CGO_ENABLED").Output()
	if err != nil || strings.TrimSpace(string(output)) != "1" {
		t.Skip("cgo is unavailable")
	}
}

func temporaryPackage(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/sample\n\ngo 1.24.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func operationNames(operations []OperationPlan) []config.Operation {
	result := make([]config.Operation, len(operations))
	for index, operation := range operations {
		result[index] = operation.Op
	}
	return result
}

func buildPackageForTest(t *testing.T, dir string) *PackagePlan {
	t.Helper()
	loaded := loadPackageForTest(t, dir)
	plan, err := BuildPackage(loaded)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func loadPackageForTest(t *testing.T, dir string) *loadpkg.Package {
	t.Helper()
	loaded, err := loadpkg.LoadPackage(context.Background(), dir, config.CLIConfig{})
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func includedSourceImportsForTest(pkg *loadpkg.Package) []loadpkg.SourceImport {
	var imports []loadpkg.SourceImport
	for _, file := range pkg.SourceFiles {
		if file.ExternalTest {
			continue
		}
		imports = append(imports, file.Imports...)
	}
	return imports
}
