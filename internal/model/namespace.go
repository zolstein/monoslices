package model

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/zolstein/monoslices/internal/loadpkg"
)

// SourceConflictKind identifies the source construct that occupies a package
// name. Keeping the kind with the provenance lets diagnostics distinguish a
// declaration from an import binding even though both affect generated
// package declarations.
type SourceConflictKind string

const (
	SourceDeclarationConflict SourceConflictKind = "source declaration"
	SourceImportConflict      SourceConflictKind = "source import binding"
)

// SourceConflict records where a handwritten package name was introduced.
// Multiple records may exist for one name because every discovered source file
// contributes to the namespace of the unconditional generated package.
type SourceConflict struct {
	Kind SourceConflictKind
	Name string

	Filename string
	Path     string
	Line     int
	Column   int

	ImportPath string
}

// GeneratedDeclaration is the provenance of a generated package declaration.
// Source is set for public declarations and is zero for private helpers.
type GeneratedDeclaration struct {
	Name   string
	Source loadpkg.Directive
	Public bool
}

type fixedImportBinding struct {
	Alias  string
	Path   string
	Source loadpkg.Directive
}

// Namespace is the mutable package-level namespace used while planning one
// aggregate generated file. Source declarations and source imports are kept as
// separate conflict classes; generated declarations and generated-file imports
// share the generated file's package block.
type Namespace struct {
	sourceDeclarations map[string][]SourceConflict
	sourceImports      map[string][]SourceConflict

	generatedDeclarations map[string]GeneratedDeclaration
	// generatedImports reuses one generator-owned alias for each import path.
	// generatedImportBindings is the alias-oriented index used for occupancy;
	// unlike the path index it can be queried directly when allocating package
	// declarations or fixed source-type bindings.
	generatedImports        map[string]string
	generatedImportBindings map[string]Import
	fixedImports            map[string]fixedImportBinding

	generatedPredeclared  map[string]struct{}
	sourceTypePredeclared map[string]struct{}
}

// NewNamespace builds the source namespace from all production and
// same-package-test source inventory in pkg. Source imports are deliberately
// not used to occupy generated import aliases: handwritten imports belong to
// individual source-file blocks, while generated declarations must avoid them.
func NewNamespace(pkg *loadpkg.Package, predeclared ...string) *Namespace {
	namespace := &Namespace{
		sourceDeclarations:      make(map[string][]SourceConflict),
		sourceImports:           make(map[string][]SourceConflict),
		generatedDeclarations:   make(map[string]GeneratedDeclaration),
		generatedImports:        make(map[string]string),
		generatedImportBindings: make(map[string]Import),
		fixedImports:            make(map[string]fixedImportBinding),
		generatedPredeclared:    make(map[string]struct{}),
		sourceTypePredeclared:   make(map[string]struct{}),
	}
	if len(predeclared) == 0 {
		predeclared = generatedPredeclaredNames
	}
	for _, name := range predeclared {
		if name != "" {
			namespace.generatedPredeclared[name] = struct{}{}
		}
	}
	if pkg == nil {
		return namespace
	}
	for _, file := range pkg.SourceFiles {
		if file.ExternalTest {
			continue
		}
		for _, declaration := range file.Declarations {
			if declaration.Name == "" {
				continue
			}
			namespace.sourceDeclarations[declaration.Name] = append(namespace.sourceDeclarations[declaration.Name], SourceConflict{
				Kind:     SourceDeclarationConflict,
				Name:     declaration.Name,
				Filename: declaration.Filename,
				Path:     declaration.Path,
				Line:     declaration.Line,
				Column:   declaration.Column,
			})
		}
		for _, imported := range file.Imports {
			// Dot imports are rejected by BuildPackage before namespace planning;
			// retaining dependency exported names here would make generated-name
			// safety depend on inactive dependency variants, which is unsupported.
			if imported.Dot {
				continue
			}
			for _, name := range sourceImportNames(imported) {
				if name == "" || name == "_" {
					continue
				}
				namespace.sourceImports[name] = append(namespace.sourceImports[name], SourceConflict{
					Kind:       SourceImportConflict,
					Name:       name,
					Filename:   imported.Filename,
					Path:       imported.Path,
					Line:       imported.Line,
					Column:     imported.Column,
					ImportPath: imported.ImportPath,
				})
			}
		}
	}
	return namespace
}

