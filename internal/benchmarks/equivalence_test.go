package benchmarks

import (
	"cmp"
	"fmt"
	"slices"
	"testing"
)

type recordOperations[T comparable] struct {
	index        func([]T) int
	contains     func([]T) bool
	equal        func([]T, []T) bool
	compare      func([]T, []T) int
	min, max     func([]T) T
	isSorted     func([]T) bool
	search       func([]T, SearchKey) (int, bool)
	delete       func([]T) []T
	compact      func([]T) []T
	sort, stable func([]T)
}

func TestKeyDifferenceOrdering(t *testing.T) {
	for _, a := range []int32{-1 << 31, -1, 0, 1, 1<<31 - 1} {
		for _, b := range []int32{-1 << 31, -1, 0, 1, 1<<31 - 1} {
			got := cmp.Compare(keyDifference(a, b), 0)
			if want := cmp.Compare(a, b); got != want {
				t.Errorf("keyDifference(%d, %d) sign = %d, want %d", a, b, got, want)
			}
		}
	}
}

func TestBenchmarkFixturesMatchStdlib(t *testing.T) {
	checkRecordOperations(t, "008-value", makeRecord008, record008Predicate, record008Equal, record008Compare, record008SearchCompare, recordOperations[Record008]{Record008ValueIndex, Record008ValueContains, Record008ValueEqual, Record008ValueCompare, Record008ValueMin, Record008ValueMax, Record008ValueIsSorted, Record008ValueSearchBinarySearch, Record008ValueDelete, Record008ValueCompact, Record008ValueSort, Record008ValueSortStable})
	checkRecordOperations(t, "016-value", makeRecord016, record016Predicate, record016Equal, record016Compare, record016SearchCompare, recordOperations[Record016]{Record016ValueIndex, Record016ValueContains, Record016ValueEqual, Record016ValueCompare, Record016ValueMin, Record016ValueMax, Record016ValueIsSorted, Record016ValueSearchBinarySearch, Record016ValueDelete, Record016ValueCompact, Record016ValueSort, Record016ValueSortStable})
	checkRecordOperations(t, "016-byref", makeRecord016, record016Predicate, record016Equal, record016Compare, record016SearchCompare, recordOperations[Record016]{Record016ByrefIndex, Record016ByrefContains, Record016ByrefEqual, Record016ByrefCompare, Record016ByrefMin, Record016ByrefMax, Record016ByrefIsSorted, Record016ByrefSearchBinarySearch, Record016ByrefDelete, Record016ByrefCompact, Record016ByrefSort, Record016ByrefSortStable})
	checkRecordOperations(t, "064-byref", makeRecord064, record064PredicateValue, record064EqualValue, record064CompareValue, record064SearchCompare, recordOperations[Record064]{Record064ByrefIndex, Record064ByrefContains, Record064ByrefEqual, Record064ByrefCompare, Record064ByrefMin, Record064ByrefMax, Record064ByrefIsSorted, Record064ByrefSearchBinarySearch, Record064ByrefDelete, Record064ByrefCompact, Record064ByrefSort, Record064ByrefSortStable})
	checkRecordOperations(t, "256-byref", makeRecord256, record256PredicateValue, record256EqualValue, record256CompareValue, record256SearchCompare, recordOperations[Record256]{Record256ByrefIndex, Record256ByrefContains, Record256ByrefEqual, Record256ByrefCompare, Record256ByrefMin, Record256ByrefMax, Record256ByrefIsSorted, Record256ByrefSearchBinarySearch, Record256ByrefDelete, Record256ByrefCompact, Record256ByrefSort, Record256ByrefSortStable})
}

