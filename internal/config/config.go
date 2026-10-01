// Package config parses and normalizes package-wide command-line options and
// source directives for monoslices.
package config

import (
	"errors"
	"flag"
	"fmt"
	"go/token"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SemanticKind identifies a specialization function role accepted by monoslices.
type SemanticKind string

const (
	SemanticPred SemanticKind = "pred"
	SemanticEq   SemanticKind = "eq"
	SemanticCmp  SemanticKind = "cmp"
)

// ByRefMode selects which eligible specialization-function parameters receive
// slice elements by reference.
type ByRefMode string

const (
	ByRefDisabled ByRefMode = ""
	ByRefTrue     ByRefMode = "true"
	ByRefFirst    ByRefMode = "first"
	ByRefSecond   ByRefMode = "second"
)

// AppliesTo reports whether this mode selects the zero-based parameter index.
func (mode ByRefMode) AppliesTo(index int) bool {
	return mode == ByRefTrue || mode == ByRefFirst && index == 0 || mode == ByRefSecond && index == 1
}

// Operation is a supported generated operation name.
type Operation string

const (
	OperationContains     Operation = "contains"
	OperationIndex        Operation = "index"
	OperationDelete       Operation = "delete"
	OperationCompact      Operation = "compact"
	OperationEqual        Operation = "equal"
	OperationCompare      Operation = "compare"
	OperationMin          Operation = "min"
	OperationMax          Operation = "max"
	OperationSort         Operation = "sort"
	OperationSortStable   Operation = "sort-stable"
	OperationIsSorted     Operation = "is-sorted"
	OperationBinarySearch Operation = "binary-search"
)

// OperationSpec is the central registry entry for a supported operation.
type OperationSpec struct {
	Name     Operation
	Semantic SemanticKind
}

// operationRegistry is kept in canonical generation order. Later phases use
// this same order when deciding which operations are applicable.
var operationRegistry = [...]OperationSpec{
	{Name: OperationContains, Semantic: SemanticPred},
	{Name: OperationIndex, Semantic: SemanticPred},
	{Name: OperationDelete, Semantic: SemanticPred},
	{Name: OperationCompact, Semantic: SemanticEq},
	{Name: OperationEqual, Semantic: SemanticEq},
	{Name: OperationCompare, Semantic: SemanticCmp},
	{Name: OperationMin, Semantic: SemanticCmp},
	{Name: OperationMax, Semantic: SemanticCmp},
	{Name: OperationSort, Semantic: SemanticCmp},
	{Name: OperationSortStable, Semantic: SemanticCmp},
	{Name: OperationIsSorted, Semantic: SemanticCmp},
	{Name: OperationBinarySearch, Semantic: SemanticCmp},
}

// SupportedOperations returns the supported operations in canonical order.
func SupportedOperations() []Operation {
	result := make([]Operation, len(operationRegistry))
	for i, spec := range operationRegistry {
		result[i] = spec.Name
	}
	return result
}

// SupportedOperationSpecs returns a copy of the canonical operation registry.
// Callers can use the semantic kind when building later applicability models
// without maintaining a second operation list.
func SupportedOperationSpecs() []OperationSpec {
	result := make([]OperationSpec, len(operationRegistry))
	copy(result, operationRegistry[:])
	return result
}

// CLIConfig is the normalized package-wide command-line configuration.
type CLIConfig struct {
	Out    string
	OutSet bool
}

// DefaultOutputFilename is the package-wide generated output filename.
const DefaultOutputFilename = "monoslices_gen.go"

// DirectiveConfig is the normalized configuration from one source directive.
// Ops is nil when ops was omitted; OpsSet distinguishes that case from an
// explicit operation selection.
type DirectiveConfig struct {
	Name   string
	Pred   string
	Eq     string
	Cmp    string
	ByRef  ByRefMode
	Ops    []Operation
	OpsSet bool
}

// ErrHelp is returned by ParseCLI when the user requested command help.
var ErrHelp = flag.ErrHelp

// ParseCLI parses package-wide command-line options. It does not inspect the
// package in the current working directory.
func ParseCLI(args []string) (CLIConfig, error) {
	var result CLIConfig
	fs := flag.NewFlagSet("monoslices", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	fs.StringVar(&result.Out, "out", "", "generated output filename")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return CLIConfig{}, ErrHelp
		}
		return CLIConfig{}, diagnosticError("%v", err)
	}
	if positional := fs.Args(); len(positional) != 0 {
		return CLIConfig{}, diagnosticError("unexpected positional argument %q", positional[0])
	}
	fs.Visit(func(parsed *flag.Flag) {
		if parsed.Name == "out" {
			result.OutSet = true
		}
	})
	if result.OutSet && result.Out == "" {
		return CLIConfig{}, diagnosticError("-out=%q must not be empty", result.Out)
	}
	if result.Out != "" && !strings.HasSuffix(result.Out, ".go") {
		return CLIConfig{}, diagnosticError("-out=%q must end in .go", result.Out)
	}
	return result, nil
}

