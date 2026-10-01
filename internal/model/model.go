// Package model resolves specialization functions from parsed source and turns
// package directives into complete aggregate operation plans. Code generation
// should consume these plans rather than inspect source declarations itself.
package model

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zolstein/monoslices/internal/config"
	"github.com/zolstein/monoslices/internal/loadpkg"
)

// SpecParam retains the source expression from the directive file that
// must be preserved for generated declarations.
type SpecParam struct {
	Source         SourceType
	PointerElement *SourceType
}

// SpecFunc is a validated package-scope function represented by the source
// information needed for planning and generation.
type SpecFunc struct {
	Kind   config.SemanticKind
	Name   string
	Params []SpecParam
}

// OperationPlan contains all type and function-role decisions for one
// generated operation.
type OperationPlan struct {
	Op         config.Operation
	PublicName string
	Elem1      SourceType
	Elem2      SourceType
	Target     SourceType
	Func       *SpecFunc
	Arg1ByRef  bool
	Arg2ByRef  bool
}

// PackagePlan is the aggregate semantic model for one source package. All
// directives share the package analysis and the one import namespace.
type PackagePlan struct {
	Package    *loadpkg.Package
	Directives []DirectivePlan
	Namespace  *Namespace
	Types      *ImportPlanner

	// SortGroups are emitted after all directive operation blocks. The groups
	// are in directive source order and contain names allocated against the
	// complete package namespace.
	SortGroups []SortGroupPlan
	// SortSupport is shared by every unstable sorting group in the file.
	SortSupport *SortSupportPlan
}

// SortGroupPlan describes one directive's comparator-specific sorting helper
// graph in the aggregate output. A directive may request both sorting
// operations, but it still has only one group so insertion sort is emitted once.
type SortGroupPlan struct {
	SourcePath   string
	SourceOffset int
	BaseName     string
	Cmp          *SpecFunc
	Elem         SourceType
	Arg1ByRef    bool
	Arg2ByRef    bool
	Unstable     bool
	Stable       bool
	Helpers      map[string]string
}

// SortSupportPlan contains comparator-independent declarations shared by all
// unstable sorting groups in one generated file.
type SortSupportPlan struct {
	Helpers map[string]string
}

// DirectivePlan contains the validated semantic model for one source
// directive.
type DirectivePlan struct {
	Source     loadpkg.Directive
	Config     config.DirectiveConfig
	Funcs      map[config.SemanticKind]*SpecFunc
	Operations []OperationPlan
}

