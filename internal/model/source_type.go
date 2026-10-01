package model

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zolstein/monoslices/internal/loadpkg"
)

// SourceType is a source-level type expression with imported package
// qualifiers bound to explicit source aliases. It intentionally does not model
// Go type semantics: the source expression is what the generated package will
// compile later.
type SourceType struct {
	text         string
	references   []SourceReference
	predeclared  []string
	sourceFile   string
	sourceLine   int
	sourceColumn int
}

// SourceReference identifies one imported qualifier in SourceType.text. Start
// and End are byte offsets relative to the source type text, with End
// exclusive. Name is the source-file binding and must be preserved when the
// expression is emitted into the aggregate file.
type SourceReference struct {
	Start      int
	End        int
	Name       string
	ImportPath string
}

// SourceImportBinding identifies a source import binding required by a source
// type expression. Aggregate rendering keeps this alias exactly as written by
// the specialization function's source file.
type SourceImportBinding struct {
	Name string
	Path string
}

// sourceDiagnosticError is a user-facing validation error tied to source
// syntax rather than to the directive that requested it. Its filename is
// package-local so diagnostics do not expose the loader's absolute paths.
type sourceDiagnosticError struct {
	Filename string
	Line     int
	Column   int
	Message  string
}

func (e *sourceDiagnosticError) Error() string {
	if e == nil {
		return "<nil>"
	}
	filename := filepath.Base(e.Filename)
	if filename == "." || filename == "" {
		filename = e.Filename
	}
	return fmt.Sprintf("%s:%d:%d: monoslices: %s", filename, e.Line, e.Column, e.Message)
}

func newSourceDiagnosticError(fset *token.FileSet, position token.Pos, message string) *sourceDiagnosticError {
	if fset == nil {
		return &sourceDiagnosticError{Message: message}
	}
	located := fset.Position(position)
	return &sourceDiagnosticError{
		Filename: filepath.Base(located.Filename),
		Line:     located.Line,
		Column:   located.Column,
		Message:  message,
	}
}

// String returns the source spelling of the type expression.
func (t SourceType) String() string {
	return t.text
}

// SameSpelling reports whether two source types have the same normalized
// source spelling. Formatting and redundant outer parentheses are ignored,
// while local identifiers and imported qualifier names remain significant.
// It is used when an operation requires one homogeneous element type in the
// generated source.
func (t SourceType) SameSpelling(other SourceType) bool {
	return normalizeSourceType(t.text) == normalizeSourceType(other.text)
}

// ImportBindings returns the source alias/path pairs referenced by the
// expression, in first-reference order. Aliases are part of this
// representation because aggregate source must preserve them.
func (t SourceType) ImportBindings() []SourceImportBinding {
	if len(t.references) == 0 {
		return nil
	}
	bindings := make([]SourceImportBinding, 0, len(t.references))
	seen := make(map[string]bool, len(t.references))
	for _, reference := range t.references {
		if reference.Name == "" || reference.ImportPath == "" {
			continue
		}
		key := reference.Name + "\x00" + reference.ImportPath
		if seen[key] {
			continue
		}
		seen[key] = true
		bindings = append(bindings, SourceImportBinding{Name: reference.Name, Path: reference.ImportPath})
	}
	return bindings
}

// PredeclaredIdentifiers returns the universe identifiers used unqualified by
// the expression. Selector components and field/method names are excluded.
func (t SourceType) PredeclaredIdentifiers() []string {
	return append([]string(nil), t.predeclared...)
}

// Render returns the preserved source spelling without consulting or changing
// any import-planning state. Source-type qualifiers are already bound to their
// source aliases and are never rewritten for aggregate generation.
func (t SourceType) Render() string {
	return t.text
}

// NewSourceType constructs a source type from an expression in file. fset
// must be the FileSet used to parse file.AST. Keeping the original source
// bytes makes String and Render preserve the expression's source spelling.
func NewSourceType(fset *token.FileSet, file *loadpkg.SourceFile, expression ast.Expr) (SourceType, error) {
	if file == nil {
		return SourceType{}, fmt.Errorf("monoslices: cannot construct source type without a source file")
	}
	return sourceTypeFromSource(fset, file.Source, expression, file.Imports)
}

// SourceTypeFromExpr is an explicit alias for NewSourceType useful to callers
// that construct symbolic types while walking an AST.
func SourceTypeFromExpr(fset *token.FileSet, file *loadpkg.SourceFile, expression ast.Expr) (SourceType, error) {
	return NewSourceType(fset, file, expression)
}

// sourceTypeFromSource is the lower-level constructor used when source file
// metadata is available separately from its parsed AST.
func sourceTypeFromSource(fset *token.FileSet, source []byte, expression ast.Expr, imports []loadpkg.SourceImport) (SourceType, error) {
	if fset == nil {
		return SourceType{}, fmt.Errorf("monoslices: cannot construct source type without a token file set")
	}
	if expression == nil {
		return SourceType{}, fmt.Errorf("monoslices: cannot construct source type from a nil expression")
	}
	file := fset.File(expression.Pos())
	if file == nil {
		return SourceType{}, fmt.Errorf("monoslices: source type expression is not in the supplied file set")
	}
	start := file.Offset(expression.Pos())
	end := file.Offset(expression.End())
	if start < 0 || end < start || end > len(source) {
		return SourceType{}, fmt.Errorf("monoslices: source type expression has invalid source range")
	}
	text := string(source[start:end])
	if text == "" {
		// This fallback is useful for callers that have an AST but not the
		// corresponding source bytes. The regular package path always retains
		// bytes, so this does not affect source-preserving generation.
		var formatted bytes.Buffer
		if err := format.Node(&formatted, fset, expression); err != nil {
			return SourceType{}, fmt.Errorf("monoslices: cannot format source type: %v", err)
		}
		text = formatted.String()
	}

	references, err := sourceReferences(fset, expression, start, imports)
	if err != nil {
		return SourceType{}, err
	}
	position := fset.Position(expression.Pos())
	return SourceType{
		text:         text,
		references:   references,
		predeclared:  sourcePredeclaredIdentifiers(expression, fset, references, start),
		sourceFile:   position.Filename,
		sourceLine:   position.Line,
		sourceColumn: position.Column,
	}, nil
}

