package model

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/zolstein/monoslices/internal/config"
	"github.com/zolstein/monoslices/internal/loadpkg"
)

func TestSourceTypePreservesExpressionsAndBindsImports(t *testing.T) {
	const source = `package sample

import h "example.com/helper"
import p "example.com/platform"

func compare(a Item, b *Item) int { return 0 }
type declaration struct {
	first Item
	second *Item
	array [N]int
	slice []Item
	mapping map[Key]Value
	anonymous struct { Value Item }
	function func(Item) bool
	qualified h.Item
	generic h.Box[p.Item]
}
`
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "records.go", source, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	file := loadpkg.SourceFile{
		Source: sourceBytes(source),
		AST:    parsed,
		Imports: []loadpkg.SourceImport{
			{Name: "h", ImportPath: "example.com/helper", ExplicitAlias: true},
			{Name: "p", ImportPath: "example.com/platform", ExplicitAlias: true},
		},
	}
	fields := parsed.Decls[3].(*ast.GenDecl).Specs[0].(*ast.TypeSpec).Type.(*ast.StructType).Fields.List
	tests := []struct {
		name string
		expr ast.Expr
		want string
	}{
		{"identifier", fields[0].Type, "Item"},
		{"pointer", fields[1].Type, "*Item"},
		{"array", fields[2].Type, "[N]int"},
		{"slice", fields[3].Type, "[]Item"},
		{"map", fields[4].Type, "map[Key]Value"},
		{"struct", fields[5].Type, "struct { Value Item }"},
		{"function", fields[6].Type, "func(Item) bool"},
		{"qualified", fields[7].Type, "h.Item"},
		{"generic", fields[8].Type, "h.Box[p.Item]"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NewSourceType(fset, &file, test.expr)
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != test.want {
				t.Fatalf("String() = %q, want %q", got.String(), test.want)
			}
		})
	}
	qualified, err := NewSourceType(fset, &file, fields[8].Type)
	if err != nil {
		t.Fatal(err)
	}
	bindings := qualified.ImportBindings()
	if len(bindings) != 2 || bindings[0].Path != "example.com/helper" || bindings[1].Path != "example.com/platform" {
		t.Fatalf("ImportBindings() = %v, want both imported paths in source order", bindings)
	}
	if got := qualified.Render(); got != "h.Box[p.Item]" {
		t.Fatalf("Render() = %q, want preserved source aliases", got)
	}
	if len(bindings) != 2 || bindings[0] != (SourceImportBinding{Name: "h", Path: "example.com/helper"}) || bindings[1] != (SourceImportBinding{Name: "p", Path: "example.com/platform"}) {
		t.Fatalf("ImportBindings() = %#v, want source aliases and paths", bindings)
	}
}

