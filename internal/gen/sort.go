package gen

import (
	"bytes"
	"fmt"
	"go/scanner"
	"go/token"
	"strings"
	"text/template"

	"github.com/zolstein/monoslices/internal/config"
	"github.com/zolstein/monoslices/internal/model"
)

// The sorting templates in this file are adapted from the Go 1.24.0 standard
// library sources src/slices/sort.go and src/slices/zsortanyfunc.go. The
// aggregate generator keeps each comparator-specific helper graph statically
// bound by removing cmp parameters and substituting the selected comparator
// expression. The generated-file header carries the Go Authors BSD license
// attribution.

type sortEmitter struct {
	source    *bytes.Buffer
	name      string
	cmpName   string
	arg1ByRef bool
	arg2ByRef bool
	typ       string
	bits      string
	helpers   map[string]string
	support   map[string]string
	emitted   map[string]bool
	err       error
}

type sortTemplateData struct {
	Type       string
	Name       string
	CmpName    string
	Arg1ByRef  bool
	Arg2ByRef  bool
	Bits       string
	Helpers    map[string]string
	Support    map[string]string
	LocalNames map[string]string
}

func sortHelper(data sortTemplateData, base string) string {
	if data.Helpers != nil {
		if name, ok := data.Helpers[base]; ok {
			return name
		}
	}
	if data.Support != nil {
		if name, ok := data.Support[base]; ok {
			return name
		}
	}
	return model.GeneratedHelperName(data.Name, base)
}

func sortLocal(data sortTemplateData, name string) (string, error) {
	local, ok := data.LocalNames[name]
	if !ok {
		return "", fmt.Errorf("sorting template local %q was not allocated", name)
	}
	return local, nil
}

func sortCompare(data sortTemplateData, left, right string) (string, error) {
	var err error
	left, err = sortExpression(data, left)
	if err != nil {
		return "", err
	}
	right, err = sortExpression(data, right)
	if err != nil {
		return "", err
	}
	return call2(data.CmpName, left, right, data.Arg1ByRef, data.Arg2ByRef), nil
}

func sortExpression(data sortTemplateData, expression string) (string, error) {
	fileSet := token.NewFileSet()
	file := fileSet.AddFile("sorting operand", fileSet.Base(), len(expression))
	var scan scanner.Scanner
	scan.Init(file, []byte(expression), nil, 0)
	type replacement struct {
		start int
		end   int
		text  string
	}
	replacements := make([]replacement, 0, 2)
	for {
		position, tokenType, literal := scan.Scan()
		if tokenType == token.EOF {
			break
		}
		if tokenType != token.IDENT {
			continue
		}
		local, err := sortLocal(data, literal)
		if err != nil {
			return "", err
		}
		start := file.Offset(position)
		replacements = append(replacements, replacement{start: start, end: start + len(literal), text: local})
	}
	if len(replacements) == 0 {
		return expression, nil
	}
	var result strings.Builder
	result.Grow(len(expression))
	last := 0
	for _, replacement := range replacements {
		result.WriteString(expression[last:replacement.start])
		result.WriteString(replacement.text)
		last = replacement.end
	}
	result.WriteString(expression[last:])
	return result.String(), nil
}

var sortTemplates = template.Must(template.New("sort").Funcs(template.FuncMap{
	"helper":  sortHelper,
	"compare": sortCompare,
	"local":   sortLocal,
}).Parse(sortTemplateText))

// sortTemplateLocals is the declaration/use inventory for each sorting helper
// scope. Keeping it alongside the templates makes adding a local require an
// explicit allocation entry instead of relying on a post-render rewrite.
var sortTemplateLocals = map[string][]string{
	"InsertionSort":        {"data", "a", "b", "i", "j"},
	"SiftDown":             {"data", "lo", "hi", "first", "root", "child"},
	"HeapSort":             {"data", "a", "b", "first", "lo", "hi", "i"},
	"PDQSort":              {"data", "a", "b", "limit", "maxInsertion", "wasBalanced", "wasPartitioned", "length", "pivot", "hint", "mid", "alreadyPartitioned", "leftLen", "rightLen", "balanceThreshold"},
	"Partition":            {"data", "a", "b", "pivot", "i", "j"},
	"PartitionEqual":       {"data", "a", "b", "pivot", "i", "j"},
	"PartialInsertionSort": {"data", "a", "b", "maxSteps", "shortestShifting", "i", "j"},
	"BreakPatterns":        {"data", "a", "b", "length", "random", "modulus", "idx", "other"},
	"ChoosePivot":          {"data", "a", "b", "shortestNinther", "maxSwaps", "l", "swaps", "i", "j", "k"},
	"Order2":               {"data", "a", "b", "swaps"},
	"Median":               {"data", "a", "b", "c", "swaps"},
	"MedianAdjacent":       {"data", "a", "swaps"},
	"ReverseRange":         {"data", "a", "b", "i", "j"},
	"Support":              {"r", "length"},
	"Stable":               {"data", "n", "blockSize", "a", "b", "m"},
	"SymMerge":             {"data", "a", "m", "b", "i", "j", "h", "k", "mid", "n", "start", "r", "p", "c", "end"},
	"Rotate":               {"data", "a", "m", "b", "i", "j"},
	"SwapRange":            {"data", "a", "b", "n", "i"},
}

