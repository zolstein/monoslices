package model

import (
	"strings"
	"testing"

	"github.com/zolstein/monoslices/internal/config"
	"github.com/zolstein/monoslices/internal/loadpkg"
)

func TestNamespaceClaimsPublicSourceProvenanceAndGeneratedDuplicates(t *testing.T) {
	pkg := &loadpkg.Package{
		SourceFiles: []loadpkg.SourceFile{
			{Filename: "records.go", Declarations: []loadpkg.SourceDeclaration{{Name: "ProductionSort", Filename: "records.go", Line: 2, Column: 6}}, Imports: []loadpkg.SourceImport{{Name: "ProductionImported", Filename: "records.go", Line: 3, Column: 8, ImportPath: "example.com/production"}}},
			{Filename: "inactive.go", Declarations: []loadpkg.SourceDeclaration{{Name: "ItemsSort", Filename: "inactive.go", Line: 4, Column: 6}}, Imports: []loadpkg.SourceImport{{Name: "InactiveImported", Filename: "inactive.go", Line: 5, Column: 8, ImportPath: "example.com/inactive"}}},
			{Filename: "test.go", Imports: []loadpkg.SourceImport{{Name: "ImportedSort", Filename: "test.go", Line: 3, Column: 8, ImportPath: "example.com/imported"}}, Test: true},
			{Filename: "external_test.go", ExternalTest: true, Declarations: []loadpkg.SourceDeclaration{{Name: "ExternalSort", Filename: "external_test.go", Line: 3, Column: 8}}, Imports: []loadpkg.SourceImport{{Name: "ExternalImported", Filename: "external_test.go", Line: 4, Column: 8, ImportPath: "example.com/external"}}},
		},
	}
	namespace := NewNamespace(pkg)
	source := loadpkg.Directive{Filename: "records.go", Line: 8, Column: 1, Config: config.DirectiveConfig{Name: "Items"}}
	if err := namespace.ClaimPublicDeclaration("ProductionSort", source); err == nil || !strings.Contains(err.Error(), "records.go:2:6") {
		t.Fatalf("production declaration conflict = %v, want source provenance", err)
	}
	if err := namespace.ClaimPublicDeclaration("ItemsSort", source); err == nil || !strings.Contains(err.Error(), "inactive.go:4:6") {
		t.Fatalf("inactive declaration conflict = %v, want source provenance", err)
	}
	for _, name := range []string{"ProductionImported", "InactiveImported"} {
		if err := namespace.ClaimPublicDeclaration(name, source); err == nil || !strings.Contains(err.Error(), "import binding") {
			t.Fatalf("%s conflict = %v, want source import provenance", name, err)
		}
	}
	if err := namespace.ClaimPublicDeclaration("ImportedSort", source); err == nil || !strings.Contains(err.Error(), "import binding") || !strings.Contains(err.Error(), "test.go:3:8") {
		t.Fatalf("import conflict = %v, want import provenance", err)
	}
	if err := namespace.ClaimPublicDeclaration("GeneratedSort", source); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ExternalSort", "ExternalImported"} {
		if err := namespace.ClaimPublicDeclaration(name, source); err != nil {
			t.Fatalf("external-test %s should not occupy namespace: %v", name, err)
		}
	}
	other := source
	other.Line = 12
	if err := namespace.ClaimPublicDeclaration("GeneratedSort", other); err == nil || !strings.Contains(err.Error(), "records.go:8:1") || !strings.Contains(err.Error(), "records.go:12:1") {
		t.Fatalf("duplicate generated conflict = %v, want both directive locations", err)
	}
}

func TestNamespaceUsesKnownImportBindingsForConflictsAndHelpers(t *testing.T) {
	namespace := NewNamespace(&loadpkg.Package{
		SourceFiles: []loadpkg.SourceFile{{
			Filename: "records.go",
			Imports: []loadpkg.SourceImport{
				{Name: "ItemsSort", ImportPath: "example.com/other", ExplicitAlias: true, Filename: "records.go", Line: 3, Column: 8},
				{Name: "helper", ImportPath: "example.com/helper", ExplicitAlias: true, Filename: "records.go", Line: 4, Column: 8},
				{ImportPath: "example.com/UnknownGeneratedName"},
			},
		}},
	})
	source := loadpkg.Directive{Filename: "records.go", Line: 8, Column: 1}
	if err := namespace.ClaimPublicDeclaration("ItemsSort", source); err == nil || !strings.Contains(err.Error(), "import binding") {
		t.Fatalf("explicit alias conflict = %v, want source import binding conflict", err)
	}
	if err := namespace.ClaimPublicDeclaration("UnknownGeneratedName", source); err != nil {
		t.Fatalf("unresolved default import should not occupy generated namespace: %v", err)
	}
	if got := namespace.AllocatePrivateDeclaration("helper"); got != "helper2" {
		t.Fatalf("helper allocation around explicit alias = %q, want helper2", got)
	}
	if got := namespace.AllocatePrivateDeclaration("DefaultHelperName"); got != "DefaultHelperName" {
		t.Fatalf("helper allocation around unresolved default import = %q, want unoccupied name", got)
	}
}