func TestSourceTypeTracksUnqualifiedPredeclaredUses(t *testing.T) {
	const source = `package sample

import s "example.com/string"

func compare(a map[string]s.Value, b map[string]s.Value) int { return 0 }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "records.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	loaded := loadpkg.SourceFile{
		Source:  sourceBytes(source),
		AST:     file,
		Imports: []loadpkg.SourceImport{{Name: "s", ImportPath: "example.com/string", ExplicitAlias: true}},
	}
	typ, err := NewSourceType(fset, &loaded, file.Decls[1].(*ast.FuncDecl).Type.Params.List[0].Type)
	if err != nil {
		t.Fatal(err)
	}
	if got := typ.PredeclaredIdentifiers(); len(got) != 1 || got[0] != "string" {
		t.Fatalf("predeclared identifiers = %v, want [string]", got)
	}
	if got := typ.ImportBindings(); len(got) != 1 || got[0] != (SourceImportBinding{Name: "s", Path: "example.com/string"}) {
		t.Fatalf("import bindings = %v, want s/example.com/string", got)
	}
}

func TestSourceTypeDifferentExplicitQualifiersRemainDifferent(t *testing.T) {
	makeType := func(alias string) SourceType {
		source := "package sample\nimport " + alias + " \"example.com/helper\"\nfunc compare(a " + alias + ".Box[Item], b " + alias + ".Box[Item]) int { return 0 }\n"
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "records.go", source, 0)
		if err != nil {
			t.Fatal(err)
		}
		declaration := file.Decls[1].(*ast.FuncDecl)
		loaded := loadpkg.SourceFile{
			Source:  sourceBytes(source),
			AST:     file,
			Imports: []loadpkg.SourceImport{{Name: alias, ImportPath: "example.com/helper", ExplicitAlias: true}},
		}
		result, err := NewSourceType(fset, &loaded, declaration.Type.Params.List[0].Type)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if makeType("h").SameSpelling(makeType("helper")) {
		t.Fatal("different imported qualifier spellings were treated as homogeneous")
	}
}

func TestSourceTypeNormalizesParenthesesForSameSpelling(t *testing.T) {
	makeType := func(expression string) SourceType {
		source := "package sample\nfunc compare(a " + expression + ", b " + expression + ") int { return 0 }\n"
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "records.go", source, 0)
		if err != nil {
			t.Fatal(err)
		}
		loaded := loadpkg.SourceFile{Source: sourceBytes(source), AST: file}
		result, err := NewSourceType(fset, &loaded, file.Decls[0].(*ast.FuncDecl).Type.Params.List[0].Type)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if !makeType("*Item").SameSpelling(makeType("(*Item)")) {
		t.Fatal("parenthesized pointer was not the same normalized spelling")
	}
}

func TestSourceTypeSameSpellingNormalizesFormatting(t *testing.T) {
	const source = `package sample

type Item int

func compare(a map[string]Item, b (map[ string ]Item)) int { return 0 }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "records.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	loaded := loadpkg.SourceFile{Source: sourceBytes(source), AST: file}
	params := file.Decls[1].(*ast.FuncDecl).Type.Params.List
	first, err := NewSourceType(fset, &loaded, params[0].Type)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewSourceType(fset, &loaded, params[1].Type)
	if err != nil {
		t.Fatal(err)
	}
	if first.String() == second.String() {
		t.Fatal("test setup did not preserve formatting difference")
	}
	if !first.SameSpelling(second) {
		t.Fatalf("formatted source types %q and %q were not homogeneous", first, second)
	}
}

func TestSourceTypeRejectsDefaultImportedQualifiersRecursively(t *testing.T) {
	expressions := []string{
		"helper.Item",
		"helper.Box[Item]",
		"map[helper.Key]helper.Value",
		"[helper.N]Item",
		"func(helper.Input) helper.Output",
		"struct{ Value helper.Item }",
		"[]map[helper.Key]local.Value",
	}
	for _, expression := range expressions {
		t.Run(expression, func(t *testing.T) {
			source := "package sample\nimport \"example.com/helper\"\nfunc compare(a " + expression + ", b " + expression + ") int { return 0 }\n"
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "records.go", source, 0)
			if err != nil {
				t.Fatal(err)
			}
			loaded := loadpkg.SourceFile{
				Source:  sourceBytes(source),
				AST:     file,
				Imports: []loadpkg.SourceImport{{ImportPath: "example.com/helper"}},
			}
			_, err = NewSourceType(fset, &loaded, file.Decls[1].(*ast.FuncDecl).Type.Params.List[0].Type)
			if err == nil || !strings.Contains(err.Error(), `specialization function parameter type uses qualifier "helper" that is not bound by an explicit import alias; imported qualifiers used in specialization function parameter types must have explicit aliases`) {
				t.Fatalf("NewSourceType() error = %v, want unresolved-qualifier diagnostic", err)
			}
			if !strings.Contains(err.Error(), "records.go:3:") || strings.Contains(err.Error(), "example.com/helper") {
				t.Fatalf("diagnostic = %v, want source position without guessing an import path", err)
			}
		})
	}
}

