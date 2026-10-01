# Source restrictions

`monoslices` generates source that may later be compiled under different `GOOS`, `GOARCH`, build tags, cgo settings, test builds, and dependency configurations.

Two parts of the current design account for most of the resulting source restrictions:

- `monoslices` emits one generated `.go` file for a package. Package declarations and build constraints apply to whole files, so generated declarations cannot currently be divided among different package names or build configurations.
- `monoslices` works from source syntax rather than assigning declarations and type expressions a meaning under one particular build configuration. Information needed in generated code must therefore be available directly from the source syntax.

## One generated file

`monoslices` currently emits one generated `.go` file for a package.

A Go source file has one package clause, and its build constraints determine whether the entire file participates in a build. Individual declarations within the file cannot have different package names or build constraints.

This means generated declarations cannot currently mirror distinctions that exist only between source-file variants. In particular, directives must generate declarations that are available unconditionally, and all non-test source variants must agree on the package name used by the generated file.

## Why `monoslices` uses syntactic analysis

There are two broad ways to inspect Go source.

Syntactic analysis works with what is written in the source: declaration names, type expressions, explicit import aliases, pointer syntax, and so on. Semantic analysis uses Go’s type system to determine what those expressions mean.

Semantic analysis requires a build configuration. Go must first determine which files belong to the package and its dependencies. Only then can it resolve identifiers, determine types, and know which imported declarations are available.

Those meanings can change between builds. Build constraints can select different declarations or different definitions of the same name. Dependencies can select different files. A declaration can exist in one configuration and not another.

`monoslices` therefore avoids interpreting source under the configuration in which generation happens and then baking that interpretation into unconditional generated code. It instead works with expressions and bindings present in the package source and leaves their final interpretation to the Go compiler.

For example, generated code can preserve the type expression `Record` without deciding what declaration `Record` refers to. Different builds remain free to give that name the meaning provided by their selected source files.

The tradeoff is that some relationships accepted by Go’s type system are not visible from syntax alone. In those cases, `monoslices` requires the relevant information to be written explicitly.

## Directive and specialization availability

### Directives must be unconditional

Generation directives must appear in files without build constraints.

A directive in a constrained file would describe generated declarations that should exist only when the corresponding constraint is satisfied. `monoslices` cannot represent that distinction while all generated declarations share one output file.

### Specialization functions must be declared in the directive file

A specialization function referenced by a directive must be declared in the same file as that directive.

Because directive files are unconditional, this also makes the specialization function available whenever the generated file is compiled.

The same-file requirement also gives `monoslices` the parameter syntax and import bindings associated with the specialization without having to resolve a declaration from another source file.

### Non-test files must use one package name

All discovered non-test `.go` files must use the same package name, including files whose build constraints make them mutually exclusive.

Go itself only requires the files selected for a particular build to agree on their package name. `monoslices` is stricter because the generated output contains one unconditional package clause and may be compiled with different subsets of the source files.

## Specialization type syntax

The supported shapes of specialization functions are defined by the operations that use them. The restrictions below concern how the relevant types must be written in source.

### Result types must be written as `bool` or `int`

Predicate and equality functions must spell their result type `bool`. Comparator functions must spell their result type `int`.

Aliases and named types are not accepted in their place:

```go
type Bool = bool
type Result int

func equal(a, b Record) Bool
func compare(a, b Record) Result
```

`Bool` and `Result` do not syntactically state the result types that `monoslices` expects.

### `byref` parameters must use explicit pointer syntax

When `byref` applies to a parameter that receives a slice element, the parameter type must contain the pointer explicitly:

```go
func compare(a, b *Record) int
func compare(a, b (*Record)) int
```

A pointer alias is not sufficient:

```go
type RecordPtr = *Record

func compare(a, b RecordPtr) int
```

`*Record` contains the pointee expression `Record` directly. `RecordPtr` does not.

### Parameters for the same element type must use the same type expression

When an operation requires several parameters to represent the same slice element type, those parameters must use matching type expressions after syntactic normalization. For `byref` parameters, the explicit pointer layer is removed before this comparison.

Different expressions are not treated as equivalent merely because they denote the same type:

```go
type Left = Record
type Right = Record

func compareAlias(a Left, b Right) int

func compareImport(a h.Record, b helper.Record) int
```

Generated APIs need one type expression for the slice element type. `monoslices` does not use type checking to prove that different expressions are equivalent or to choose one of them as the generated spelling.

### Qualified parameter types require explicit import aliases

If a type expression copied into generated code contains an imported qualifier, the corresponding import must state that qualifier explicitly.

For example:

```go
import model "example.com/project/model"

func compare(a, b model.Record) int
```

