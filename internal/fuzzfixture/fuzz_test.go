package fuzzfixture

import (
	"slices"
	"testing"
)

func addIntSliceSeeds(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0})
	f.Add([]byte{3, 3, 3, 3})
	f.Add([]byte{252, 254, 0, 2, 4})
	f.Add([]byte{4, 2, 0, 254, 252})
	f.Add([]byte{239, 0, 7, 8, 16, 31, 32})
	f.Add(make([]byte, 12))
	f.Add(make([]byte, 13))
	f.Add(make([]byte, 33))
	f.Add(make([]byte, 129))
}

func addPairSeeds(f *testing.F) {
	f.Add([]byte{}, []byte{})
	f.Add([]byte{1}, []byte{1})
	f.Add([]byte{1, 1, 2, 2}, []byte{1, 1, 2, 2})
	f.Add([]byte{252, 254, 0, 2}, []byte{252, 254, 1, 2})
	f.Add([]byte{4, 3, 2, 1}, []byte{})
	f.Add([]byte{}, []byte{0, 1, 2, 3})
}

// Bound fuzz case work while retaining large slices for algorithm-regime coverage.
const maxFuzzSliceLength = 4096

func intsFromBytes(data []byte) []int {
	if len(data) > maxFuzzSliceLength {
		data = data[:maxFuzzSliceLength]
	}
	values := make([]int, len(data))
	for index, value := range data {
		values[index] = int(int8(value))
	}
	return values
}

func FuzzPredicate(f *testing.F) {
	addIntSliceSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		values := intsFromBytes(data)
		got := IntsContains(values)
		want := slices.ContainsFunc(values, isInteresting)
		if got != want {
			t.Fatalf("Contains(%v) = %t, want %t", values, got, want)
		}

		gotIndex := IntsIndex(values)
		wantIndex := slices.IndexFunc(values, isInteresting)
		if gotIndex != wantIndex {
			t.Fatalf("Index(%v) = %d, want %d", values, gotIndex, wantIndex)
		}

		if got := RefIntsContains(values); got != want {
			t.Fatalf("byref Contains(%v) = %t, want %t", values, got, want)
		}
		if got := RefIntsIndex(values); got != wantIndex {
			t.Fatalf("byref Index(%v) = %d, want %d", values, got, wantIndex)
		}
	})
}

func FuzzDeleteCompact(f *testing.F) {
	addIntSliceSeeds(f)
	f.Add([]byte{0, 1, 0, 2, 3, 0})
	f.Add([]byte{1, 1, 2, 2, 2, 3, 3})
	f.Fuzz(func(t *testing.T, data []byte) {
		values := intsFromBytes(data)
		gotBacking := slices.Clone(values)
		got := IntsDelete(gotBacking)
		wantBacking := slices.Clone(values)
		want := slices.DeleteFunc(wantBacking, isInteresting)
		assertDeleteResult(t, "Delete", gotBacking, got, wantBacking, want)

		gotBacking = slices.Clone(values)
		got = IntsCompact(gotBacking)
		wantBacking = slices.Clone(values)
		want = slices.CompactFunc(wantBacking, equalInt)
		assertDeleteResult(t, "Compact", gotBacking, got, wantBacking, want)

		gotBacking = slices.Clone(values)
		got = RefIntsDelete(gotBacking)
		wantBacking = slices.Clone(values)
		want = slices.DeleteFunc(wantBacking, isInteresting)
		assertDeleteResult(t, "byref Delete", gotBacking, got, wantBacking, want)

		gotBacking = slices.Clone(values)
		got = RefIntsCompact(gotBacking)
		wantBacking = slices.Clone(values)
		want = slices.CompactFunc(wantBacking, equalInt)
		assertDeleteResult(t, "byref Compact", gotBacking, got, wantBacking, want)
	})
}