// ValidateOutputBasename rejects output basenames that the Go tool does not
// compile as ordinary production package source. Platform-dependent filename
// constraints are checked by loadpkg using the go/build implementation linked
// into the monoslices executable.
func ValidateOutputBasename(name string) error {
	if strings.HasSuffix(name, "_test.go") {
		return diagnosticError("output filename %q must be an ordinary production Go source filename: _test.go files are compiled only during tests", name)
	}
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
		return diagnosticError("output filename %q must be an ordinary production Go source filename: names beginning with '.' or '_' are ignored by the Go tool", name)
	}
	return nil
}

// ParseDirective parses the text following the exact
// //monoslices:generate prefix and returns normalized directive configuration.
func ParseDirective(raw string) (DirectiveConfig, error) {
	assignments, err := lexDirectiveAssignments(raw)
	if err != nil {
		return DirectiveConfig{}, diagnosticError("%v", err)
	}

	values := make(map[string]string, len(assignments))
	for _, assignment := range assignments {
		switch assignment.key {
		case "name", "pred", "eq", "cmp", "byref", "ops":
			values[assignment.key] = assignment.value
		default:
			return DirectiveConfig{}, diagnosticError("unknown directive key %q", assignment.key)
		}
	}

	result := DirectiveConfig{
		Name:   values["name"],
		Pred:   values["pred"],
		Eq:     values["eq"],
		Cmp:    values["cmp"],
		OpsSet: hasDirectiveKey(values, "ops"),
	}
	if result.Name == "" {
		return DirectiveConfig{}, diagnosticError("directive name is required")
	}
	if !validIdentifier(result.Name) {
		return DirectiveConfig{}, diagnosticError("name=%q is not a valid non-keyword Go identifier", result.Name)
	}
	for _, role := range []struct {
		key   string
		value string
	}{
		{key: "pred", value: result.Pred},
		{key: "eq", value: result.Eq},
		{key: "cmp", value: result.Cmp},
	} {
		if hasDirectiveKey(values, role.key) && !validIdentifier(role.value) {
			return DirectiveConfig{}, diagnosticError("%s=%q is not a valid non-keyword Go identifier", role.key, role.value)
		}
	}
	if result.Pred == "" && result.Eq == "" && result.Cmp == "" {
		return DirectiveConfig{}, diagnosticError("at least one of pred, eq, or cmp is required")
	}

	if rawByRef, ok := values["byref"]; ok {
		if err := normalizeDirectiveByRef(&result, rawByRef); err != nil {
			return DirectiveConfig{}, diagnosticError("%v", err)
		}
	}
	if result.OpsSet {
		operations, err := normalizeDirectiveOperations(values["ops"])
		if err != nil {
			return DirectiveConfig{}, diagnosticError("%v", err)
		}
		result.Ops = operations
	}
	return result, nil
}

type directiveAssignment struct {
	key   string
	value string
}

func lexDirectiveAssignments(raw string) ([]directiveAssignment, error) {
	assignments := make([]directiveAssignment, 0, 4)
	seen := make(map[string]bool)
	for position := 0; ; {
		position = skipDirectiveWhitespace(raw, position)
		if position == len(raw) {
			return assignments, nil
		}

		keyStart := position
		for position < len(raw) {
			runeValue, size := utf8.DecodeRuneInString(raw[position:])
			if runeValue == '=' {
				break
			}
			if unicode.IsSpace(runeValue) {
				return nil, fmt.Errorf("malformed directive assignment near %q: whitespace is not allowed before '='", raw[keyStart:position])
			}
			position += size
		}
		if position == keyStart {
			return nil, errors.New("malformed directive assignment: missing key")
		}
		if position == len(raw) {
			return nil, fmt.Errorf("malformed directive assignment %q: expected '='", raw[keyStart:position])
		}
		key := raw[keyStart:position]
		if seen[key] {
			return nil, fmt.Errorf("directive key %q appears more than once", key)
		}
		seen[key] = true
		position++

		if position == len(raw) {
			assignments = append(assignments, directiveAssignment{key: key})
			return assignments, nil
		}
		runeValue, _ := utf8.DecodeRuneInString(raw[position:])
		if unicode.IsSpace(runeValue) {
			return nil, fmt.Errorf("directive assignment %q has an empty value", key)
		}
		if raw[position] == '"' || raw[position] == '`' {
			return nil, fmt.Errorf("directive assignment %q has a quoted value; quoted values are not supported", key)
		}

		valueStart := position
		for position < len(raw) {
			next, size := utf8.DecodeRuneInString(raw[position:])
			if unicode.IsSpace(next) {
				break
			}
			position += size
		}
		value := raw[valueStart:position]
		assignments = append(assignments, directiveAssignment{key: key, value: value})
	}
}