const sortTemplateText = `
{{define "InsertionSort"}}
func {{helper . "InsertionSort"}}({{local . "data"}} []{{.Type}}, {{local . "a"}}, {{local . "b"}} int) {
	for {{local . "i"}} := {{local . "a"}} + 1; {{local . "i"}} < {{local . "b"}}; {{local . "i"}}++ {
		for {{local . "j"}} := {{local . "i"}}; {{local . "j"}} > {{local . "a"}} && ({{compare . "data[j]" "data[j-1]"}} < 0); {{local . "j"}}-- {
			{{local . "data"}}[{{local . "j"}}], {{local . "data"}}[{{local . "j"}}-1] = {{local . "data"}}[{{local . "j"}}-1], {{local . "data"}}[{{local . "j"}}]
		}
	}
}
{{end}}

{{define "SiftDown"}}
func {{helper . "SiftDown"}}({{local . "data"}} []{{.Type}}, {{local . "lo"}}, {{local . "hi"}}, {{local . "first"}} int) {
	{{local . "root"}} := {{local . "lo"}}
	for {
		{{local . "child"}} := 2*{{local . "root"}} + 1
		if {{local . "child"}} >= {{local . "hi"}} {
			break
		}
		if {{local . "child"}}+1 < {{local . "hi"}} && ({{compare . "data[first+child]" "data[first+child+1]"}} < 0) {
			{{local . "child"}}++
		}
		if !({{compare . "data[first+root]" "data[first+child]"}} < 0) {
			return
		}
		{{local . "data"}}[{{local . "first"}}+{{local . "root"}}], {{local . "data"}}[{{local . "first"}}+{{local . "child"}}] = {{local . "data"}}[{{local . "first"}}+{{local . "child"}}], {{local . "data"}}[{{local . "first"}}+{{local . "root"}}]
		{{local . "root"}} = {{local . "child"}}
	}
}
{{end}}

{{define "HeapSort"}}
func {{helper . "HeapSort"}}({{local . "data"}} []{{.Type}}, {{local . "a"}}, {{local . "b"}} int) {
	{{local . "first"}} := {{local . "a"}}
	{{local . "lo"}} := 0
	{{local . "hi"}} := {{local . "b"}} - {{local . "a"}}
	for {{local . "i"}} := ({{local . "hi"}} - 1) / 2; {{local . "i"}} >= 0; {{local . "i"}}-- {
		{{helper . "SiftDown"}}({{local . "data"}}, {{local . "i"}}, {{local . "hi"}}, {{local . "first"}})
	}
	for {{local . "i"}} := {{local . "hi"}} - 1; {{local . "i"}} >= 0; {{local . "i"}}-- {
		{{local . "data"}}[{{local . "first"}}], {{local . "data"}}[{{local . "first"}}+{{local . "i"}}] = {{local . "data"}}[{{local . "first"}}+{{local . "i"}}], {{local . "data"}}[{{local . "first"}}]
		{{helper . "SiftDown"}}({{local . "data"}}, {{local . "lo"}}, {{local . "i"}}, {{local . "first"}})
	}
}
{{end}}

{{define "PDQSort"}}
func {{helper . "PDQSort"}}({{local . "data"}} []{{.Type}}, {{local . "a"}}, {{local . "b"}}, {{local . "limit"}} int) {
	const {{local . "maxInsertion"}} = 12
	var (
		{{local . "wasBalanced"}} = true
		{{local . "wasPartitioned"}} = true
	)
	for {
		{{local . "length"}} := {{local . "b"}} - {{local . "a"}}
		if {{local . "length"}} <= {{local . "maxInsertion"}} {
			{{helper . "InsertionSort"}}({{local . "data"}}, {{local . "a"}}, {{local . "b"}})
			return
		}
		if {{local . "limit"}} == 0 {
			{{helper . "HeapSort"}}({{local . "data"}}, {{local . "a"}}, {{local . "b"}})
			return
		}
		if !{{local . "wasBalanced"}} {
			{{helper . "BreakPatterns"}}({{local . "data"}}, {{local . "a"}}, {{local . "b"}})
			{{local . "limit"}}--
		}
		{{local . "pivot"}}, {{local . "hint"}} := {{helper . "ChoosePivot"}}({{local . "data"}}, {{local . "a"}}, {{local . "b"}})
		if {{local . "hint"}} == {{helper . "DecreasingHint"}} {
			{{helper . "ReverseRange"}}({{local . "data"}}, {{local . "a"}}, {{local . "b"}})
			{{local . "pivot"}} = ({{local . "b"}} - 1) - ({{local . "pivot"}} - {{local . "a"}})
			{{local . "hint"}} = {{helper . "IncreasingHint"}}
		}
		if {{local . "wasBalanced"}} && {{local . "wasPartitioned"}} && {{local . "hint"}} == {{helper . "IncreasingHint"}} {
			if {{helper . "PartialInsertionSort"}}({{local . "data"}}, {{local . "a"}}, {{local . "b"}}) {
				return
			}
		}
		if {{local . "a"}} > 0 && !({{compare . "data[a-1]" "data[pivot]"}} < 0) {
			{{local . "mid"}} := {{helper . "PartitionEqual"}}({{local . "data"}}, {{local . "a"}}, {{local . "b"}}, {{local . "pivot"}})
			{{local . "a"}} = {{local . "mid"}}
			continue
		}
		{{local . "mid"}}, {{local . "alreadyPartitioned"}} := {{helper . "Partition"}}({{local . "data"}}, {{local . "a"}}, {{local . "b"}}, {{local . "pivot"}})
		{{local . "wasPartitioned"}} = {{local . "alreadyPartitioned"}}
		{{local . "leftLen"}}, {{local . "rightLen"}} := {{local . "mid"}}-{{local . "a"}}, {{local . "b"}}-{{local . "mid"}}
		{{local . "balanceThreshold"}} := {{local . "length"}} / 8
		if {{local . "leftLen"}} < {{local . "rightLen"}} {
			{{local . "wasBalanced"}} = {{local . "leftLen"}} >= {{local . "balanceThreshold"}}
			{{helper . "PDQSort"}}({{local . "data"}}, {{local . "a"}}, {{local . "mid"}}, {{local . "limit"}})
			{{local . "a"}} = {{local . "mid"}} + 1
		} else {
			{{local . "wasBalanced"}} = {{local . "rightLen"}} >= {{local . "balanceThreshold"}}
			{{helper . "PDQSort"}}({{local . "data"}}, {{local . "mid"}}+1, {{local . "b"}}, {{local . "limit"}})
			{{local . "b"}} = {{local . "mid"}}
		}
	}
}
{{end}}

{{define "Partition"}}
func {{helper . "Partition"}}({{local . "data"}} []{{.Type}}, {{local . "a"}}, {{local . "b"}}, {{local . "pivot"}} int) (int, bool) {
	{{local . "data"}}[{{local . "a"}}], {{local . "data"}}[{{local . "pivot"}}] = {{local . "data"}}[{{local . "pivot"}}], {{local . "data"}}[{{local . "a"}}]
	{{local . "i"}}, {{local . "j"}} := {{local . "a"}}+1, {{local . "b"}}-1
	for {{local . "i"}} <= {{local . "j"}} && ({{compare . "data[i]" "data[a]"}} < 0) {
		{{local . "i"}}++
	}
	for {{local . "i"}} <= {{local . "j"}} && !({{compare . "data[j]" "data[a]"}} < 0) {
		{{local . "j"}}--
	}
	if {{local . "i"}} > {{local . "j"}} {
		{{local . "data"}}[{{local . "j"}}], {{local . "data"}}[{{local . "a"}}] = {{local . "data"}}[{{local . "a"}}], {{local . "data"}}[{{local . "j"}}]
		return {{local . "j"}}, true
	}
	{{local . "data"}}[{{local . "i"}}], {{local . "data"}}[{{local . "j"}}] = {{local . "data"}}[{{local . "j"}}], {{local . "data"}}[{{local . "i"}}]
	{{local . "i"}}++
	{{local . "j"}}--
	for {
		for {{local . "i"}} <= {{local . "j"}} && ({{compare . "data[i]" "data[a]"}} < 0) {
			{{local . "i"}}++
		}
		for {{local . "i"}} <= {{local . "j"}} && !({{compare . "data[j]" "data[a]"}} < 0) {
			{{local . "j"}}--
		}
		if {{local . "i"}} > {{local . "j"}} {
			break
		}
		{{local . "data"}}[{{local . "i"}}], {{local . "data"}}[{{local . "j"}}] = {{local . "data"}}[{{local . "j"}}], {{local . "data"}}[{{local . "i"}}]
		{{local . "i"}}++
		{{local . "j"}}--
	}
	{{local . "data"}}[{{local . "j"}}], {{local . "data"}}[{{local . "a"}}] = {{local . "data"}}[{{local . "a"}}], {{local . "data"}}[{{local . "j"}}]
	return {{local . "j"}}, false
}
{{end}}

{{define "PartitionEqual"}}
func {{helper . "PartitionEqual"}}({{local . "data"}} []{{.Type}}, {{local . "a"}}, {{local . "b"}}, {{local . "pivot"}} int) int {
	{{local . "data"}}[{{local . "a"}}], {{local . "data"}}[{{local . "pivot"}}] = {{local . "data"}}[{{local . "pivot"}}], {{local . "data"}}[{{local . "a"}}]
	{{local . "i"}}, {{local . "j"}} := {{local . "a"}}+1, {{local . "b"}}-1
	for {
		for {{local . "i"}} <= {{local . "j"}} && !({{compare . "data[a]" "data[i]"}} < 0) {
			{{local . "i"}}++
		}
		for {{local . "i"}} <= {{local . "j"}} && ({{compare . "data[a]" "data[j]"}} < 0) {
			{{local . "j"}}--
		}
		if {{local . "i"}} > {{local . "j"}} {
			break
		}
		{{local . "data"}}[{{local . "i"}}], {{local . "data"}}[{{local . "j"}}] = {{local . "data"}}[{{local . "j"}}], {{local . "data"}}[{{local . "i"}}]
		{{local . "i"}}++
		{{local . "j"}}--
	}
	return {{local . "i"}}
}
{{end}}

{{define "PartialInsertionSort"}}
func {{helper . "PartialInsertionSort"}}({{local . "data"}} []{{.Type}}, {{local . "a"}}, {{local . "b"}} int) bool {
	const (
		{{local . "maxSteps"}} = 5
		{{local . "shortestShifting"}} = 50
	)
	{{local . "i"}} := {{local . "a"}} + 1
	for {{local . "j"}} := 0; {{local . "j"}} < {{local . "maxSteps"}}; {{local . "j"}}++ {
		for {{local . "i"}} < {{local . "b"}} && !({{compare . "data[i]" "data[i-1]"}} < 0) {
			{{local . "i"}}++
		}
		if {{local . "i"}} == {{local . "b"}} {
			return true
		}
		if {{local . "b"}}-{{local . "a"}} < {{local . "shortestShifting"}} {
			return false
		}
		{{local . "data"}}[{{local . "i"}}], {{local . "data"}}[{{local . "i"}}-1] = {{local . "data"}}[{{local . "i"}}-1], {{local . "data"}}[{{local . "i"}}]
		if {{local . "i"}}-{{local . "a"}} >= 2 {
			for {{local . "j"}} := {{local . "i"}} - 1; {{local . "j"}} >= 1; {{local . "j"}}-- {
				if !({{compare . "data[j]" "data[j-1]"}} < 0) {
					break
				}
				{{local . "data"}}[{{local . "j"}}], {{local . "data"}}[{{local . "j"}}-1] = {{local . "data"}}[{{local . "j"}}-1], {{local . "data"}}[{{local . "j"}}]
			}
		}
		if {{local . "b"}}-{{local . "i"}} >= 2 {
			for {{local . "j"}} := {{local . "i"}} + 1; {{local . "j"}} < {{local . "b"}}; {{local . "j"}}++ {
				if !({{compare . "data[j]" "data[j-1]"}} < 0) {
					break
				}
				{{local . "data"}}[{{local . "j"}}], {{local . "data"}}[{{local . "j"}}-1] = {{local . "data"}}[{{local . "j"}}-1], {{local . "data"}}[{{local . "j"}}]
			}
		}
	}
	return false
}
{{end}}

{{define "BreakPatterns"}}
func {{helper . "BreakPatterns"}}({{local . "data"}} []{{.Type}}, {{local . "a"}}, {{local . "b"}} int) {
	{{local . "length"}} := {{local . "b"}} - {{local . "a"}}
	if {{local . "length"}} >= 8 {
		{{local . "random"}} := {{helper . "Xorshift"}}({{local . "length"}})
		{{local . "modulus"}} := {{helper . "NextPowerOfTwo"}}({{local . "length"}})
		for {{local . "idx"}} := {{local . "a"}} + ({{local . "length"}}/4)*2 - 1; {{local . "idx"}} <= {{local . "a"}}+({{local . "length"}}/4)*2+1; {{local . "idx"}}++ {
			{{local . "other"}} := int(uint({{local . "random"}}.Next()) & ({{local . "modulus"}} - 1))
			if {{local . "other"}} >= {{local . "length"}} {
				{{local . "other"}} -= {{local . "length"}}
			}
			{{local . "data"}}[{{local . "idx"}}], {{local . "data"}}[{{local . "a"}}+{{local . "other"}}] = {{local . "data"}}[{{local . "a"}}+{{local . "other"}}], {{local . "data"}}[{{local . "idx"}}]
		}
	}
}
{{end}}

{{define "ChoosePivot"}}
func {{helper . "ChoosePivot"}}({{local . "data"}} []{{.Type}}, {{local . "a"}}, {{local . "b"}} int) (int, {{helper . "SortedHint"}}) {
	const (
		{{local . "shortestNinther"}} = 50
		{{local . "maxSwaps"}} = 4 * 3
	)
	{{local . "l"}} := {{local . "b"}} - {{local . "a"}}
	var (
		{{local . "swaps"}} int
		{{local . "i"}} = {{local . "a"}} + {{local . "l"}}/4*1
		{{local . "j"}} = {{local . "a"}} + {{local . "l"}}/4*2
		{{local . "k"}} = {{local . "a"}} + {{local . "l"}}/4*3
	)
	if {{local . "l"}} >= 8 {
		if {{local . "l"}} >= {{local . "shortestNinther"}} {
			{{local . "i"}} = {{helper . "MedianAdjacent"}}({{local . "data"}}, {{local . "i"}}, &{{local . "swaps"}})
			{{local . "j"}} = {{helper . "MedianAdjacent"}}({{local . "data"}}, {{local . "j"}}, &{{local . "swaps"}})
			{{local . "k"}} = {{helper . "MedianAdjacent"}}({{local . "data"}}, {{local . "k"}}, &{{local . "swaps"}})
		}
		{{local . "j"}} = {{helper . "Median"}}({{local . "data"}}, {{local . "i"}}, {{local . "j"}}, {{local . "k"}}, &{{local . "swaps"}})
	}
	switch {{local . "swaps"}} {
	case 0:
		return {{local . "j"}}, {{helper . "IncreasingHint"}}
	case {{local . "maxSwaps"}}:
		return {{local . "j"}}, {{helper . "DecreasingHint"}}
	default:
		return {{local . "j"}}, {{helper . "UnknownHint"}}
	}
}
{{end}}

{{define "Order2"}}
func {{helper . "Order2"}}({{local . "data"}} []{{.Type}}, {{local . "a"}}, {{local . "b"}} int, {{local . "swaps"}} *int) (int, int) {
	if {{compare . "data[b]" "data[a]"}} < 0 {
		*{{local . "swaps"}}++
		return {{local . "b"}}, {{local . "a"}}
	}
	return {{local . "a"}}, {{local . "b"}}
}
{{end}}

{{define "Median"}}
func {{helper . "Median"}}({{local . "data"}} []{{.Type}}, {{local . "a"}}, {{local . "b"}}, {{local . "c"}} int, {{local . "swaps"}} *int) int {
	{{local . "a"}}, {{local . "b"}} = {{helper . "Order2"}}({{local . "data"}}, {{local . "a"}}, {{local . "b"}}, {{local . "swaps"}})
	{{local . "b"}}, {{local . "c"}} = {{helper . "Order2"}}({{local . "data"}}, {{local . "b"}}, {{local . "c"}}, {{local . "swaps"}})
	{{local . "a"}}, {{local . "b"}} = {{helper . "Order2"}}({{local . "data"}}, {{local . "a"}}, {{local . "b"}}, {{local . "swaps"}})
	return {{local . "b"}}
}
{{end}}

{{define "MedianAdjacent"}}
func {{helper . "MedianAdjacent"}}({{local . "data"}} []{{.Type}}, {{local . "a"}} int, {{local . "swaps"}} *int) int {
	return {{helper . "Median"}}({{local . "data"}}, {{local . "a"}}-1, {{local . "a"}}, {{local . "a"}}+1, {{local . "swaps"}})
}
{{end}}

{{define "ReverseRange"}}
func {{helper . "ReverseRange"}}({{local . "data"}} []{{.Type}}, {{local . "a"}}, {{local . "b"}} int) {
	{{local . "i"}}, {{local . "j"}} := {{local . "a"}}, {{local . "b"}}-1
	for {{local . "i"}} < {{local . "j"}} {
		{{local . "data"}}[{{local . "i"}}], {{local . "data"}}[{{local . "j"}}] = {{local . "data"}}[{{local . "j"}}], {{local . "data"}}[{{local . "i"}}]
		{{local . "i"}}++
		{{local . "j"}}--
	}
}
{{end}}

{{define "Support"}}
type {{helper . "SortedHint"}} int

const (
	{{helper . "UnknownHint"}} {{helper . "SortedHint"}} = iota
	{{helper . "IncreasingHint"}}
	{{helper . "DecreasingHint"}}
)

type {{helper . "Xorshift"}} uint64

func ({{local . "r"}} *{{helper . "Xorshift"}}) Next() uint64 {
	*{{local . "r"}} ^= *{{local . "r"}} << 13
	*{{local . "r"}} ^= *{{local . "r"}} >> 7
	*{{local . "r"}} ^= *{{local . "r"}} << 17
	return uint64(*{{local . "r"}})
}

func {{helper . "NextPowerOfTwo"}}({{local . "length"}} int) uint {
	return 1 << {{.Bits}}.Len(uint({{local . "length"}}))
}
{{end}}

{{define "Stable"}}
func {{helper . "Stable"}}({{local . "data"}} []{{.Type}}, {{local . "n"}} int) {
	{{local . "blockSize"}} := 20
	{{local . "a"}}, {{local . "b"}} := 0, {{local . "blockSize"}}
	for {{local . "b"}} <= {{local . "n"}} {
		{{helper . "InsertionSort"}}({{local . "data"}}, {{local . "a"}}, {{local . "b"}})
		{{local . "a"}} = {{local . "b"}}
		{{local . "b"}} += {{local . "blockSize"}}
	}
	{{helper . "InsertionSort"}}({{local . "data"}}, {{local . "a"}}, {{local . "n"}})
	for {{local . "blockSize"}} < {{local . "n"}} {
		{{local . "a"}}, {{local . "b"}} = 0, 2*{{local . "blockSize"}}
		for {{local . "b"}} <= {{local . "n"}} {
			{{helper . "SymMerge"}}({{local . "data"}}, {{local . "a"}}, {{local . "a"}}+{{local . "blockSize"}}, {{local . "b"}})
			{{local . "a"}} = {{local . "b"}}
			{{local . "b"}} += 2 * {{local . "blockSize"}}
		}
		if {{local . "m"}} := {{local . "a"}} + {{local . "blockSize"}}; {{local . "m"}} < {{local . "n"}} {
			{{helper . "SymMerge"}}({{local . "data"}}, {{local . "a"}}, {{local . "m"}}, {{local . "n"}})
		}
		{{local . "blockSize"}} *= 2
	}
}
{{end}}

{{define "SymMerge"}}
func {{helper . "SymMerge"}}({{local . "data"}} []{{.Type}}, {{local . "a"}}, {{local . "m"}}, {{local . "b"}} int) {
	if {{local . "m"}}-{{local . "a"}} == 1 {
		{{local . "i"}}, {{local . "j"}} := {{local . "m"}}, {{local . "b"}}
		for {{local . "i"}} < {{local . "j"}} {
			{{local . "h"}} := int(uint({{local . "i"}}+{{local . "j"}}) >> 1)
			if {{compare . "data[h]" "data[a]"}} < 0 {
				{{local . "i"}} = {{local . "h"}} + 1
			} else {
				{{local . "j"}} = {{local . "h"}}
			}
		}
		for {{local . "k"}} := {{local . "a"}}; {{local . "k"}} < {{local . "i"}}-1; {{local . "k"}}++ {
			{{local . "data"}}[{{local . "k"}}], {{local . "data"}}[{{local . "k"}}+1] = {{local . "data"}}[{{local . "k"}}+1], {{local . "data"}}[{{local . "k"}}]
		}
		return
	}
	if {{local . "b"}}-{{local . "m"}} == 1 {
		{{local . "i"}}, {{local . "j"}} := {{local . "a"}}, {{local . "m"}}
		for {{local . "i"}} < {{local . "j"}} {
			{{local . "h"}} := int(uint({{local . "i"}}+{{local . "j"}}) >> 1)
			if !({{compare . "data[m]" "data[h]"}} < 0) {
				{{local . "i"}} = {{local . "h"}} + 1
			} else {
				{{local . "j"}} = {{local . "h"}}
			}
		}
		for {{local . "k"}} := {{local . "m"}}; {{local . "k"}} > {{local . "i"}}; {{local . "k"}}-- {
			{{local . "data"}}[{{local . "k"}}], {{local . "data"}}[{{local . "k"}}-1] = {{local . "data"}}[{{local . "k"}}-1], {{local . "data"}}[{{local . "k"}}]
		}
		return
	}
	{{local . "mid"}} := int(uint({{local . "a"}}+{{local . "b"}}) >> 1)
	{{local . "n"}} := {{local . "mid"}} + {{local . "m"}}
	var {{local . "start"}}, {{local . "r"}} int
	if {{local . "m"}} > {{local . "mid"}} {
		{{local . "start"}} = {{local . "n"}} - {{local . "b"}}
		{{local . "r"}} = {{local . "mid"}}
	} else {
		{{local . "start"}} = {{local . "a"}}
		{{local . "r"}} = {{local . "m"}}
	}
	{{local . "p"}} := {{local . "n"}} - 1
	for {{local . "start"}} < {{local . "r"}} {
		{{local . "c"}} := int(uint({{local . "start"}}+{{local . "r"}}) >> 1)
		if !({{compare . "data[p-c]" "data[c]"}} < 0) {
			{{local . "start"}} = {{local . "c"}} + 1
		} else {
			{{local . "r"}} = {{local . "c"}}
		}
	}
	{{local . "end"}} := {{local . "n"}} - {{local . "start"}}
	if {{local . "start"}} < {{local . "m"}} && {{local . "m"}} < {{local . "end"}} {
		{{helper . "Rotate"}}({{local . "data"}}, {{local . "start"}}, {{local . "m"}}, {{local . "end"}})
	}
	if {{local . "a"}} < {{local . "start"}} && {{local . "start"}} < {{local . "mid"}} {
		{{helper . "SymMerge"}}({{local . "data"}}, {{local . "a"}}, {{local . "start"}}, {{local . "mid"}})
	}
	if {{local . "mid"}} < {{local . "end"}} && {{local . "end"}} < {{local . "b"}} {
		{{helper . "SymMerge"}}({{local . "data"}}, {{local . "mid"}}, {{local . "end"}}, {{local . "b"}})
	}
}
{{end}}

{{define "Rotate"}}
func {{helper . "Rotate"}}({{local . "data"}} []{{.Type}}, {{local . "a"}}, {{local . "m"}}, {{local . "b"}} int) {
	{{local . "i"}}, {{local . "j"}} := {{local . "m"}}-{{local . "a"}}, {{local . "b"}}-{{local . "m"}}
	for {{local . "i"}} != {{local . "j"}} {
		if {{local . "i"}} > {{local . "j"}} {
			{{helper . "SwapRange"}}({{local . "data"}}, {{local . "m"}}-{{local . "i"}}, {{local . "m"}}, {{local . "j"}})
			{{local . "i"}} -= {{local . "j"}}
		} else {
			{{helper . "SwapRange"}}({{local . "data"}}, {{local . "m"}}-{{local . "i"}}, {{local . "m"}}+{{local . "j"}}-{{local . "i"}}, {{local . "i"}})
			{{local . "j"}} -= {{local . "i"}}
		}
	}
	{{helper . "SwapRange"}}({{local . "data"}}, {{local . "m"}}-{{local . "i"}}, {{local . "m"}}, {{local . "i"}})
}
{{end}}

{{define "SwapRange"}}
func {{helper . "SwapRange"}}({{local . "data"}} []{{.Type}}, {{local . "a"}}, {{local . "b"}}, {{local . "n"}} int) {
	for {{local . "i"}} := 0; {{local . "i"}} < {{local . "n"}}; {{local . "i"}}++ {
		{{local . "data"}}[{{local . "a"}}+{{local . "i"}}], {{local . "data"}}[{{local . "b"}}+{{local . "i"}}] = {{local . "data"}}[{{local . "b"}}+{{local . "i"}}], {{local . "data"}}[{{local . "a"}}+{{local . "i"}}]
	}
}
{{end}}
`