// ClaimPublicDeclaration reserves a public generated package declaration and
// reports source or earlier-generated conflicts at both useful locations.
func (namespace *Namespace) ClaimPublicDeclaration(name string, source loadpkg.Directive) error {
	if namespace == nil {
		return diagnostic("cannot claim generated declaration %q without a namespace", name)
	}
	if previous, ok := namespace.generatedDeclarations[name]; ok {
		first := previous.Source.Errorf("generated identifier %q would be generated more than once", name)
		second := source.Errorf("conflicting generation request is here")
		return fmt.Errorf("%s\n%s", first, second)
	}
	// Reserve the generated name even when a source conflict is reported. This
	// lets a later duplicate generated request retain the useful two-location
	// diagnostic before BuildPackage returns the accumulated namespace error.
	namespace.generatedDeclarations[name] = GeneratedDeclaration{Name: name, Source: source, Public: true}
	if conflicts := namespace.sourceDeclarations[name]; len(conflicts) != 0 {
		return namespace.sourceConflictError(name, source, conflicts[0])
	}
	if conflicts := namespace.sourceImports[name]; len(conflicts) != 0 {
		return namespace.sourceConflictError(name, source, conflicts[0])
	}
	if alias, ok := namespace.generatedImportAlias(name); ok {
		return fmt.Errorf("%s\nmonoslices: generated import alias %q is here", source.Errorf("generated identifier %q conflicts with generated import alias", name), alias)
	}
	return nil
}

// ReserveSourceTypePredeclared records universe names that emitted symbolic
// types need to keep unqualified. These names constrain generated import
// aliases, but are deliberately not added to generatedPredeclared: source
// types may intentionally refer to a same-package declaration with that name.
func (namespace *Namespace) ReserveSourceTypePredeclared(names []string, source ...loadpkg.Directive) error {
	if namespace == nil {
		return nil
	}
	var location loadpkg.Directive
	if len(source) != 0 {
		location = source[0]
	}
	for _, name := range names {
		if name == "" {
			continue
		}
		namespace.sourceTypePredeclared[name] = struct{}{}
		if fixed, ok := namespace.fixedImports[name]; ok {
			if location.Filename != "" || location.Path != "" {
				return location.Errorf("predeclared identifier %q used by generated symbolic type conflicts with source-type import alias %q for %q", name, name, fixed.Path)
			}
			return diagnostic("predeclared identifier %q used by generated symbolic type conflicts with source-type import alias %q for %q", name, name, fixed.Path)
		}
	}
	return nil
}

// ReserveImportBinding reserves a source-type import alias exactly as it
// appeared in its source file. Unlike generator-owned imports, this alias may
// not be renamed to resolve an aggregate-file conflict.
func (namespace *Namespace) ReserveImportBinding(alias, path string, source loadpkg.Directive) error {
	if namespace == nil {
		return diagnostic("cannot reserve source-type import binding %q", alias)
	}
	if alias == "" || alias == "_" || alias == "." || path == "" {
		return nil
	}
	if existing, ok := namespace.fixedImports[alias]; ok {
		if existing.Path == path {
			return nil
		}
		first := source.Errorf("source-type import alias %q for %q conflicts with source-type import alias for %q", alias, path, existing.Path)
		second := existing.Source.Errorf("source-type import alias %q for %q is already required here", alias, existing.Path)
		return fmt.Errorf("%s\n%s", first, second)
	}
	if conflicts := namespace.sourceDeclarations[alias]; len(conflicts) != 0 {
		return namespace.sourceConflictError(alias, source, conflicts[0])
	}
	if declaration, ok := namespace.generatedDeclarations[alias]; ok {
		if declaration.Public {
			return source.Errorf("generated import alias %q conflicts with public generated declaration", alias)
		}
		return source.Errorf("generated import alias %q conflicts with generated private declaration", alias)
	}
	if _, ok := namespace.generatedPredeclared[alias]; ok {
		return source.Errorf("source-type import alias %q conflicts with predeclared identifier required by generated implementation", alias)
	}
	if _, ok := namespace.sourceTypePredeclared[alias]; ok {
		return source.Errorf("source-type import alias %q conflicts with predeclared identifier used by generated symbolic type", alias)
	}
	if generated, ok := namespace.generatedImportBindings[alias]; ok {
		if generated.Path == path {
			// A generator-owned import may have been allocated before the fixed
			// source-type binding was discovered. Coalescing the same binding keeps
			// the source alias exact without emitting it twice.
		} else {
			return source.Errorf("source-type import alias %q for %q conflicts with generated import alias for %q", alias, path, generated.Path)
		}
	}
	if alias == "init" {
		return source.Errorf("source-type import alias %q is not available in generated source", alias)
	}
	namespace.fixedImports[alias] = fixedImportBinding{Alias: alias, Path: path, Source: source}
	return nil
}