func TestNamespaceAllocatesPrivateAndImportNamesThroughOneNamespace(t *testing.T) {
	namespace := NewNamespace(&loadpkg.Package{
		SourceFiles: []loadpkg.SourceFile{{
			Filename:     "records.go",
			Declarations: []loadpkg.SourceDeclaration{{Name: "helper", Filename: "records.go"}},
			Imports:      []loadpkg.SourceImport{{Name: "imported", Filename: "other.go"}, {Name: "len", Filename: "other.go"}},
		}},
	})
	if got := namespace.AllocatePrivateDeclaration("helper"); got != "helper2" {
		t.Fatalf("private source-declaration collision = %q, want helper2", got)
	}
	if got := namespace.AllocatePrivateDeclaration("imported"); got != "imported2" {
		t.Fatalf("private source-import collision = %q, want imported2", got)
	}
	if got := namespace.AllocatePrivateDeclaration("helper"); got != "helper3" {
		t.Fatalf("private generated collision = %q, want helper3", got)
	}
	if got := namespace.AllocateImportAlias("example.com/len", "len"); got != "len2" {
		t.Fatalf("predeclared import alias = %q, want len2", got)
	}
	if got := namespace.AllocateImportAlias("example.com/helper", "helper"); got != "helper4" {
		t.Fatalf("generated declaration import alias = %q, want helper4", got)
	}
	if got := namespace.AllocateImportAlias("example.com/other", "imported"); got != "imported" {
		t.Fatalf("source import alias = %q, want imported", got)
	}
}

func TestNamespaceImportAliasesOccupyDeclarationsRegardlessOfOrder(t *testing.T) {
	source := loadpkg.Directive{Filename: "records.go", Line: 3, Column: 1}

	importFirst := NewNamespace(nil)
	if got := importFirst.AllocateImportAlias("example.com/helper", "helper"); got != "helper" {
		t.Fatalf("import-first alias = %q, want helper", got)
	}
	if got := importFirst.AllocatePrivateDeclaration("helper"); got != "helper2" {
		t.Fatalf("private declaration after import = %q, want helper2", got)
	}
	if err := importFirst.ClaimPublicDeclaration("helper", source); err == nil || !strings.Contains(err.Error(), "generated import alias") {
		t.Fatalf("public declaration after import = %v, want generated-import conflict", err)
	}

	declarationFirst := NewNamespace(nil)
	if got := declarationFirst.AllocatePrivateDeclaration("helper"); got != "helper" {
		t.Fatalf("declaration-first private name = %q, want helper", got)
	}
	if got := declarationFirst.AllocateImportAlias("example.com/helper", "helper"); got != "helper2" {
		t.Fatalf("import after private declaration = %q, want helper2", got)
	}

	publicFirst := NewNamespace(nil)
	if err := publicFirst.ClaimPublicDeclaration("helper", source); err != nil {
		t.Fatal(err)
	}
	if got := publicFirst.AllocateImportAlias("example.com/helper", "helper"); got != "helper2" {
		t.Fatalf("import after public declaration = %q, want helper2", got)
	}
}

func TestNamespaceReservesFixedSourceTypeImportAliasesForHelpersAndImports(t *testing.T) {
	namespace := NewNamespace(nil)
	source := loadpkg.Directive{Filename: "records.go", Line: 3, Column: 1}
	if err := namespace.ReserveImportBinding("helper", "example.com/helper", source); err != nil {
		t.Fatal(err)
	}
	if got := namespace.AllocatePrivateDeclaration("helper"); got != "helper2" {
		t.Fatalf("private declaration around source-type alias = %q, want helper2", got)
	}
	if err := namespace.ClaimPublicDeclaration("helper", source); err == nil || !strings.Contains(err.Error(), "generated import alias") {
		t.Fatalf("public declaration around source-type alias = %v, want generated-import conflict", err)
	}
	if got := namespace.AllocateImportAlias("example.com/helper", "helper"); got != "helper" {
		t.Fatalf("generator import alias for fixed path = %q, want helper", got)
	}
	if got := namespace.AllocateImportAlias("example.com/other", "helper"); got != "helper3" {
		t.Fatalf("generator import alias around fixed/helper2 names = %q, want helper3", got)
	}
	if got := namespace.AllocateImportAlias("example.com/init", "init"); got != "init2" {
		t.Fatalf("generator init alias = %q, want init2", got)
	}
	namespace.ReserveSourceTypePredeclared([]string{"string"})
	if got := namespace.AllocateImportAlias("example.com/string", "string"); got != "string2" {
		t.Fatalf("generator source-type predeclared alias = %q, want string2", got)
	}
}