The explicit alias tells `monoslices` that `model` refers to `"example.com/project/model"`.

A default import does not provide that binding syntactically:

```go
import "example.com/project/model"

func compare(a, b model.Record) int
```

The import path is present in the source, but the local name of a default import comes from the imported package’s `package` clause.

Default imports remain usable when their qualifiers do not occur directly in a type expression that `monoslices` needs to reproduce. A local type can, for example, keep the imported qualifier out of the specialization signature:

```go
import "example.com/project/model"

type Record = model.Record

func compare(a, b Record) int
```

`monoslices` preserves qualifiers in copied type expressions rather than rewriting them. If generated types use the same qualifier in more than one specialization, that qualifier must refer to the same import path in every case. Different aliases for the same import path are allowed.

## Package-wide parsing and name checks

These checks are not performed within one selected build configuration. The generated file is unconditional and may be compiled with different subsets of the package source, so `monoslices` combines the names it can determine syntactically across the discovered files.

### All discovered Go files must parse

`monoslices` parses discovered Go files to determine package names, build constraints, declarations, imports, directives, and specialization declarations.

A syntax error can therefore prevent generation even when the affected file would not be selected by the build in which generation happens.

Same-package tests and external tests are also parsed so they can be classified. Same-package tests participate in checks for the generated package. External tests form a separate package, so their declarations and imports are excluded after classification.

All discovered files must parse, but they do not need to type-check successfully for generation to proceed.

### Build variants and same-package tests all participate in name checks

Generated package declarations are checked against syntactically known declarations and explicit import bindings from all discovered variants of the generated package, including build-constrained files and same-package `_test.go` files.

For example:

```go
var RecordsSort int
```

in a constrained source file prevents generation of an unconditional declaration named `RecordsSort`.

Same-package tests participate because the generated declarations are also present when those tests are compiled. External tests do not participate because they belong to a different package.

`monoslices` does not try to prove that arbitrary build constraints are mutually exclusive. Two declarations can therefore conflict for generation purposes even if there is no build in which both would be selected.

### Dot imports are not allowed

Dot imports introduce exported identifiers from another package without naming those identifiers in the importing source:

```go
import . "example.com/helpers"
```

The parsed source therefore does not contain the set of names introduced by the import.

When generation directives are present, dot imports are rejected in files belonging to the generated package, including same-package tests. Dot imports in external test packages are outside these checks.

### Generated declarations must not conflict with known names

Public generated declaration names are fixed by directives. They are checked against other generated declarations and against syntactically known declarations and explicit import bindings from the package source inventory.

Separate directives may use the same `name=` prefix when they generate different final identifiers:

```go
//monoslices:generate name=Records cmp=byID ops=sort
//monoslices:generate name=Records cmp=byName ops=binary-search
```

They may not both generate the same identifier.

Import aliases that appear in copied type expressions are also fixed names in the generated file. Because `monoslices` preserves those qualifiers, they cannot conflict with generated declarations.

Generated code also relies on some unqualified predeclared identifiers. Package-level declarations and preserved import aliases cannot change the meanings of those identifiers in the generated file.

Private helper names and generator-owned import aliases are not fixed API or preserved source syntax. `monoslices` can choose those names around known conflicts instead of rejecting the package.

## What generation does not check

### General semantic correctness

Successful generation does not establish that the package type-checks or compiles.

Undefined identifiers, invalid calls, invalid function bodies, invalid type expressions, unrelated duplicate declarations, and similar semantic errors do not by themselves prevent generation. They matter only when they prevent `monoslices` from determining information it needs.

The Go compiler remains responsible for reporting those errors.

### Default import names are not checked for collisions

A default import does not state its local binding name in the importing source:

```go
import "example.com/project/model"
```

That name comes from the imported package’s `package` clause. Because `monoslices` does not load dependencies to discover default import names, those bindings are not included in generated-name collision checks.

Explicit import aliases are included because their binding names are written directly in the source.

As a result, generation can succeed even when a generated declaration conflicts with a default import name in some source file. The Go compiler reports that conflict when the affected files are compiled together.

## Planned relaxations

### Multiple generated files and constrained generation

Support for multiple generated output files is intended to remove restrictions that come from forcing all generated declarations into one file.

In particular, generated files should be able to carry different build constraints. That would allow directives in constrained source files to produce declarations with corresponding availability instead of requiring every directive to be unconditional.

Splitting generated output by build constraint should also allow source variants with different package names where those variants cannot participate in the same build. Each generated file could use the package name appropriate to the source configuration in which it applies.

This change requires rules for how source-file constraints are carried into generated files and how overlapping constraint sets are handled.