func checkRecordOperations[T comparable](t *testing.T, name string, makeRecord func(int32, int32) T, predicate func(T) bool, equal func(T, T) bool, compare func(T, T) int, searchCompare func(T, SearchKey) int, ops recordOperations[T]) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		const n = 257
		for _, input := range scanInputs(n) {
			values := recordScanCase(n, input.hit, makeRecord)
			if got, want := ops.index(values), slices.IndexFunc(values, predicate); got != want {
				t.Errorf("Index/%s = %d, want %d", input.name, got, want)
			}
			if got, want := ops.contains(values), slices.ContainsFunc(values, predicate); got != want {
				t.Errorf("Contains/%s = %t, want %t", input.name, got, want)
			}
		}
		for _, input := range equalityInputs {
			p := recordEqualCase(n, input.mismatchAt, makeRecord)
			if got, want := ops.equal(p.left, p.right), slices.EqualFunc(p.left, p.right, equal); got != want {
				t.Errorf("Equal/%s = %t, want %t", input.name, got, want)
			}
			if got, want := ops.compare(p.left, p.right), slices.CompareFunc(p.left, p.right, compare); got != want {
				t.Errorf("Compare/%s = %d, want %d", input.name, got, want)
			}
		}
		values := recordPattern(n, patternRandom, makeRecord)
		if got, want := ops.min(values), slices.MinFunc(values, compare); got != want {
			t.Error("Min differs from stdlib")
		}
		if got, want := ops.max(values), slices.MaxFunc(values, compare); got != want {
			t.Error("Max differs from stdlib")
		}
		for _, input := range sortednessInputs {
			values := recordPattern(n, patternSorted, makeRecord)
			if input.earlyExit {
				values[n-2], values[n-1] = values[n-1], values[n-2]
			}
			if got, want := ops.isSorted(values), slices.IsSortedFunc(values, compare); got != want {
				t.Errorf("IsSorted/%s = %t, want %t", input.name, got, want)
			}
		}
		sorted := recordSearch(n, makeRecord)
		queries := variedSearchTargets(n)
		for _, input := range searchInputs {
			queries = append(queries, SearchKey{Key: searchTarget(n, input.kind)})
		}
		for _, target := range queries {
			index, found := ops.search(sorted, target)
			wantIndex, wantFound := slices.BinarySearchFunc(sorted, target, searchCompare)
			if index != wantIndex || found != wantFound {
				t.Fatalf("BinarySearch(%d) = (%d,%t), want (%d,%t)", target.Key, index, found, wantIndex, wantFound)
			}
		}
		for _, density := range benchmarkDeleteDensities {
			source := recordDeleteCase(n, density, makeRecord)
			got, want := ops.delete(slices.Clone(source)), slices.DeleteFunc(slices.Clone(source), predicate)
			if !slices.Equal(got, want) {
				t.Errorf("Delete/%s retained different values", density.name)
			}
			if density.none {
				checkUnchanged(t, "Delete/no-match", source, func(s []T) { _ = ops.delete(s) }, func(s []T) { _ = slices.DeleteFunc(s, predicate) })
			}
		}
		for _, pattern := range compactPatterns {
			source := recordCompact(n, pattern.spacing, makeRecord)
			got, want := ops.compact(slices.Clone(source)), slices.CompactFunc(slices.Clone(source), equal)
			if !slices.Equal(got, want) {
				t.Errorf("Compact/%s retained different values", pattern.name)
			}
			if pattern.spacing == 0 {
				checkUnchanged(t, "Compact/no-duplicates", source, func(s []T) { _ = ops.compact(s) }, func(s []T) { _ = slices.CompactFunc(s, equal) })
			}
		}
		checkRecordAllocations(t, makeRecord, ops)
		for _, pattern := range benchmarkSortPatterns {
			source := recordPattern(n, pattern.pattern, makeRecord)
			got, want := slices.Clone(source), slices.Clone(source)
			ops.sort(got)
			slices.SortFunc(want, compare)
			assertSortedPermutation(t, fmt.Sprintf("Sort/%s", pattern.name), source, got, want, compare)
			got, want = slices.Clone(source), slices.Clone(source)
			ops.stable(got)
			slices.SortStableFunc(want, compare)
			if !slices.Equal(got, want) {
				t.Errorf("SortStable/%s differs from stdlib", pattern.name)
			}
			if pattern.pattern == patternSorted {
				checkUnchanged(t, "Sort/sorted", source, ops.sort, func(s []T) { slices.SortFunc(s, compare) })
				checkUnchanged(t, "SortStable/sorted", source, ops.stable, func(s []T) { slices.SortStableFunc(s, compare) })
			}
		}
	})
}

func checkRecordAllocations[T comparable](t *testing.T, makeRecord func(int32, int32) T, ops recordOperations[T]) {
	t.Helper()
	values := recordPattern(256, patternRandom, makeRecord)
	other := slices.Clone(values)
	sorted := recordSearch(256, makeRecord)
	deleteSource := recordDeleteCase(256, benchmarkDeleteDensity{name: "dense", dense: true}, makeRecord)
	compactSource := recordCompact(256, 4, makeRecord)
	deleteWork, compactWork, sortWork := slices.Clone(deleteSource), slices.Clone(compactSource), slices.Clone(values)
	var result T
	for _, tc := range []struct {
		name string
		call func()
	}{
		{"Index", func() { benchmarkIntSink = ops.index(values) }},
		{"Contains", func() { benchmarkBoolSink = ops.contains(values) }},
		{"Equal", func() { benchmarkBoolSink = ops.equal(values, other) }},
		{"Compare", func() { benchmarkIntSink = ops.compare(values, other) }},
		{"Min", func() { result = ops.min(values) }},
		{"Max", func() { result = ops.max(values) }},
		{"IsSorted", func() { benchmarkBoolSink = ops.isSorted(sorted) }},
		{"BinarySearch", func() { benchmarkIntSink, benchmarkBoolSink = ops.search(sorted, SearchKey{Key: 128}) }},
		{"Delete", func() { copy(deleteWork, deleteSource); benchmarkLenSink = len(ops.delete(deleteWork)) }},
		{"Compact", func() { copy(compactWork, compactSource); benchmarkLenSink = len(ops.compact(compactWork)) }},
		{"Sort", func() { copy(sortWork, values); ops.sort(sortWork) }},
		{"SortStable", func() { copy(sortWork, values); ops.stable(sortWork) }},
	} {
		if allocs := testing.AllocsPerRun(100, tc.call); allocs != 0 {
			t.Errorf("%s allocates %g objects per call", tc.name, allocs)
		}
	}
	_ = result
}

func checkUnchanged[T comparable](t *testing.T, name string, source []T, generated, standard func([]T)) {
	t.Helper()
	work, original := slices.Clone(source), slices.Clone(source)
	generated(work)
	if !slices.Equal(work, original) {
		t.Errorf("%s generated case changes reusable fixture", name)
	}
	work = slices.Clone(source)
	standard(work)
	if !slices.Equal(work, original) {
		t.Errorf("%s stdlib case changes reusable fixture", name)
	}
}

func assertSortedPermutation[T comparable](t *testing.T, name string, source, got, want []T, compare func(T, T) int) {
	t.Helper()
	if !slices.IsSortedFunc(got, compare) || !slices.EqualFunc(got, want, func(a, b T) bool { return compare(a, b) == 0 }) {
		t.Errorf("%s keys differ from stdlib", name)
	}
	counts := make(map[T]int, len(source))
	for _, v := range source {
		counts[v]++
	}
	for _, v := range got {
		counts[v]--
	}
	for _, count := range counts {
		if count != 0 {
			t.Errorf("%s does not retain input records", name)
			break
		}
	}
}
