// Package gen emits one deterministic aggregate source file of statically bound
// monoslices operations.
package gen

import (
	"bytes"
	_ "embed"
	"fmt"
	"go/format"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/zolstein/monoslices/internal/config"
	"github.com/zolstein/monoslices/internal/loadpkg"
	"github.com/zolstein/monoslices/internal/model"
)

// GeneratePackage renders all directive plans into one aggregate source file.
// All directives share one header and import namespace; specialization function
// calls remain statically bound in each operation body.
func GeneratePackage(plan *model.PackagePlan) ([]byte, error) {
	if plan == nil || plan.Package == nil {
		return nil, fmt.Errorf("monoslices: cannot generate source without a package plan")
	}
	if plan.Types == nil {
		return nil, fmt.Errorf("monoslices: package plan has no import planner")
	}

	// Copy both levels before sorting so generation is deterministic even when
	// a caller assembled a plan rather than receiving the loader's source order.
	directives := make([]model.DirectivePlan, len(plan.Directives))
	for index := range plan.Directives {
		directives[index] = plan.Directives[index]
		directives[index].Operations = append([]model.OperationPlan(nil), plan.Directives[index].Operations...)
	}
	sort.SliceStable(directives, func(i, j int) bool {
		return directiveLess(directives[i].Source, directives[j].Source)
	})

	operations := make([]*model.OperationPlan, 0)
	for directiveIndex := range directives {
		directive := &directives[directiveIndex]
		sort.SliceStable(directive.Operations, func(i, j int) bool {
			return operationRank(directive.Operations[i].Op) < operationRank(directive.Operations[j].Op)
		})
		for operationIndex := range directive.Operations {
			operation := &directive.Operations[operationIndex]
			// Symbolic types are registered during BuildPackage's explicit
			// planning phase. Rendering only consumes that finalized plan.
			operations = append(operations, operation)
		}
	}
	plan.Types.Finalize()

	groups := make(map[string]model.SortGroupPlan, len(plan.SortGroups))
	for _, group := range plan.SortGroups {
		groups[sortDirectiveKey(group.SourcePath, group.SourceOffset)] = group
	}

	typesByPlan := make(map[*model.OperationPlan]renderedTypes, len(operations))
	for _, operation := range operations {
		typesByPlan[operation] = renderedTypes{
			elem1:  plan.Types.Render(operation.Elem1),
			elem2:  plan.Types.Render(operation.Elem2),
			target: plan.Types.Render(operation.Target),
		}
	}

	var source bytes.Buffer
	writeHeader(&source)
	source.WriteString("package ")
	source.WriteString(plan.Package.Name)
	source.WriteString("\n\n")
	writeImports(&source, plan.Types.AllImports())

	for directiveIndex := range directives {
		if directiveIndex != 0 {
			source.WriteByte('\n')
		}
		directive := &directives[directiveIndex]
		filename := directive.Source.Filename
		if filename == "" {
			filename = filepath.Base(directive.Source.Path)
		}
		fmt.Fprintf(&source, "// monoslices: %s %s\n\n", filename, config.FormatDirective(directive.Config))
		group, hasGroup := groups[sortDirectiveKey(directive.Source.Path, directive.Source.Offset)]
		for operationIndex := range directive.Operations {
			operation := &directive.Operations[operationIndex]
			var err error
			if operation.Op == config.OperationSort || operation.Op == config.OperationSortStable {
				if !hasGroup {
					return nil, fmt.Errorf("monoslices: missing sorting group for %s", operation.PublicName)
				}
				if err := emitSortGroupEntry(&source, operation, group, typesByPlan[operation].elem1, plan.Types.ImportAlias("math/bits")); err != nil {
					return nil, err
				}
			} else {
				err = emitOperation(&source, operation, typesByPlan[operation])
			}
			if err != nil {
				return nil, err
			}
			if operationIndex+1 < len(directive.Operations) {
				source.WriteByte('\n')
			}
		}
	}

	if len(plan.SortGroups) > 0 {
		source.WriteByte('\n')
		for index, directive := range directives {
			group, ok := groups[sortDirectiveKey(directive.Source.Path, directive.Source.Offset)]
			if !ok {
				continue
			}
			if index != 0 {
				source.WriteByte('\n')
			}
			typ := plan.Types.Render(group.Elem)
			if err := emitSortGroup(&source, group, plan.SortSupport, typ, plan.Types.ImportAlias("math/bits")); err != nil {
				return nil, err
			}
		}
		if plan.SortSupport != nil {
			source.WriteByte('\n')
			if err := emitSortSupport(&source, *plan.SortSupport, plan.Types.ImportAlias("math/bits")); err != nil {
				return nil, err
			}
		}
	}

	formatted, err := format.Source(source.Bytes())
	if err != nil {
		return nil, fmt.Errorf("monoslices: format generated source: %w", err)
	}
	return formatted, nil
}

type renderedTypes struct {
	elem1  string
	elem2  string
	target string
}

// localNames allocates identifiers for one generated function scope. The
// The selected function is package-scoped, so every generated local that has
// the same preferred spelling must be changed before the function is rendered.
type localNames struct {
	names map[string]string
}

func newLocalNames(funcName string, preferred ...string) localNames {
	used := map[string]bool{}
	if funcName != "" {
		used[funcName] = true
	}
	names := make(map[string]string, len(preferred))
	for _, name := range preferred {
		if _, exists := names[name]; exists {
			continue
		}
		candidate := name
		for used[candidate] {
			candidate += "_"
		}
		used[candidate] = true
		names[name] = candidate
	}
	return localNames{names: names}
}