func TestNamespaceFixedSourceTypeImportAliasConflictsRegardlessOfOrder(t *testing.T) {
	source := loadpkg.Directive{Filename: "records.go", Line: 3, Column: 1}

	privateFirst := NewNamespace(nil)
	if got := privateFirst.AllocatePrivateDeclaration("helper"); got != "helper" {
		t.Fatalf("private-first name = %q, want helper", got)
	}
	if err := privateFirst.ReserveImportBinding("helper", "example.com/helper", source); err == nil || !strings.Contains(err.Error(), "generated private declaration") {
		t.Fatalf("fixed alias after private declaration = %v, want generated-private conflict", err)
	}

	publicFirst := NewNamespace(nil)
	if err := publicFirst.ClaimPublicDeclaration("helper", source); err != nil {
		t.Fatal(err)
	}
	if err := publicFirst.ReserveImportBinding("helper", "example.com/helper", source); err == nil || !strings.Contains(err.Error(), "public generated declaration") {
		t.Fatalf("fixed alias after public declaration = %v, want public-generated conflict", err)
	}

	generatorFirst := NewNamespace(nil)
	if got := generatorFirst.AllocateImportAlias("example.com/helper", "helper"); got != "helper" {
		t.Fatalf("generator-first alias = %q, want helper", got)
	}
	if err := generatorFirst.ReserveImportBinding("helper", "example.com/other", source); err == nil || !strings.Contains(err.Error(), "generated import alias") {
		t.Fatalf("fixed alias after generator import = %v, want generated-import conflict", err)
	}

	fixedFirst := NewNamespace(nil)
	if err := fixedFirst.ReserveImportBinding("helper", "example.com/helper", source); err != nil {
		t.Fatal(err)
	}
	if got := fixedFirst.AllocateImportAlias("example.com/other", "helper"); got != "helper2" {
		t.Fatalf("generator alias around fixed source-type binding = %q, want helper2", got)
	}
	if got := fixedFirst.AllocateImportAlias("example.com/helper", "helper"); got != "helper" {
		t.Fatalf("generator alias for fixed source-type path = %q, want helper", got)
	}
}

func TestNamespaceRetainsDifferentFixedAliasesForOnePath(t *testing.T) {
	namespace := NewNamespace(nil)
	first := loadpkg.Directive{Filename: "first.go", Line: 3, Column: 1}
	second := loadpkg.Directive{Filename: "second.go", Line: 3, Column: 1}
	if err := namespace.ReserveImportBinding("h", "example.com/helper", first); err != nil {
		t.Fatal(err)
	}
	if err := namespace.ReserveImportBinding("helper", "example.com/helper", second); err != nil {
		t.Fatal(err)
	}
	if err := namespace.ReserveImportBinding("h", "example.com/helper", second); err != nil {
		t.Fatal(err)
	}
	got := namespace.FixedImportBindings()
	want := []Import{{Name: "h", Path: "example.com/helper"}, {Name: "helper", Path: "example.com/helper"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("fixed source-type bindings = %#v, want %#v", got, want)
	}
	if got := namespace.FixedImportAlias("example.com/helper"); got != "h" {
		t.Fatalf("fixed alias = %q, want deterministic first alias h", got)
	}
}

func TestNamespacePredeclaredValidationUsesSourceDeclarationsOnly(t *testing.T) {
	namespace := NewNamespace(&loadpkg.Package{
		SourceFiles: []loadpkg.SourceFile{{
			Filename:     "records.go",
			Declarations: []loadpkg.SourceDeclaration{{Name: "len", Filename: "records.go", Line: 2, Column: 5}},
			Imports:      []loadpkg.SourceImport{{Name: "len", Filename: "imports.go", Line: 2, Column: 8}},
		}},
	})
	if err := namespace.ValidatePredeclaredDeclarations(); err == nil || !strings.Contains(err.Error(), "records.go:2:5") || !strings.Contains(err.Error(), "len") {
		t.Fatalf("predeclared validation = %v, want declaration diagnostic", err)
	}
}
