package benchmarks

import (
	"os"
	"slices"
	"testing"
)

var (
	benchmarkBoolSink bool
	benchmarkIntSink  int
	benchmarkLenSink  int
	benchmark008Sink  Record008
	benchmark016Sink  Record016
	benchmark064Sink  Record064
	benchmark256Sink  Record256
)

const (
	patternRandom = iota
	patternSorted
	patternReverse
	patternDuplicates
	variedSearchQueryCount = 32 * 1024
	benchmarkSliceSize     = 4096
	benchmarkSortSize      = 1024
)

var (
	benchmarkSortPatterns = []struct {
		name    string
		pattern int
	}{
		{"random", patternRandom}, {"sorted", patternSorted}, {"reverse", patternReverse}, {"duplicates", patternDuplicates},
	}
	benchmarkDeleteDensities = []benchmarkDeleteDensity{
		{name: "none", none: true}, {name: "sparse"}, {name: "dense", dense: true},
	}
	compactPatterns = []struct {
		name    string
		spacing int
	}{
		{"no-duplicates", 0}, {"sparse-duplicates", 97}, {"dense-duplicates", 4},
	}
	searchInputs = []struct {
		name string
		kind int
	}{
		{"found-middle", searchFound}, {"missing-below", searchMissingBelow},
		{"missing-interior", searchMissingInterior}, {"missing-above", searchMissingAbove},
	}
)

var equalityInputs = []struct {
	name       string
	mismatchAt int
}{
	{"equal", -1}, {"early-mismatch", 0}, {"late-mismatch", -2},
}
var sortednessInputs = []struct {
	name      string
	earlyExit bool
}{
	{"sorted", false}, {"early-exit", true},
}

const (
	searchFound = iota
	searchMissingBelow
	searchMissingInterior
	searchMissingAbove
)

type benchmarkDeleteDensity struct {
	name        string
	dense, none bool
}
type pair[T any] struct{ left, right []T }

func runImplementations(b *testing.B, name string, generated, standard func(*testing.B)) {
	b.Helper()
	variants := benchmarkVariantNames(name)
	if os.Getenv("MONOSLICES_BENCH_STDLIB_FIRST") == "1" {
		b.Run(variants[1], standard)
		b.Run(variants[0], generated)
		return
	}
	b.Run(variants[0], generated)
	b.Run(variants[1], standard)
}

func benchmarkVariantNames(name string) [2]string {
	return [2]string{name + "/impl=generated", name + "/impl=stdlib"}
}

func reportQueryTime(b *testing.B, queriesPerLoop int) {
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/(float64(b.N)*float64(queriesPerLoop)), "ns/op")
}

func scanInputs(n int) []struct {
	name string
	hit  int
} {
	return []struct {
		name string
		hit  int
	}{
		{"miss", -1}, {"first-hit", 0}, {"middle-hit", n / 2}, {"end-hit", n - 1},
	}
}
func recordScanCase[T any](n, hit int, makeRecord func(int32, int32) T) []T {
	values := recordScan(n, makeRecord)
	if hit >= 0 {
		values[hit] = makeRecord(32, int32(hit))
	}
	return values
}
func recordEqualCase[T any](n, mismatchAt int, makeRecord func(int32, int32) T) pair[T] {
	left := recordPattern(n, patternRandom, makeRecord)
	right := slices.Clone(left)
	if mismatchAt == -2 {
		mismatchAt = n - 1
	}
	if mismatchAt >= 0 {
		right[mismatchAt] = makeRecord(-1, int32(mismatchAt))
	}
	return pair[T]{left, right}
}
func recordPattern[T any](n, pattern int, makeRecord func(int32, int32) T) []T {
	values := make([]T, n)
	keys := deterministicKeys(n, pattern)
	for i := range values {
		values[i] = makeRecord(int32(keys[i]), int32(i))
	}
	return values
}
func deterministicKeys(n, pattern int) []int {
	keys := make([]int, n)
	random := uint64(0x4d595df4d0f33173) + uint64(n)*0x9e3779b97f4a7c15
	for i := range keys {
		switch pattern {
		case patternSorted:
			keys[i] = i
		case patternReverse:
			keys[i] = n - i
		case patternDuplicates:
			random = nextRandom(random)
			keys[i] = int(random % 16)
		default:
			random = nextRandom(random)
			keys[i] = int(random % uint64(n*4+1))
		}
	}
	return keys
}
func nextRandom(value uint64) uint64 { value ^= value << 7; value ^= value >> 9; return value }

func recordSearch[T any](n int, makeRecord func(int32, int32) T) []T {
	values := make([]T, n)
	for i := range values {
		values[i] = makeRecord(int32(i*2), int32(i))
	}
	return values
}
func searchTarget(n, kind int) int32 {
	switch kind {
	case searchFound:
		return int32(n)
	case searchMissingBelow:
		return -1
	case searchMissingInterior:
		return int32(n + 1)
	default:
		return int32(n * 2)
	}
}
func variedSearchTargets(n int) []SearchKey {
	queries := make([]SearchKey, variedSearchQueryCount)
	random := uint64(0x13198a2e03707344) + uint64(n)
	for i := range queries {
		random = nextRandom(random)
		switch i % 4 {
		case 0:
			queries[i].Key = int32(random%uint64(n)) * 2
		case 1:
			queries[i].Key = -1
		case 2:
			queries[i].Key = int32(random%uint64(n-1))*2 + 1
		case 3:
			queries[i].Key = int32(n * 2)
		}
	}
	for i := len(queries) - 1; i > 0; i-- {
		random = nextRandom(random)
		j := int(random % uint64(i+1))
		queries[i], queries[j] = queries[j], queries[i]
	}
	return queries
}

func recordDeleteCase[T any](n int, density benchmarkDeleteDensity, makeRecord func(int32, int32) T) []T {
	values := recordScan(n, makeRecord)
	if density.none {
		return values
	}
	random := uint64(0x243f6a8885a308d3) + uint64(n)
	for i := range values {
		random = nextRandom(random)
		if density.dense && random&1 == 0 || !density.dense && random%97 == 0 {
			values[i] = makeRecord(0, int32(i))
		}
	}
	return values
}
func recordCompact[T any](n, spacing int, makeRecord func(int32, int32) T) []T {
	values := make([]T, n)
	for i := range values {
		key := int32(i)
		if spacing > 0 && i > 0 && i%spacing == 0 {
			key = int32(i - 1)
		}
		values[i] = makeRecord(key, int32(i))
	}
	return values
}