// BuildPackage validates every source directive against parsed package metadata
// and builds one import namespace for the complete generated declaration set. It
// never loads or type-checks a package itself.
func BuildPackage(pkg *loadpkg.Package) (*PackagePlan, error) {
	if pkg == nil {
		return nil, diagnostic("package metadata is unavailable")
	}
	namespace := NewNamespace(pkg)
	if len(pkg.Directives) != 0 {
		// Dot imports make the source package namespace depend on an external
		// package's exported declarations. Reject them before operation names
		// are planned rather than reconstructing that dependency namespace.
		for _, file := range pkg.SourceFiles {
			if file.ExternalTest {
				continue
			}
			for _, imported := range file.Imports {
				if imported.Dot {
					return nil, sourceDotImportDiagnostic(imported, pkg.Directives)
				}
			}
		}
		if err := namespace.ValidatePredeclaredDeclarations(); err != nil {
			return nil, err
		}
	}
	plan := &PackagePlan{
		Package:   pkg,
		Namespace: namespace,
		Types:     NewImportPlanner(namespace),
	}
	var namespaceError error

	for index := range pkg.Directives {
		source := &pkg.Directives[index]
		file := sourceFileForDirective(pkg, source)
		if file == nil {
			return nil, source.Errorf("internal source file lookup failed")
		}
		cfg := source.Config
		funcs := make(map[config.SemanticKind]*SpecFunc)
		for _, entry := range []struct {
			kind config.SemanticKind
			name string
		}{
			{config.SemanticPred, cfg.Pred},
			{config.SemanticEq, cfg.Eq},
			{config.SemanticCmp, cfg.Cmp},
		} {
			if entry.name == "" {
				continue
			}
			fn, err := resolveSpecFunc(pkg, file, entry.kind, entry.name, source)
			if err != nil {
				return nil, err
			}
			funcs[entry.kind] = fn
		}

		directive := DirectivePlan{
			Source: *source,
			Config: cfg,
			Funcs:  funcs,
		}
		for _, op := range directiveOperations(cfg) {
			operation, applicable, reason := makePlan(op, funcs, cfg.ByRef)
			if !applicable {
				if cfg.OpsSet {
					return nil, source.Errorf("%s", reason)
				}
				continue
			}
			operation.PublicName = cfg.Name + operationGoName(op)
			directive.Operations = append(directive.Operations, *operation)
		}
		if len(directive.Operations) == 0 {
			return nil, source.Errorf("no applicable operations")
		}
		if cfg.ByRef == config.ByRefFirst || cfg.ByRef == config.ByRefSecond {
			selected := false
			for _, operation := range directive.Operations {
				if cfg.ByRef == config.ByRefFirst && operation.Arg1ByRef || cfg.ByRef == config.ByRefSecond && operation.Arg2ByRef {
					selected = true
					break
				}
			}
			if !selected {
				return nil, source.Errorf("byref=%s does not select an eligible slice-element parameter in any generated operation", cfg.ByRef)
			}
		}

		plan.Directives = append(plan.Directives, directive)
		for operationIndex := range directive.Operations {
			operation := &directive.Operations[operationIndex]
			// Source-type aliases are fixed aggregate-file bindings. Reserve
			// them before public declarations and private sorting helpers so those
			// names can diagnose conflicts or move around them deterministically.
			for _, typ := range []SourceType{operation.Elem1, operation.Elem2, operation.Target} {
				if err := plan.Types.Add(typ, *source); err != nil {
					return nil, err
				}
			}
			if err := plan.Namespace.ClaimPublicDeclaration(operation.PublicName, *source); err != nil {
				// Continue planning so a later duplicate generated name can
				// supersede an earlier source-conflict diagnostic with the useful
				// two-location duplicate report.
				if namespaceError == nil || strings.Contains(err.Error(), "would be generated more than once") {
					namespaceError = err
				}
			}
			if operation.Op == config.OperationSort {
				plan.Types.AddGeneratorImport("math/bits", "bits")
			}
		}
	}
	if namespaceError != nil {
		return nil, namespaceError
	}
	if err := planSortGroups(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

var unstableSortHelperNames = []string{
	"InsertionSort", "SiftDown", "HeapSort", "PDQSort", "Partition",
	"PartitionEqual", "PartialInsertionSort", "BreakPatterns", "ChoosePivot",
	"Order2", "Median", "MedianAdjacent", "ReverseRange",
}

var stableSortHelperNames = []string{"InsertionSort", "Stable", "SymMerge", "Rotate", "SwapRange"}

var sharedUnstableSortHelperNames = []string{
	"SortedHint", "UnknownHint", "IncreasingHint", "DecreasingHint",
	"Xorshift", "NextPowerOfTwo",
}

// planSortGroups allocates every private sorting declaration only after public
// declarations have been validated. This keeps helper and import aliases
// stable across the whole aggregate file.
func planSortGroups(plan *PackagePlan) error {
	allocator := plan.Namespace
	var firstUnstable *SortGroupPlan
	for _, directive := range plan.Directives {
		var unstable, stable *OperationPlan
		for index := range directive.Operations {
			switch directive.Operations[index].Op {
			case config.OperationSort:
				unstable = &directive.Operations[index]
			case config.OperationSortStable:
				stable = &directive.Operations[index]
			}
		}
		if unstable == nil && stable == nil {
			continue
		}
		group := SortGroupPlan{
			SourcePath:   directive.Source.Path,
			SourceOffset: directive.Source.Offset,
			Cmp:          sortCmp(unstable, stable),
			Elem:         sortElement(unstable, stable),
			Arg1ByRef:    sortOperation(unstable, stable).Arg1ByRef,
			Arg2ByRef:    sortOperation(unstable, stable).Arg2ByRef,
			Unstable:     unstable != nil,
			Stable:       stable != nil,
			Helpers:      make(map[string]string),
		}
		if unstable != nil {
			group.BaseName = unstable.PublicName
		} else {
			group.BaseName = stable.PublicName
		}
		names := make([]string, 0, len(unstableSortHelperNames)+len(stableSortHelperNames))
		if group.Unstable {
			names = append(names, unstableSortHelperNames...)
		}
		if group.Stable {
			for _, name := range stableSortHelperNames {
				if name == "InsertionSort" && group.Unstable {
					continue
				}
				names = append(names, name)
			}
		}
		for _, helper := range names {
			preferred := GeneratedHelperName(group.BaseName, helper)
			group.Helpers[helper] = allocator.AllocatePrivateDeclaration(preferred)
		}
		plan.SortGroups = append(plan.SortGroups, group)
		if firstUnstable == nil && group.Unstable {
			copy := group
			firstUnstable = &copy
		}
	}
	if firstUnstable != nil {
		support := &SortSupportPlan{Helpers: make(map[string]string, len(sharedUnstableSortHelperNames))}
		for _, helper := range sharedUnstableSortHelperNames {
			preferred := GeneratedHelperName(firstUnstable.BaseName, helper)
			support.Helpers[helper] = allocator.AllocatePrivateDeclaration(preferred)
		}
		plan.SortSupport = support
	}
	return nil
}

func sortCmp(unstable, stable *OperationPlan) *SpecFunc {
	if unstable != nil {
		return unstable.Func
	}
	return stable.Func
}

func sortElement(unstable, stable *OperationPlan) SourceType {
	if unstable != nil {
		return unstable.Elem1
	}
	return stable.Elem1
}

func sortOperation(unstable, stable *OperationPlan) *OperationPlan {
	if unstable != nil {
		return unstable
	}
	return stable
}

func directiveOperations(cfg config.DirectiveConfig) []config.Operation {
	if !cfg.OpsSet {
		return config.SupportedOperations()
	}
	selected := make(map[config.Operation]bool, len(cfg.Ops))
	for _, operation := range cfg.Ops {
		selected[operation] = true
	}
	operations := make([]config.Operation, 0, len(cfg.Ops))
	for _, operation := range config.SupportedOperations() {
		if selected[operation] {
			operations = append(operations, operation)
		}
	}
	return operations
}

func sourceDotImportDiagnostic(imported loadpkg.SourceImport, directives []loadpkg.Directive) error {
	filename := imported.Filename
	if filename == "" {
		filename = filepath.Base(imported.Path)
	}
	message := "dot imports are not supported when generation directives are present; dot-imported identifier namespaces cannot be checked safely"
	if imported.Constrained {
		message = "dot imports in inactive source files are not supported; dot-imported identifier namespaces cannot be checked safely"
	}
	var generated []string
	for _, directive := range directives {
		for _, operation := range directiveOperations(directive.Config) {
			generated = append(generated, directive.Config.Name+operationGoName(operation))
		}
	}
	if len(generated) != 0 {
		message += fmt.Sprintf(" (generated identifiers include %s)", strings.Join(generated, ", "))
	}
	for _, directive := range directives {
		directiveFilename := directive.Filename
		if directiveFilename == "" {
			directiveFilename = filepath.Base(directive.Path)
		}
		message += fmt.Sprintf("; directive at %s:%d:%d", directiveFilename, directive.Line, directive.Column)
	}
	return fmt.Errorf("%s:%d:%d: monoslices: %s", filename, imported.Line, imported.Column, message)
}

func sourceFileForDirective(pkg *loadpkg.Package, directive *loadpkg.Directive) *loadpkg.SourceFile {
	if pkg == nil || directive == nil {
		return nil
	}
	path := canonicalSourcePath(directive.Path)
	if path == "" {
		return nil
	}
	for index := range pkg.SourceFiles {
		file := &pkg.SourceFiles[index]
		if file.Path != "" && canonicalSourcePath(file.Path) == path {
			return file
		}
	}
	return nil
}

func sourceFuncsInFile(file *loadpkg.SourceFile, name string) []*loadpkg.SourceFuncDecl {
	if file == nil || name == "" {
		return nil
	}
	var matches []*loadpkg.SourceFuncDecl
	for index := range file.Functions {
		if file.Functions[index].Name == name {
			matches = append(matches, &file.Functions[index])
		}
	}
	return matches
}

func resolveSpecFunc(pkg *loadpkg.Package, file *loadpkg.SourceFile, kind config.SemanticKind, name string, source *loadpkg.Directive) (*SpecFunc, error) {
	matches := sourceFuncsInFile(file, name)
	if len(matches) == 0 {
		return nil, specDiagnostic(source, kind, name, "must be declared in the same source file as the monoslices directive")
	}
	if len(matches) > 1 {
		first, second := matches[0], matches[1]
		position := token.NoPos
		if first.Decl != nil && first.Decl.Name != nil {
			position = first.Decl.Name.Pos()
		}
		return nil, newSourceDiagnosticError(pkg.SourceFset, position, fmt.Sprintf(
			"specialization name %q has multiple package-level function declarations in the directive file and cannot be resolved unambiguously; declarations at %s:%d:%d and %s:%d:%d",
			name, filepath.Base(first.Filename), first.Line, first.Column,
			filepath.Base(second.Filename), second.Line, second.Column,
		))
	}
	sourceFunction := matches[0]
	declaration := sourceFunction.Decl
	if declaration == nil || declaration.Type == nil {
		return nil, specDiagnostic(source, kind, name, "does not have a function signature")
	}
	if declaration.Type.TypeParams != nil && len(declaration.Type.TypeParams.List) != 0 {
		return nil, specDiagnostic(source, kind, name, "is generic; generic semantic functions are unsupported")
	}

	wantParams, wantShape := 1, "func(E) bool"
	switch kind {
	case config.SemanticEq:
		wantParams, wantShape = 2, "func(E1, E2) bool"
	case config.SemanticCmp:
		wantParams, wantShape = 2, "func(E1, E2) int"
	}
	parameterCount := fieldListCount(declaration.Type.Params)
	resultCount := fieldListCount(declaration.Type.Results)
	if fieldListVariadic(declaration.Type.Params) || parameterCount != wantParams || resultCount != 1 {
		return nil, specDiagnostic(source, kind, name, "must have signature %s; found function with %d parameters and %d results", wantShape, parameterCount, resultCount)
	}
	wantResultName := resultNameForKind(kind)
	if err := validateSourceResultType(pkg.SourceFset, sourceFunction, wantResultName); err != nil {
		var sourceErr *sourceDiagnosticError
		if errors.As(err, &sourceErr) {
			return nil, sourceErr
		}
		return nil, specDiagnostic(source, kind, name, "could not read specialization function result type: %v", err)
	}
	params, err := specParams(pkg.SourceFset, file, sourceFunction)
	if err != nil {
		var sourceErr *sourceDiagnosticError
		if errors.As(err, &sourceErr) {
			return nil, sourceErr
		}
		return nil, specDiagnostic(source, kind, name, "could not read specialization function parameters: %v", err)
	}
	return &SpecFunc{Kind: kind, Name: name, Params: params}, nil
}

func resultNameForKind(kind config.SemanticKind) string {
	if kind == config.SemanticCmp {
		return "int"
	}
	return "bool"
}

func fieldListCount(fields *ast.FieldList) int {
	if fields == nil {
		return 0
	}
	count := 0
	for _, field := range fields.List {
		if len(field.Names) == 0 {
			count++
			continue
		}
		count += len(field.Names)
	}
	return count
}

func fieldListVariadic(fields *ast.FieldList) bool {
	if fields == nil {
		return false
	}
	for _, field := range fields.List {
		if _, ok := field.Type.(*ast.Ellipsis); ok {
			return true
		}
	}
	return false
}

func validateSourceResultType(fset *token.FileSet, declaration *loadpkg.SourceFuncDecl, want string) error {
	if declaration == nil || declaration.Decl == nil || declaration.Decl.Type == nil || fieldListCount(declaration.Decl.Type.Results) != 1 {
		return fmt.Errorf("specialization function declaration has no single source result type")
	}
	resultType := declaration.Decl.Type.Results.List[0].Type
	if identifier, ok := resultType.(*ast.Ident); ok && identifier.Name == want {
		return nil
	}
	return newSourceDiagnosticError(fset, resultType.Pos(), fmt.Sprintf("must spell its result type %s in the specialization function", want))
}

func canonicalSourcePath(path string) string {
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		if absolute, err := filepath.Abs(path); err == nil {
			path = absolute
		}
	}
	return filepath.Clean(path)
}

// specParams flattens the directive-file AST parameter fields according to
// Go's parameter count rules while retaining each field's source expression.
// The planning model intentionally does not retain semantic Go types.
func specParams(fset *token.FileSet, file *loadpkg.SourceFile, declaration *loadpkg.SourceFuncDecl) ([]SpecParam, error) {
	if fset == nil || file == nil || declaration == nil || declaration.Decl == nil || declaration.Decl.Type == nil || declaration.Decl.Type.Params == nil {
		return nil, fmt.Errorf("specialization function declaration has no parameter list")
	}
	imports := file.Imports
	params := make([]SpecParam, 0)
	for _, field := range declaration.Decl.Type.Params.List {
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		sourceType, err := sourceTypeFromSource(fset, file.Source, field.Type, imports)
		if err != nil {
			return nil, err
		}
		var pointerElement *SourceType
		pointerExpression := field.Type
		for {
			parenthesized, ok := pointerExpression.(*ast.ParenExpr)
			if !ok {
				break
			}
			pointerExpression = parenthesized.X
		}
		if star, ok := pointerExpression.(*ast.StarExpr); ok {
			pointee, err := sourceTypeFromSource(fset, file.Source, star.X, imports)
			if err != nil {
				return nil, err
			}
			pointerElement = &pointee
		}
		for index := 0; index < count; index++ {
			params = append(params, SpecParam{
				Source:         sourceType,
				PointerElement: pointerElement,
			})
		}
	}
	return params, nil
}

func specDiagnostic(source *loadpkg.Directive, kind config.SemanticKind, name, format string, args ...any) error {
	if source != nil {
		return source.Errorf("%s=%s %s", kind, name, fmt.Sprintf(format, args...))
	}
	return diagnostic("-%s=%s %s", kind, name, fmt.Sprintf(format, args...))
}

func makePlan(op config.Operation, funcs map[config.SemanticKind]*SpecFunc, byref config.ByRefMode) (*OperationPlan, bool, string) {
	spec := operationSpec(op)
	fn := funcs[spec.semantic]
	if fn == nil {
		return nil, false, fmt.Sprintf("operation %q requires %s", op, spec.semantic)
	}
	plan := &OperationPlan{Op: op, PublicName: operationGoName(op), Func: fn}
	param := fn.Params
	effective := func(index int, role string) (SourceType, bool, string) {
		if !byref.AppliesTo(index) {
			return param[index].Source, true, ""
		}
		if param[index].PointerElement == nil {
			return SourceType{}, false, fmt.Sprintf("operation %q requires %s=%s parameter %d (%s) to use explicit pointer syntax when by-reference mode applies", op, fn.Kind, fn.Name, index+1, role)
		}
		return *param[index].PointerElement, true, ""
	}
	switch op {
	case config.OperationContains, config.OperationIndex, config.OperationDelete:
		var ok bool
		var reason string
		plan.Elem1, ok, reason = effective(0, "slice element")
		if !ok {
			return nil, false, reason
		}
		plan.Arg1ByRef = byref.AppliesTo(0)
	case config.OperationCompact:
		var ok bool
		var reason string
		plan.Elem1, ok, reason = effective(0, "slice element")
		if !ok {
			return nil, false, reason
		}
		plan.Elem2, ok, reason = effective(1, "slice element")
		if !ok {
			return nil, false, reason
		}
		plan.Arg1ByRef, plan.Arg2ByRef = byref.AppliesTo(0), byref.AppliesTo(1)
		if !plan.Elem1.SameSpelling(plan.Elem2) {
			return nil, false, fmt.Sprintf("operation %q requires a homogeneous equality function", op)
		}
	case config.OperationEqual, config.OperationCompare:
		var ok bool
		var reason string
		plan.Elem1, ok, reason = effective(0, "slice element")
		if !ok {
			return nil, false, reason
		}
		plan.Elem2, ok, reason = effective(1, "slice element")
		if !ok {
			return nil, false, reason
		}
		plan.Arg1ByRef, plan.Arg2ByRef = byref.AppliesTo(0), byref.AppliesTo(1)
	case config.OperationMin, config.OperationMax, config.OperationSort, config.OperationSortStable, config.OperationIsSorted:
		var ok bool
		var reason string
		plan.Elem1, ok, reason = effective(0, "slice element")
		if !ok {
			return nil, false, reason
		}
		plan.Elem2, ok, reason = effective(1, "slice element")
		if !ok {
			return nil, false, reason
		}
		plan.Arg1ByRef, plan.Arg2ByRef = byref.AppliesTo(0), byref.AppliesTo(1)
		if !plan.Elem1.SameSpelling(plan.Elem2) {
			return nil, false, fmt.Sprintf("operation %q requires a homogeneous comparator", op)
		}
	case config.OperationBinarySearch:
		var ok bool
		var reason string
		plan.Elem1, ok, reason = effective(0, "slice element")
		if !ok {
			return nil, false, reason
		}
		plan.Arg1ByRef = byref.AppliesTo(0)
		// The second argument is an ordinary target, not a slice-element role.
		// Preserve its source expression literally even when the function role
		// has by-reference mode enabled.
		plan.Target = param[1].Source
	default:
		return nil, false, fmt.Sprintf("operation %q is not supported", op)
	}
	return plan, true, ""
}

type operationDetails struct {
	semantic config.SemanticKind
}

// generatedPredeclaredNames is the generator-wide set of predeclared names
// referenced unqualified by operation and sorting implementation source.
var generatedPredeclaredNames = []string{"bool", "clear", "false", "int", "iota", "len", "panic", "true", "uint", "uint64"}

// GeneratedPredeclaredIdentifiers returns the fixed generator-wide set. The
// operation argument is retained for source compatibility with earlier model
// callers; all generated operations reserve the same conservative set.
func GeneratedPredeclaredIdentifiers(_ config.Operation) []string {
	return append([]string(nil), generatedPredeclaredNames...)
}

func sourceImportNames(imported loadpkg.SourceImport) []string {
	if imported.Name == "" || imported.Name == "_" || imported.Name == "." {
		return nil
	}
	return []string{imported.Name}
}

func operationSpec(op config.Operation) operationDetails {
	for _, spec := range config.SupportedOperationSpecs() {
		if spec.Name == op {
			return operationDetails{semantic: spec.Semantic}
		}
	}
	return operationDetails{}
}

func operationGoName(op config.Operation) string {
	parts := strings.Split(string(op), "-")
	var builder strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		builder.WriteString(strings.ToUpper(part[:1]))
		builder.WriteString(part[1:])
	}
	return builder.String()
}