func TestSourceTypeRejectsQualifierWithoutAnyImport(t *testing.T) {
	const source = `package sample

func compare(a missing.Item, b missing.Item) int { return 0 }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "records.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	loaded := loadpkg.SourceFile{Source: sourceBytes(source), AST: file}
	_, err = NewSourceType(fset, &loaded, file.Decls[0].(*ast.FuncDecl).Type.Params.List[0].Type)
	if err == nil || !strings.Contains(err.Error(), `specialization function parameter type uses qualifier "missing" that is not bound by an explicit import alias; imported qualifiers used in specialization function parameter types must have explicit aliases`) {
		t.Fatalf("NewSourceType() error = %v, want unsupported unbound qualifier diagnostic", err)
	}
}

func TestSourceTypeAcceptsMultipleExplicitImportedQualifiers(t *testing.T) {
	const source = `package sample

import (
	helper "example.com/helper"
	model "example.com/model"
)

func compare(a map[helper.Key]model.Value, b map[helper.Key]model.Value) int { return 0 }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "records.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	loaded := loadpkg.SourceFile{
		Source: sourceBytes(source),
		AST:    file,
		Imports: []loadpkg.SourceImport{
			{Name: "helper", ImportPath: "example.com/helper", ExplicitAlias: true},
			{Name: "model", ImportPath: "example.com/model", ExplicitAlias: true},
		},
	}
	typ, err := NewSourceType(fset, &loaded, file.Decls[1].(*ast.FuncDecl).Type.Params.List[0].Type)
	if err != nil {
		t.Fatal(err)
	}
	if got := typ.Render(); got != "map[helper.Key]model.Value" {
		t.Fatalf("Render() = %q, want source spelling unchanged", got)
	}
	want := []SourceImportBinding{{Name: "helper", Path: "example.com/helper"}, {Name: "model", Path: "example.com/model"}}
	if got := typ.ImportBindings(); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("ImportBindings() = %#v, want %#v", got, want)
	}
	wantReferences := []struct {
		name string
		path string
	}{
		{"helper", "example.com/helper"},
		{"model", "example.com/model"},
	}
	if len(typ.references) != len(wantReferences) {
		t.Fatalf("source references = %#v, want %d references", typ.references, len(wantReferences))
	}
	for index, reference := range typ.references {
		wantReference := wantReferences[index]
		if reference.Name != wantReference.name || reference.ImportPath != wantReference.path || typ.String()[reference.Start:reference.End] != reference.Name {
			t.Fatalf("source reference[%d] = %#v, want alias/path %q/%q at its source spelling", index, reference, wantReference.name, wantReference.path)
		}
	}
}

func TestSourceTypeRejectsExplicitAliasBoundToDistinctPathsInEitherOrder(t *testing.T) {
	for _, paths := range [][]string{
		{"example.com/first", "example.com/second"},
		{"example.com/second", "example.com/first"},
	} {
		t.Run(paths[0]+" then "+paths[1], func(t *testing.T) {
			source := "package sample\n\nimport (\n\tshared \"" + paths[0] + "\"\n\tshared \"" + paths[1] + "\"\n)\n\nfunc compare(a shared.Item, b shared.Item) int { return 0 }\n"
			fset := token.NewFileSet()
			parsed, err := parser.ParseFile(fset, "records.go", source, 0)
			if err != nil {
				t.Fatal(err)
			}
			loaded := loadpkg.SourceFile{
				Source: sourceBytes(source),
				AST:    parsed,
				Imports: []loadpkg.SourceImport{
					{Name: "shared", ImportPath: paths[0], ExplicitAlias: true, Filename: "records.go", Line: 4, Column: 2},
					{Name: "shared", ImportPath: paths[1], ExplicitAlias: true, Filename: "records.go", Line: 5, Column: 2},
				},
			}
			_, err = NewSourceType(fset, &loaded, parsed.Decls[1].(*ast.FuncDecl).Type.Params.List[0].Type)
			if err == nil || !strings.Contains(err.Error(), "multiple explicit import bindings") || !strings.Contains(err.Error(), "cannot be reproduced unambiguously") || !strings.Contains(err.Error(), `explicit import alias "shared"`) {
				t.Fatalf("NewSourceType() error = %v, want ambiguous import alias diagnostic", err)
			}
			for _, want := range []string{"records.go:8:", paths[0], paths[1], "records.go:4:", "records.go:5:"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("diagnostic %q does not contain %q", err, want)
				}
			}
		})
	}
}