func (e *sortEmitter) data(templateName string) sortTemplateData {
	locals := newLocalNames(e.cmpName, sortTemplateLocals[templateName]...)
	return sortTemplateData{
		Type:       e.typ,
		Name:       e.name,
		CmpName:    e.cmpName,
		Arg1ByRef:  e.arg1ByRef,
		Arg2ByRef:  e.arg2ByRef,
		Bits:       e.bits,
		Helpers:    e.helpers,
		Support:    e.support,
		LocalNames: locals.names,
	}
}

func (e *sortEmitter) emit(templateName string) {
	if e.err != nil || e.emitted[templateName] {
		return
	}
	e.emitted[templateName] = true
	var generated bytes.Buffer
	if err := sortTemplates.ExecuteTemplate(&generated, templateName, e.data(templateName)); err != nil {
		e.err = fmt.Errorf("monoslices: render sorting template %q: %w", templateName, err)
		return
	}
	e.source.Write(generated.Bytes())
}

func (e *sortEmitter) emitUnstableSpecialized() {
	e.emit("InsertionSort")
	e.emit("SiftDown")
	e.emit("HeapSort")
	e.emit("PDQSort")
	e.emit("Partition")
	e.emit("PartitionEqual")
	e.emit("PartialInsertionSort")
	e.emit("BreakPatterns")
	e.emit("ChoosePivot")
	e.emit("Order2")
	e.emit("Median")
	e.emit("MedianAdjacent")
	e.emit("ReverseRange")
}