func sourceReferences(fset *token.FileSet, expression ast.Expr, expressionStart int, imports []loadpkg.SourceImport) ([]SourceReference, error) {
	bindings := make(map[string][]loadpkg.SourceImport)
	for _, imported := range imports {
		if !imported.ExplicitAlias || imported.Name == "" || imported.Name == "_" || imported.Name == "." || imported.ImportPath == "" {
			continue
		}
		bindings[imported.Name] = append(bindings[imported.Name], imported)
	}
	var references []SourceReference
	var validationErr error
	ast.Inspect(expression, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || validationErr != nil {
			return validationErr == nil
		}
		identifier, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		candidates, ok := bindings[identifier.Name]
		if !ok {
			validationErr = newSourceDiagnosticError(
				fset,
				identifier.Pos(),
				fmt.Sprintf(
					"specialization function parameter type uses qualifier %q that is not bound by an explicit import alias; imported qualifiers used in specialization function parameter types must have explicit aliases",
					identifier.Name,
				),
			)
			return false
		}
		byPath := make(map[string]loadpkg.SourceImport)
		for _, candidate := range candidates {
			if _, exists := byPath[candidate.ImportPath]; !exists {
				byPath[candidate.ImportPath] = candidate
			}
		}
		if len(byPath) > 1 {
			conflicts := make([]loadpkg.SourceImport, 0, len(byPath))
			for _, candidate := range byPath {
				conflicts = append(conflicts, candidate)
			}
			sort.Slice(conflicts, func(i, j int) bool {
				if conflicts[i].ImportPath != conflicts[j].ImportPath {
					return conflicts[i].ImportPath < conflicts[j].ImportPath
				}
				if conflicts[i].Filename != conflicts[j].Filename {
					return conflicts[i].Filename < conflicts[j].Filename
				}
				return conflicts[i].Line < conflicts[j].Line
			})
			first, second := conflicts[0], conflicts[1]
			validationErr = newSourceDiagnosticError(
				fset,
				identifier.Pos(),
				fmt.Sprintf(
					"specialization function parameter type uses explicit import alias %q with multiple explicit import bindings for distinct paths %q at %s:%d:%d and %q at %s:%d:%d; it cannot be reproduced unambiguously",
					identifier.Name,
					first.ImportPath, filepath.Base(first.Filename), first.Line, first.Column,
					second.ImportPath, filepath.Base(second.Filename), second.Line, second.Column,
				),
			)
			return false
		}
		path := candidates[0].ImportPath
		file := fset.File(identifier.Pos())
		if file == nil {
			return true
		}
		start := file.Offset(identifier.Pos()) - expressionStart
		end := file.Offset(identifier.End()) - expressionStart
		if start >= 0 && end > start {
			references = append(references, SourceReference{Start: start, End: end, Name: identifier.Name, ImportPath: path})
		}
		return true
	})
	if validationErr != nil {
		return nil, validationErr
	}
	sort.Slice(references, func(i, j int) bool {
		if references[i].Start != references[j].Start {
			return references[i].Start < references[j].Start
		}
		return references[i].End < references[j].End
	})
	return references, nil
}

func sourcePredeclaredIdentifiers(expression ast.Expr, fset *token.FileSet, references []SourceReference, expressionStart int) []string {
	if expression == nil || fset == nil {
		return nil
	}
	excluded := make(map[token.Pos]bool)
	for _, reference := range references {
		if reference.Start < 0 {
			continue
		}
		file := fset.File(expression.Pos())
		if file == nil {
			continue
		}
		excluded[file.Pos(expressionStart+reference.Start)] = true
	}
	ast.Inspect(expression, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.Field:
			for _, name := range node.Names {
				excluded[name.Pos()] = true
			}
		case *ast.SelectorExpr:
			if node.Sel != nil {
				excluded[node.Sel.Pos()] = true
			}
		}
		return true
	})
	seen := make(map[string]bool)
	var result []string
	ast.Inspect(expression, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if !ok || identifier.Name == "" || excluded[identifier.Pos()] || types.Universe.Lookup(identifier.Name) == nil || seen[identifier.Name] {
			return true
		}
		seen[identifier.Name] = true
		result = append(result, identifier.Name)
		return true
	})
	return result
}

func normalizeSourceType(text string) string {
	expression, err := parser.ParseExpr(text)
	if err != nil {
		return strings.TrimSpace(text)
	}
	for {
		parenthesized, ok := expression.(*ast.ParenExpr)
		if !ok {
			break
		}
		expression = parenthesized.X
	}
	var formatted bytes.Buffer
	if err := format.Node(&formatted, token.NewFileSet(), expression); err != nil {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(formatted.String())
}