func assertDeleteResult(t *testing.T, operation string, gotBacking []int, got []int, wantBacking []int, want []int) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s result = %v, want %v", operation, got, want)
	}
	if len(got) != len(want) {
		t.Fatalf("%s length = %d, want %d", operation, len(got), len(want))
	}
	if !slices.Equal(gotBacking[len(got):], wantBacking[len(want):]) {
		t.Fatalf("%s tail = %v, want %v", operation, gotBacking[len(got):], wantBacking[len(want):])
	}
	for index, value := range gotBacking[len(got):] {
		if value != 0 {
			t.Fatalf("%s tail index %d = %d, want zero", operation, index+len(got), value)
		}
	}
}

func FuzzEqualCompare(f *testing.F) {
	addPairSeeds(f)
	f.Fuzz(func(t *testing.T, leftData, rightData []byte) {
		left, right := intsFromBytes(leftData), intsFromBytes(rightData)
		gotEqual := IntsEqual(left, right)
		wantEqual := slices.EqualFunc(left, right, equalInt)
		if gotEqual != wantEqual {
			t.Fatalf("Equal(%v, %v) = %t, want %t", left, right, gotEqual, wantEqual)
		}

		gotCompare := IntsCompare(left, right)
		wantCompare := slices.CompareFunc(left, right, compareInt)
		if gotCompare != wantCompare {
			t.Fatalf("Compare(%v, %v) = %d, want %d", left, right, gotCompare, wantCompare)
		}

		if got := RefIntsEqual(left, right); got != wantEqual {
			t.Fatalf("byref Equal(%v, %v) = %t, want %t", left, right, got, wantEqual)
		}
		if got := RefIntsCompare(left, right); got != wantCompare {
			t.Fatalf("byref Compare(%v, %v) = %d, want %d", left, right, got, wantCompare)
		}
	})
}

func FuzzMinMaxIsSorted(f *testing.F) {
	addIntSliceSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		values := intsFromBytes(data)
		gotSorted := IntsIsSorted(values)
		wantSorted := slices.IsSortedFunc(values, compareInt)
		if gotSorted != wantSorted {
			t.Fatalf("IsSorted(%v) = %t, want %t", values, gotSorted, wantSorted)
		}
		if got := RefIntsIsSorted(values); got != wantSorted {
			t.Fatalf("byref IsSorted(%v) = %t, want %t", values, got, wantSorted)
		}
		if len(values) == 0 {
			return
		}

		gotMin := IntsMin(values)
		wantMin := slices.MinFunc(values, compareInt)
		if gotMin != wantMin {
			t.Fatalf("Min(%v) = %d, want %d", values, gotMin, wantMin)
		}
		gotMax := IntsMax(values)
		wantMax := slices.MaxFunc(values, compareInt)
		if gotMax != wantMax {
			t.Fatalf("Max(%v) = %d, want %d", values, gotMax, wantMax)
		}
		if got := RefIntsMin(values); got != wantMin {
			t.Fatalf("byref Min(%v) = %d, want %d", values, got, wantMin)
		}
		if got := RefIntsMax(values); got != wantMax {
			t.Fatalf("byref Max(%v) = %d, want %d", values, got, wantMax)
		}
	})
}

func FuzzBinarySearch(f *testing.F) {
	f.Add([]byte{}, 0)
	f.Add([]byte{1}, 1)
	f.Add([]byte{2, 2, 2, 2}, 2)
	f.Add([]byte{1, 2, 3, 4, 5}, 3)
	f.Add([]byte{5, 4, 3, 2, 1}, 0)
	f.Add([]byte{224, 255, 0, 0, 0, 31, 32}, 0)
	f.Add(make([]byte, 12), 0)
	f.Add(make([]byte, 33), 0)
	f.Fuzz(func(t *testing.T, data []byte, target int) {
		values := slices.Clone(intsFromBytes(data))
		slices.SortFunc(values, compareInt)
		gotIndex, gotFound := IntsBinarySearch(values, target)
		wantIndex, wantFound := slices.BinarySearchFunc(values, target, compareInt)
		if gotIndex != wantIndex || gotFound != wantFound {
			t.Fatalf("BinarySearch(%v, %d) = (%d, %t), want (%d, %t)", values, target, gotIndex, gotFound, wantIndex, wantFound)
		}
		gotIndex, gotFound = RefIntsBinarySearch(values, &target)
		if gotIndex != wantIndex || gotFound != wantFound {
			t.Fatalf("byref BinarySearch(%v, %d) = (%d, %t), want (%d, %t)", values, target, gotIndex, gotFound, wantIndex, wantFound)
		}
	})
}