func (e *sortEmitter) emitUnstableHelpers() {
	e.emitUnstableSpecialized()
	e.emit("Support")
}

func (e *sortEmitter) emitStableHelpers() {
	e.emit("InsertionSort")
	e.emit("Stable")
	e.emit("SymMerge")
	e.emit("Rotate")
	e.emit("SwapRange")
}

func emitSortGroup(source *bytes.Buffer, group model.SortGroupPlan, support *model.SortSupportPlan, typ, bitsAlias string) error {
	if group.Cmp == nil {
		return fmt.Errorf("monoslices: sorting group has no comparator")
	}
	emitter := &sortEmitter{
		source:    source,
		cmpName:   group.Cmp.Name,
		arg1ByRef: group.Arg1ByRef,
		arg2ByRef: group.Arg2ByRef,
		typ:       typ,
		bits:      bitsAlias,
		helpers:   group.Helpers,
		emitted:   make(map[string]bool),
	}
	if support != nil {
		emitter.support = support.Helpers
	}
	if group.Unstable {
		emitter.emitUnstableSpecialized()
	}
	if group.Stable {
		emitter.emitStableHelpers()
	}
	if emitter.err != nil {
		return emitter.err
	}
	return nil
}

func emitSortSupport(source *bytes.Buffer, support model.SortSupportPlan, bitsAlias string) error {
	emitter := &sortEmitter{
		source:  source,
		bits:    bitsAlias,
		support: support.Helpers,
		emitted: make(map[string]bool),
	}
	emitter.emit("Support")
	if emitter.err != nil {
		return emitter.err
	}
	return nil
}

func (e sortEmitter) helper(base string) string {
	return sortHelper(sortTemplateData{Name: e.name}, base)
}

func emitSortGroupEntry(source *bytes.Buffer, plan *model.OperationPlan, group model.SortGroupPlan, typ, bitsAlias string) error {
	if plan.Func == nil {
		return fmt.Errorf("monoslices: operation %q has no specialization function", plan.Op)
	}
	data := operationTemplateData{
		PublicName: plan.PublicName,
		FuncName:   plan.Func.Name,
		ByrefNote:  byrefNote(plan),
		Elem1:      typ,
		Locals:     newLocalNames(plan.Func.Name, "s"),
		Bits:       bitsAlias,
	}
	if plan.Op == config.OperationSort {
		data.SortHelper = group.Helpers["PDQSort"]
		return executeFunctionTemplate(source, "sort", data)
	}
	data.StableSortHelper = group.Helpers["Stable"]
	return executeFunctionTemplate(source, "sort-stable", data)
}