// FixedImportBindings returns source-type imports in deterministic alias
// order. It is used by the import planner when rendering aggregate imports.
func (namespace *Namespace) FixedImportBindings() []Import {
	if namespace == nil {
		return nil
	}
	aliases := make([]string, 0, len(namespace.fixedImports))
	for alias := range namespace.fixedImports {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	result := make([]Import, 0, len(aliases))
	for _, alias := range aliases {
		binding := namespace.fixedImports[alias]
		result = append(result, Import{Name: alias, Path: binding.Path})
	}
	return result
}

// FixedImportAlias returns one preserved alias for path, or an empty string.
// Generator-owned references to that path may use an existing fixed binding
// rather than introducing an unnecessary second import declaration.
func (namespace *Namespace) FixedImportAlias(path string) string {
	if namespace == nil {
		return ""
	}
	aliases := make([]string, 0)
	for alias, binding := range namespace.fixedImports {
		if binding.Path == path {
			aliases = append(aliases, alias)
		}
	}
	if len(aliases) == 0 {
		return ""
	}
	sort.Strings(aliases)
	return aliases[0]
}

// AllocatePrivateDeclaration reserves a package-level helper name. Allocation
// is deterministic and immediate so later declarations and imports see it.
func (namespace *Namespace) AllocatePrivateDeclaration(preferred string) string {
	if namespace == nil {
		return preferred
	}
	candidate := preferred
	for suffix := 2; namespace.occupiedByPackage(candidate); suffix++ {
		candidate = fmt.Sprintf("%s%d", preferred, suffix)
	}
	namespace.generatedDeclarations[candidate] = GeneratedDeclaration{Name: candidate}
	return candidate
}

// AllocateImportAlias reserves an alias for one generated-file import. The
// source import map is intentionally not consulted: handwritten imports in
// another source file have a different file block and are not a conflict for
// the generated file's import block.
func (namespace *Namespace) AllocateImportAlias(importPath, preferred string) string {
	if namespace == nil {
		return preferred
	}
	if alias, ok := namespace.generatedImports[importPath]; ok {
		return alias
	}
	if fixed := namespace.FixedImportAlias(importPath); fixed != "" {
		return fixed
	}
	if preferred == "" {
		preferred = filepath.Base(importPath)
	}
	alias := preferred
	for suffix := 2; namespace.occupiedByImport(alias); suffix++ {
		alias = fmt.Sprintf("%s%d", preferred, suffix)
	}
	namespace.generatedImports[importPath] = alias
	namespace.generatedImportBindings[alias] = Import{Name: alias, Path: importPath}
	return alias
}

// ValidatePredeclaredDeclarations rejects handwritten package declarations
// that would shadow a predeclared name used by generated implementation code.
func (namespace *Namespace) ValidatePredeclaredDeclarations() error {
	if namespace == nil {
		return nil
	}
	names := make([]string, 0, len(namespace.generatedPredeclared))
	for name := range namespace.generatedPredeclared {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if conflicts := namespace.sourceDeclarations[name]; len(conflicts) != 0 {
			conflict := conflicts[0]
			return fmt.Errorf("%s:%d:%d: monoslices: package-scope declaration %q shadows predeclared identifier %q required by generated implementation", conflictFilename(conflict), conflict.Line, conflict.Column, conflict.Name, name)
		}
	}
	return nil
}

func (namespace *Namespace) occupiedByPackage(name string) bool {
	if name == "" {
		return true
	}
	if _, ok := namespace.sourceDeclarations[name]; ok {
		return true
	}
	if _, ok := namespace.sourceImports[name]; ok {
		return true
	}
	if _, ok := namespace.generatedDeclarations[name]; ok {
		return true
	}
	if _, ok := namespace.generatedImportBindings[name]; ok {
		return true
	}
	if _, ok := namespace.fixedImports[name]; ok {
		return true
	}
	_, ok := namespace.generatedPredeclared[name]
	return ok
}

func (namespace *Namespace) occupiedByImport(name string) bool {
	if name == "" {
		return true
	}
	if _, ok := namespace.sourceDeclarations[name]; ok {
		return true
	}
	if _, ok := namespace.generatedDeclarations[name]; ok {
		return true
	}
	if _, ok := namespace.generatedPredeclared[name]; ok {
		return true
	}
	if _, ok := namespace.sourceTypePredeclared[name]; ok || name == "init" {
		return true
	}
	if _, ok := namespace.fixedImports[name]; ok {
		return true
	}
	if _, ok := namespace.generatedImportBindings[name]; ok {
		return true
	}
	return false
}

func (namespace *Namespace) generatedImportAlias(name string) (string, bool) {
	if _, ok := namespace.fixedImports[name]; ok {
		return name, true
	}
	if _, ok := namespace.generatedImportBindings[name]; ok {
		return name, true
	}
	return "", false
}

func (namespace *Namespace) sourceConflictError(name string, source loadpkg.Directive, conflict SourceConflict) error {
	kind := "existing package declaration"
	if conflict.Kind == SourceImportConflict {
		kind = "import binding"
	}
	first := source.Errorf("generated identifier %q conflicts with %s", name, kind)
	filename := conflictFilename(conflict)
	location := fmt.Sprintf("%s:%d:%d: monoslices: ", filename, conflict.Line, conflict.Column)
	if conflict.Kind == SourceImportConflict {
		return fmt.Errorf("%s\n%simport binding %q is here", first, location, conflict.Name)
	}
	return fmt.Errorf("%s\n%sexisting declaration %q is here", first, location, conflict.Name)
}

func conflictFilename(conflict SourceConflict) string {
	if conflict.Filename != "" {
		return conflict.Filename
	}
	return filepath.Base(conflict.Path)
}
