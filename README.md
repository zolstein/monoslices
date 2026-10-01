# monoslices

`monoslices` generates specialized (“monomorphic”) variants of the `*Func` operations in Go’s [`slices`](https://pkg.go.dev/slices) package.

## Motivation

Go’s `slices` provides useful slice operations on generic slices. The `*Func` operations use a function parameter to evaluate slice elements. These operations are flexible, but they can cause unnecessary overhead, for two reasons:

1. The function parameters are passed as runtime values. Calls to these functions often can’t be inlined, which prevents the compiler from optimizing the operation well.
2. The function parameters receive slice elements by value. This can cause the operation to copy more data than is necessary, especially for large element types.

`monoslices` solves both these problems by allowing users to generate slice operations that call their specialization functions directly, and configure whether slice elements are passed to functions as values or references.

## Quick start

`monoslices` requires Go 1.24 or newer. Add it as a tool dependency:

```sh
go get -tool github.com/zolstein/monoslices
```

Then declare the specialization functions and `monoslices:generate` directives:

```go
package records

import "cmp"

type Record struct {
	ID      int
	Expired bool
}

func equalRecord(a, b Record) bool {
	return a.ID == b.ID
}

func compareRecord(a, b Record) int {
	return cmp.Compare(a.ID, b.ID)
}

func isExpired(record Record) bool {
	return record.Expired
}

//monoslices:generate name=Records pred=isExpired eq=equalRecord cmp=compareRecord
```

Run `go tool monoslices` from the package directory to generate.

To integrate with `go generate`, add the following declaration once in any `.go` file in the package:

```go
//go:generate go tool monoslices
```

Run generation from the module root with `go generate ./path/to/package` or `go generate ./...` for all packages.

The example generates a file `monoslices_gen.go`, containing operations such as:

```go
func RecordsEqual(a, b []Record) bool
func RecordsIndex(s []Record) int
func RecordsSort(s []Record)
```

The generated operations call the named specialization functions directly and otherwise follow the behavior and preconditions of their corresponding `slices.*Func` operations.

## Choosing operations

A directive can provide a predicate with `pred`, an equality function with `eq`, a comparator with `cmp`, or any combination. By default, all applicable operations for the provided specialization functions are generated. Use `ops` to generate only specific operations. For example, to generate only `RecordsSort`, use:

```go
//monoslices:generate name=Records cmp=compareRecord ops=sort
```

See the reference for the [complete directive specification](docs/reference.md#directives) and [complete list of operations](docs/reference.md#operations).

## By-reference functions

Normal `slices.*Func` operations pass slice elements by value to their function parameter. For large element types, those copies can be significant.

`byref` lets specialization functions receive pointers to the slice elements instead:

```go
import "cmp"

type Record struct {
	ID      int
	Payload [256]byte
}

func compareRecords(a, b *Record) int {
	return cmp.Compare(a.ID, b.ID)
}

//monoslices:generate name=Records cmp=compareRecords byref=true ops=binary-search
```

The generated API operates on `[]Record`, not `[]*Record`. Arguments that don’t correspond to slice elements are unchanged:

```go
func RecordsBinarySearch(s []Record, target *Record) (int, bool)
```

Pointers to slice elements point into the slice’s backing array and should generally be treated as read-only and ephemeral.

For more details, see the [reference](docs/reference.md#by-reference-functions).

## Performance

Generated operations are faster for most workloads, though some show little or no change, particularly when operations are fully inlined.

Times below are relative to `slices.*Func` across four tested processors. Negative percentages mean `monoslices` is faster; `~0%` means no meaningful difference.

| Operation    |   16 B value |  64 B by-ref | 256 B by-ref |
| ------------ | -----------: | -----------: | -----------: |
| Index        |         \~0% |         \~0% |         \~0% |
| Equal        | -37% to -49% | -70% to -83% | -71% to -90% |
| BinarySearch | -40% to -46% | -43% to -56% | -65% to -74% |
| Sort         | -40% to -52% | -70% to -75% | -76% to -81% |

These columns represent different element sizes and passing modes. See the [full benchmark results](docs/benchmark-results.md) for other operations, workloads, and test details.

## Source restrictions

`monoslices` places some restrictions on a package’s source code, so generation remains well-defined across build configurations.

Declare each directive in the same file as the functions it names. Those functions must be package-level, non-generic, and non-variadic. Put directives in unconditional `.go` files, not test files or files selected only for particular build configurations.

Give explicit aliases to packages that qualify specialization function parameter types. For example, use import `model "example.com/project/model"` when `model.Record` is a parameter type.

See the [reference](docs/reference.md) for signature requirements, byref pointer syntax, and the full source-file rules. The [source restrictions](docs/source-restrictions.md) document discusses why these restrictions exist.

## Attribution and license

`monoslices` is distributed under the BSD 3-Clause License.

Generated files retain the applicable Go Authors copyright and BSD license attribution.