func skipDirectiveWhitespace(raw string, position int) int {
	for position < len(raw) {
		runeValue, size := utf8.DecodeRuneInString(raw[position:])
		if !unicode.IsSpace(runeValue) {
			break
		}
		position += size
	}
	return position
}

func hasDirectiveKey(values map[string]string, key string) bool {
	_, ok := values[key]
	return ok
}

func normalizeDirectiveByRef(result *DirectiveConfig, raw string) error {
	mode := ByRefMode(raw)
	switch mode {
	case ByRefTrue, ByRefFirst, ByRefSecond:
		result.ByRef = mode
		return nil
	default:
		return fmt.Errorf("byref must be one of true, first, or second; got %q", raw)
	}
}

func normalizeDirectiveOperations(raw string) ([]Operation, error) {
	if raw == "" {
		return nil, errors.New("ops contains an empty operation name")
	}
	selected := make(map[Operation]bool)
	for _, entry := range strings.Split(raw, ",") {
		if entry == "" {
			return nil, errors.New("ops contains an empty operation name")
		}
		operation := Operation(entry)
		if !knownOperation(operation) {
			return nil, fmt.Errorf("unknown operation %q", entry)
		}
		if selected[operation] {
			return nil, fmt.Errorf("duplicate operation %q", entry)
		}
		selected[operation] = true
	}
	return canonicalOperations(selected), nil
}

// FormatDirective renders normalized directive configuration in deterministic
// source/provenance order. It does not include source filename or position.
func FormatDirective(cfg DirectiveConfig) string {
	parts := make([]string, 0, 6)
	if cfg.Name != "" {
		parts = append(parts, "name="+cfg.Name)
	}
	if cfg.Pred != "" {
		parts = append(parts, "pred="+cfg.Pred)
	}
	if cfg.Eq != "" {
		parts = append(parts, "eq="+cfg.Eq)
	}
	if cfg.Cmp != "" {
		parts = append(parts, "cmp="+cfg.Cmp)
	}
	if cfg.ByRef != ByRefDisabled {
		parts = append(parts, "byref="+string(cfg.ByRef))
	}
	if cfg.OpsSet {
		operations := make([]string, len(cfg.Ops))
		for i, operation := range cfg.Ops {
			operations[i] = string(operation)
		}
		parts = append(parts, "ops="+strings.Join(operations, ","))
	}
	return strings.Join(parts, " ")
}

func canonicalOperations(selected map[Operation]bool) []Operation {
	operations := make([]Operation, 0, len(selected))
	for _, spec := range operationRegistry {
		if selected[spec.Name] {
			operations = append(operations, spec.Name)
		}
	}
	return operations
}

func diagnosticError(format string, args ...any) error {
	return fmt.Errorf("monoslices: "+format, args...)
}

func validIdentifier(value string) bool {
	if value == "" || token.Lookup(value) != token.IDENT {
		return false
	}
	for index, r := range value {
		if index == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
			continue
		}
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// PrintUsage writes concise package-wide command help.
func PrintUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: monoslices [-out=<file.go>]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  -out string")
	fmt.Fprintf(w, "        generated package output filename (default %s)\n", DefaultOutputFilename)
	fmt.Fprintln(w, "  -h, -help")
	fmt.Fprintln(w, "        show this help")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "The command processes the package in the current directory.")
	fmt.Fprintln(w, "Generated API families are declared with //monoslices:generate source directives.")
}

func knownOperation(operation Operation) bool {
	for _, spec := range operationRegistry {
		if spec.Name == operation {
			return true
		}
	}
	return false
}