func diagnostic(format string, args ...any) error {
	return fmt.Errorf("monoslices: "+format, args...)
}

// ImportPlanner reserves exact source-type import bindings and allocates aliases
// for imports used by generated implementation code. Source types are rendered
// from their preserved spelling without changing this planning state.
type ImportPlanner struct {
	namespace        *Namespace
	generatorImports map[string]string
	aliases          map[string]string
}

// NewImportPlanner creates an import planner backed by the package namespace.
func NewImportPlanner(namespace *Namespace) *ImportPlanner {
	if namespace == nil {
		namespace = NewNamespace(nil)
	}
	return &ImportPlanner{
		namespace:        namespace,
		generatorImports: make(map[string]string),
	}
}

// GeneratedHelperName returns the package-private name used for one generated
// sorting helper. The separators make the directive and helper parts distinct.
func GeneratedHelperName(name, helper string) string {
	return "monoslices_" + name + "_" + helper + "_helper"
}

// AddGeneratorImport records one import used by generated implementation code
// under its preferred alias. Source-type imports are intentionally not
// added here: their exact aliases are already reserved in Namespace.
func (planner *ImportPlanner) AddGeneratorImport(path, preferred string) {
	if path == "" {
		return
	}
	if _, exists := planner.generatorImports[path]; !exists {
		planner.generatorImports[path] = preferred
	}
	planner.aliases = nil
}