func FuzzHeterogeneousBinarySearch(f *testing.F) {
	f.Add([]byte{}, 0)
	f.Add([]byte{1}, 1)
	f.Add([]byte{2, 2, 2, 4}, 2)
	f.Add([]byte{4, 3, 2, 1}, 3)
	f.Add([]byte{240, 248, 0, 8, 16}, 7)
	f.Add(make([]byte, 12), 0)
	f.Add(make([]byte, 33), 0)
	f.Fuzz(func(t *testing.T, data []byte, target int) {
		keys := intsFromBytes(data)
		records := make([]Record, len(keys))
		for index, key := range keys {
			records[index] = Record{Key: key, Seq: index}
		}
		slices.SortStableFunc(records, compareRecord)
		wantIndex, wantFound := slices.BinarySearchFunc(records, SearchKey{Key: target}, compareRecordKey)
		gotIndex, gotFound := RecordKeysBinarySearch(records, SearchKey{Key: target})
		if gotIndex != wantIndex || gotFound != wantFound {
			t.Fatalf("BinarySearch(%v, %d) = (%d, %t), want (%d, %t)", records, target, gotIndex, gotFound, wantIndex, wantFound)
		}
		gotIndex, gotFound = RefRecordKeysBinarySearch(records, SearchKey{Key: target})
		if gotIndex != wantIndex || gotFound != wantFound {
			t.Fatalf("byref BinarySearch(%v, %d) = (%d, %t), want (%d, %t)", records, target, gotIndex, gotFound, wantIndex, wantFound)
		}
	})
}

func FuzzSort(f *testing.F) {
	addIntSliceSeeds(f)
	f.Add([]byte{2, 1, 2, 1, 0, 0})
	f.Add([]byte{64, 63, 62, 1, 0, 255})
	f.Fuzz(func(t *testing.T, data []byte) {
		values := intsFromBytes(data)
		got := slices.Clone(values)
		want := slices.Clone(values)
		IntsSort(got)
		slices.SortFunc(want, compareInt)
		if !slices.IsSortedFunc(got, compareInt) {
			t.Fatalf("generated sort is not sorted: %v", got)
		}
		if !slices.IsSortedFunc(want, compareInt) {
			t.Fatalf("standard sort is not sorted: %v", want)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("generated sort = %v, standard sort = %v", got, want)
		}
		if !sameIntMultiset(got, values) {
			t.Fatalf("generated sort is not a permutation of %v: %v", values, got)
		}
		gotByref := slices.Clone(values)
		RefIntsSort(gotByref)
		if !slices.Equal(gotByref, want) {
			t.Fatalf("byref generated sort = %v, standard sort = %v", gotByref, want)
		}
	})
}

func sameIntMultiset(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[int]int, len(left))
	for _, value := range left {
		counts[value]++
	}
	for _, value := range right {
		counts[value]--
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}