func TestSourceTypeAcceptsDifferentAliasesForSameImportPath(t *testing.T) {
	const source = `package sample

import (
	h "example.com/shared"
	shared "example.com/shared"
)

func compare(a map[h.Key]shared.Value, b map[h.Key]shared.Value) int { return 0 }
`
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "records.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	loaded := loadpkg.SourceFile{
		Source: sourceBytes(source),
		AST:    parsed,
		Imports: []loadpkg.SourceImport{
			{Name: "h", ImportPath: "example.com/shared", ExplicitAlias: true},
			{Name: "shared", ImportPath: "example.com/shared", ExplicitAlias: true},
		},
	}
	typ, err := NewSourceType(fset, &loaded, parsed.Decls[1].(*ast.FuncDecl).Type.Params.List[0].Type)
	if err != nil {
		t.Fatal(err)
	}
	want := []SourceImportBinding{{Name: "h", Path: "example.com/shared"}, {Name: "shared", Path: "example.com/shared"}}
	if got := typ.ImportBindings(); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("ImportBindings() = %#v, want %#v", got, want)
	}
}

func TestSourceTypeAcceptsLocalSymbolicTypeOverDefaultImport(t *testing.T) {
	const source = `package sample

import "example.com/model"

type Item = model.Item
func compare(a, b Item) int { return 0 }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "records.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	loaded := loadpkg.SourceFile{
		Source:  sourceBytes(source),
		AST:     file,
		Imports: []loadpkg.SourceImport{{ImportPath: "example.com/model"}},
	}
	typ, err := NewSourceType(fset, &loaded, file.Decls[2].(*ast.FuncDecl).Type.Params.List[0].Type)
	if err != nil {
		t.Fatal(err)
	}
	if typ.String() != "Item" || len(typ.ImportBindings()) != 0 {
		t.Fatalf("local symbolic source type = %q with bindings %#v, want Item with no imported binding", typ, typ.ImportBindings())
	}
}

func TestSpecParamsRetainGroupedSourceAndPointerMetadata(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int
func compare(a, b (*Item)) int { return 0 }

//monoslices:generate name=Items cmp=compare byref=true ops=sort
`,
	})
	plan := buildPackageForTest(t, dir)
	params := plan.Directives[0].Funcs[config.SemanticCmp].Params
	if len(params) != 2 {
		t.Fatalf("specialization parameter count = %d, want 2", len(params))
	}
	for index, param := range params {
		if param.Source.String() != "(*Item)" {
			t.Errorf("parameter %d source = %q, want (*Item)", index, param.Source.String())
		}
		if param.PointerElement == nil || param.PointerElement.String() != "Item" {
			t.Errorf("parameter %d pointer element = %#v, want Item", index, param.PointerElement)
		}
	}
}

func TestSpecParamsBindImportedSourceTypes(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"helper/helper.go": `package helper

type Item int
`,
		"records.go": `package sample

import h "example.com/sample/helper"

func compare(left, right h.Item) int { return int(left - right) }

//monoslices:generate name=Items cmp=compare ops=compare
`,
	})
	plan := buildPackageForTest(t, dir)
	params := plan.Directives[0].Funcs[config.SemanticCmp].Params
	if len(params) != 2 || params[0].Source.String() != "h.Item" || params[1].Source.String() != "h.Item" {
		t.Fatalf("imported specialization params = %#v, want two h.Item params", params)
	}
	want := SourceImportBinding{Name: "h", Path: "example.com/sample/helper"}
	if got := params[0].Source.ImportBindings(); len(got) != 1 || got[0] != want {
		t.Fatalf("imported source-type bindings = %v, want [%v]", got, want)
	}
}

func TestSpecParamsFlattenUnnamedFieldsAndKeepAliasOpaque(t *testing.T) {
	dir := temporaryPackage(t, map[string]string{
		"records.go": `package sample

type Item int
type ItemPtr = *Item
func compare(ItemPtr, ItemPtr) int { return 0 }

//monoslices:generate name=Items cmp=compare ops=compare
`,
	})
	plan := buildPackageForTest(t, dir)
	params := plan.Directives[0].Funcs[config.SemanticCmp].Params
	if len(params) != 2 || params[0].Source.String() != "ItemPtr" || params[1].Source.String() != "ItemPtr" {
		t.Fatalf("flattened specialization params = %#v, want two ItemPtr params", params)
	}
	if params[0].PointerElement != nil || params[1].PointerElement != nil {
		t.Fatal("alias-to-pointer specialization unexpectedly received pointer metadata")
	}
}

func sourceBytes(source string) []byte { return []byte(source) }