// Add reserves symbolic names and every exact source-type alias/path binding.
// Source-type imports do not undergo package discovery or alias allocation.
func (planner *ImportPlanner) Add(typ SourceType, sources ...loadpkg.Directive) error {
	var source loadpkg.Directive
	if len(sources) != 0 {
		source = sources[0]
	}
	source = typ.sourceLocation(source)
	if err := planner.namespace.ReserveSourceTypePredeclared(typ.PredeclaredIdentifiers(), source); err != nil {
		return err
	}
	for _, binding := range typ.ImportBindings() {
		if err := planner.namespace.ReserveImportBinding(binding.Name, binding.Path, source); err != nil {
			return err
		}
	}
	planner.aliases = nil
	return nil
}

func (t SourceType) sourceLocation(fallback loadpkg.Directive) loadpkg.Directive {
	if t.sourceFile == "" || t.sourceLine == 0 {
		return fallback
	}
	fallback.Path = t.sourceFile
	fallback.Filename = filepath.Base(t.sourceFile)
	fallback.Line = t.sourceLine
	fallback.Column = t.sourceColumn
	return fallback
}

// Finalize assigns generator-owned aliases deterministically. A fixed source-type
// binding for the same path is reused rather than adding another alias.
func (planner *ImportPlanner) Finalize() {
	paths := make([]string, 0, len(planner.generatorImports))
	for path := range planner.generatorImports {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	planner.aliases = make(map[string]string, len(paths))
	for _, path := range paths {
		if fixed := planner.namespace.FixedImportAlias(path); fixed != "" {
			planner.aliases[path] = fixed
			continue
		}
		preferred := planner.generatorImports[path]
		planner.aliases[path] = planner.namespace.AllocateImportAlias(path, preferred)
	}
}

// Render returns typ's preserved source spelling. Planning must be completed
// explicitly with Add before Finalize; rendering never discovers imports,
// reserves bindings, finalizes aliases, or otherwise mutates planner state.
func (_ *ImportPlanner) Render(typ SourceType) string {
	return typ.Render()
}

// ImportAlias returns the chosen alias for a generator-owned path. If the path
// is fixed by a source-type binding, that deterministic alias is reusable too.
func (planner *ImportPlanner) ImportAlias(path string) string {
	if planner.aliases == nil {
		planner.Finalize()
	}
	if alias := planner.aliases[path]; alias != "" {
		return alias
	}
	return planner.namespace.FixedImportAlias(path)
}

// Imports returns fixed source-type bindings. Generator-owned imports are included
// only by AllImports.
func (planner *ImportPlanner) Imports() []Import {
	return planner.namespace.FixedImportBindings()
}

// AllImports returns fixed source-type bindings and generator-owned imports sorted
// deterministically by path and alias.
func (planner *ImportPlanner) AllImports() []Import {
	if planner.aliases == nil {
		planner.Finalize()
	}
	imports := planner.namespace.FixedImportBindings()
	fixedPaths := make(map[string]bool, len(imports))
	for _, imported := range imports {
		fixedPaths[imported.Path] = true
	}
	paths := make([]string, 0, len(planner.generatorImports))
	for path := range planner.generatorImports {
		if !fixedPaths[path] {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		imports = append(imports, Import{Path: path, Name: planner.aliases[path]})
	}
	sort.Slice(imports, func(i, j int) bool {
		if imports[i].Path != imports[j].Path {
			return imports[i].Path < imports[j].Path
		}
		return imports[i].Name < imports[j].Name
	})
	return imports
}

// Import is one deterministic generated import.
type Import struct {
	Path string
	Name string
}