func FuzzStableSort(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0})
	f.Add([]byte{2, 1, 2, 1, 2})
	f.Add([]byte{0, 1, 2, 3, 4})
	f.Add([]byte{4, 3, 2, 1, 0})
	f.Add([]byte{249, 0, 7, 0, 249, 7, 0})
	f.Add(make([]byte, 20))
	f.Add(make([]byte, 21))
	f.Fuzz(func(t *testing.T, data []byte) {
		keys := intsFromBytes(data)
		got := recordsFromKeys(keys)
		want := slices.Clone(got)
		RecordsSortStable(got)
		slices.SortStableFunc(want, compareRecord)
		if !slices.Equal(got, want) {
			t.Fatalf("generated stable sort = %v, standard stable sort = %v", got, want)
		}
		gotByref := intsFromBytes(data)
		wantByref := slices.Clone(gotByref)
		RefIntsSortStable(gotByref)
		slices.SortStableFunc(wantByref, compareInt)
		if !slices.Equal(gotByref, wantByref) {
			t.Fatalf("byref generated stable sort = %v, standard stable sort = %v", gotByref, wantByref)
		}
		for index := 1; index < len(got); index++ {
			if got[index-1].Key > got[index].Key {
				t.Fatalf("stable sort is not sorted: %v", got)
			}
			if got[index-1].Key == got[index].Key && got[index-1].Seq >= got[index].Seq {
				t.Fatalf("stable sort changed equal-key order: %v", got)
			}
		}
	})
}

func recordsFromKeys(keys []int) []Record {
	records := make([]Record, len(keys))
	for index, key := range keys {
		records[index] = Record{Key: key, Seq: index}
	}
	return records
}

func TestAggregateSortComparators(t *testing.T) {
	ints := []int{3, -1, 2, -1}
	IntsSort(ints)
	if want := []int{-1, -1, 2, 3}; !slices.Equal(ints, want) {
		t.Fatalf("IntsSort = %v, want %v", ints, want)
	}

	records := []Record{{Key: 3, Seq: 0}, {Key: 1, Seq: 1}, {Key: 2, Seq: 2}}
	RecordsSort(records)
	if got := []int{records[0].Key, records[1].Key, records[2].Key}; !slices.Equal(got, []int{1, 2, 3}) {
		t.Fatalf("RecordsSort keys = %v, want [1 2 3]", got)
	}

	stable := []Record{{Key: 2, Seq: 0}, {Key: 1, Seq: 1}, {Key: 2, Seq: 2}}
	RecordsSortStable(stable)
	if want := []Record{{Key: 1, Seq: 1}, {Key: 2, Seq: 0}, {Key: 2, Seq: 2}}; !slices.Equal(stable, want) {
		t.Fatalf("RecordsSortStable = %v, want %v", stable, want)
	}
}

func FuzzWeirdCompact(f *testing.F) {
	addIntSliceSeeds(f)
	f.Add([]byte{0, 1, 2, 3, 2, 1, 0})
	f.Add([]byte{3, 2, 1, 1, 2, 3})
	f.Add([]byte{254, 255, 0, 1, 2})
	f.Fuzz(func(t *testing.T, data []byte) {
		values := intsFromBytes(data)
		gotBacking := slices.Clone(values)
		got := WeirdCompact(gotBacking)
		wantBacking := slices.Clone(values)
		want := slices.CompactFunc(wantBacking, weirdEqual)
		assertDeleteResult(t, "WeirdCompact", gotBacking, got, wantBacking, want)
	})
}

func TestCompactWithNonEquivalentEquality(t *testing.T) {
	for _, input := range [][]int{
		{0, 1, 2, 3, 2, 1, 0},
		{3, 2, 1, 1, 2, 3},
		{-2, -1, 0, 1, 2},
	} {
		gotBacking := slices.Clone(input)
		got := WeirdCompact(gotBacking)
		wantBacking := slices.Clone(input)
		want := slices.CompactFunc(wantBacking, weirdEqual)
		assertDeleteResult(t, "WeirdCompact", gotBacking, got, wantBacking, want)
	}
}

func TestMinMaxEmptyPanics(t *testing.T) {
	for _, test := range []struct {
		name string
		call func()
	}{
		{name: "min", call: func() { IntsMin(nil) }},
		{name: "max", call: func() { IntsMax(nil) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatalf("%s did not panic", test.name)
				}
			}()
			test.call()
		})
	}
}