func (names localNames) Name(preferred string) string {
	if name, ok := names.names[preferred]; ok {
		return name
	}
	return preferred
}

func operationLocalNames(plan *model.OperationPlan) localNames {
	preferred := []string{"s"}
	switch plan.Op {
	case config.OperationContains, config.OperationIndex:
		preferred = append(preferred, "i")
	case config.OperationDelete:
		preferred = append(preferred, "i", "index", "j", "v")
	case config.OperationCompact:
		preferred = append(preferred, "k", "s2", "k2")
	case config.OperationEqual:
		preferred = append(preferred, "a", "b", "i")
	case config.OperationCompare:
		preferred = append(preferred, "a", "b", "i", "c")
	case config.OperationMin, config.OperationMax:
		preferred = append(preferred, "i")
		if plan.Arg1ByRef || plan.Arg2ByRef {
			preferred = append(preferred, "best")
		} else {
			preferred = append(preferred, "m")
		}
	case config.OperationIsSorted:
		preferred = append(preferred, "i")
	case config.OperationBinarySearch:
		preferred = append(preferred, "target", "n", "i", "j", "h")
	}
	return newLocalNames(plan.Func.Name, preferred...)
}

const copyrightHeader = `
Adapted from:
  Go 1.24.0, src/slices/slices.go
  Go 1.24.0, src/slices/sort.go
  Go 1.24.0, src/slices/zsortanyfunc.go

Copyright 2026 Zach Olstein. All rights reserved.
Copyright 2023 The Go Authors. All rights reserved.
Copyright 2022 The Go Authors. All rights reserved.
Copyright 2021 The Go Authors. All rights reserved.
`

//go:embed go_bsd_license.txt
var goBSDLicense string

func writeHeader(source *bytes.Buffer) {
	writeCommentLine(source, loadpkg.GeneratedOwnershipComment)
	writeCommentBlock(source, copyrightHeader)
	writeCommentBlock(source, strings.TrimSuffix(goBSDLicense, "\n"))
}

func writeImports(source *bytes.Buffer, imports []model.Import) {
	if len(imports) == 0 {
		return
	}
	source.WriteString("import (\n")
	for _, imported := range imports {
		fmt.Fprintf(source, "\t%s %s\n", imported.Name, strconv.Quote(imported.Path))
	}
	source.WriteString(")\n\n")
}

func sortDirectiveKey(path string, offset int) string {
	return path + "\x00" + strconv.Itoa(offset)
}

func directiveLess(left, right loadpkg.Directive) bool {
	leftName, rightName := left.Filename, right.Filename
	if leftName == "" {
		leftName = filepath.Base(left.Path)
	}
	if rightName == "" {
		rightName = filepath.Base(right.Path)
	}
	if leftName != rightName {
		return leftName < rightName
	}
	if left.Line != right.Line {
		return left.Line < right.Line
	}
	if left.Column != right.Column {
		return left.Column < right.Column
	}
	if left.Offset != right.Offset {
		return left.Offset < right.Offset
	}
	return left.Path < right.Path
}

func operationRank(operation config.Operation) int {
	for index, supported := range config.SupportedOperations() {
		if operation == supported {
			return index
		}
	}
	return len(config.SupportedOperations())
}

func writeCommentBlock(source *bytes.Buffer, text string) {
	for line := range strings.SplitSeq(text, "\n") {
		writeCommentLine(source, line)
	}
}

func writeCommentLine(source *bytes.Buffer, line string) {
	source.WriteString("//")
	if line != "" {
		source.WriteByte(' ')
		source.WriteString(line)
	}
	source.WriteByte('\n')
}

func emitOperation(destination *bytes.Buffer, plan *model.OperationPlan, types renderedTypes) error {
	if plan.Func == nil {
		return fmt.Errorf("monoslices: operation %q has no specialization function", plan.Op)
	}
	data := newOperationTemplateData(plan, types)
	templateName, ok := operationTemplateNames[plan.Op]
	if !ok {
		return fmt.Errorf("monoslices: operation %q is not implemented by this emitter", plan.Op)
	}
	return executeFunctionTemplate(destination, templateName, data)
}

func byrefNote(plan *model.OperationPlan) string {
	if !plan.Arg1ByRef && !plan.Arg2ByRef {
		return ""
	}
	if plan.Op == config.OperationBinarySearch {
		return fmt.Sprintf(" The specialization function %q receives an ordinary pointer to the slice element; the target is passed as declared.", plan.Func.Name)
	}
	return fmt.Sprintf(" The specialization function %q receives selected slice elements through pointers to live slice slots; treat them as read-only unless the function intentionally accounts for their aliasing.", plan.Func.Name)
}

// call and call2 are the only places that add by-reference address operators.
// Their operands are always indexing expressions (or the ordinary target),
// which makes it impossible for an operation template to accidentally pass a
// pointer to a copied local value.
func call(funcName, expression string, byref bool) string {
	if byref {
		return funcName + "(&" + expression + ")"
	}
	return funcName + "(" + expression + ")"
}

func call2(funcName, first, second string, firstByRef, secondByRef bool) string {
	return funcName + "(" + operand(first, firstByRef) + ", " + operand(second, secondByRef) + ")"
}

func operand(expression string, byref bool) string {
	if byref {
		return "&" + expression
	}
	return expression
}
