package benchmarks

import (
	"slices"
	"testing"
	"unsafe"
)

func makeRecord008(key, aux int32) Record008 { return Record008{Key: key, Aux: aux} }
func makeRecord016(key, aux int32) Record016 {
	r := Record016{Key: key, Aux: aux}
	fillRecordPayload(r.Pad[:], key)
	return r
}
func makeRecord064(key, aux int32) Record064 {
	r := Record064{Key: key, Aux: aux}
	fillRecordPayload(r.Pad[:], key)
	return r
}
func makeRecord256(key, aux int32) Record256 {
	r := Record256{Key: key, Aux: aux}
	fillRecordPayload(r.Pad[:], key)
	return r
}
func fillRecordPayload(payload []byte, key int32) {
	for i := range payload {
		payload[i] = byte(int(key) + i*31 + 17)
	}
}
func recordScan[T any](n int, makeRecord func(int32, int32) T) []T {
	values := make([]T, n)
	for i := range values {
		values[i] = makeRecord(1, int32(i))
	}
	return values
}

func record064PredicateValue(v Record064) bool            { return v.Key&31 == 0 }
func record256PredicateValue(v Record256) bool            { return v.Key&31 == 0 }
func record064EqualValue(a, b Record064) bool             { return a.Key == b.Key }
func record256EqualValue(a, b Record256) bool             { return a.Key == b.Key }
func record064CompareValue(a, b Record064) int            { return keyDifference(a.Key, b.Key) }
func record256CompareValue(a, b Record256) int            { return keyDifference(a.Key, b.Key) }
func record064SearchCompare(a Record064, b SearchKey) int { return keyDifference(a.Key, b.Key) }
func record256SearchCompare(a Record256, b SearchKey) int { return keyDifference(a.Key, b.Key) }

func TestRecordSizes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		got, want uintptr
	}{
		{"008", unsafe.Sizeof(Record008{}), 8}, {"016", unsafe.Sizeof(Record016{}), 16},
		{"064", unsafe.Sizeof(Record064{}), 64}, {"256", unsafe.Sizeof(Record256{}), 256},
	} {
		if tc.got != tc.want {
			t.Errorf("Record%s size = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
	if r := makeRecord256(42, 1); r.Pad[0] == 0 && r.Pad[1] == 0 {
		t.Fatal("record payload was not initialized")
	}
}

func TestRecordSizeBenchmarksMatchStdlib(t *testing.T) {
	assertRecordSizeCase(t, "008-value", makeRecord008, Record008ValueIndex, Record008ValueCompare, Record008ValueSort, record008Predicate, record008Compare)
	assertRecordSizeCase(t, "016-value", makeRecord016, Record016ValueIndex, Record016ValueCompare, Record016ValueSort, record016Predicate, record016Compare)
	assertRecordSizeCase(t, "016-byref", makeRecord016, Record016ByrefIndex, Record016ByrefCompare, Record016ByrefSort, record016Predicate, record016Compare)
	assertRecordSizeCase(t, "064-byref", makeRecord064, Record064ByrefIndex, Record064ByrefCompare, Record064ByrefSort, record064PredicateValue, record064CompareValue)
	assertRecordSizeCase(t, "256-byref", makeRecord256, Record256ByrefIndex, Record256ByrefCompare, Record256ByrefSort, record256PredicateValue, record256CompareValue)
}
func assertRecordSizeCase[T comparable](t *testing.T, name string, makeRecord func(int32, int32) T, index func([]T) int, compare func([]T, []T) int, sortRecords func([]T), predicate func(T) bool, compareValue func(T, T) int) {
	t.Helper()
	const n = 257
	scan := recordScanCase(n, -1, makeRecord)
	if got := index(scan); got != -1 {
		t.Errorf("%s miss index = %d", name, got)
	}
	scan[n/2] = makeRecord(32, int32(n/2))
	if got, want := index(scan), slices.IndexFunc(scan, predicate); got != want || got != n/2 {
		t.Errorf("%s hit index = %d, want %d", name, got, want)
	}
	values := recordPattern(n, patternRandom, makeRecord)
	other := slices.Clone(values)
	if got := compare(values, other); got != 0 {
		t.Errorf("%s equal compare = %d", name, got)
	}
	other[n-1] = makeRecord(-1, int32(n-1))
	if got, want := compare(values, other), slices.CompareFunc(values, other, compareValue); got != want || got == 0 {
		t.Errorf("%s mismatch compare = %d, want %d", name, got, want)
	}
	assertRecordSort(t, name, values, sortRecords, compareValue)
}
func assertRecordSort[T comparable](t *testing.T, name string, source []T, generated func([]T), compare func(T, T) int) {
	t.Helper()
	got, want := slices.Clone(source), slices.Clone(source)
	generated(got)
	slices.SortFunc(want, compare)
	if !slices.IsSortedFunc(got, compare) || !slices.EqualFunc(got, want, func(a, b T) bool { return compare(a, b) == 0 }) {
		t.Errorf("%s sorted keys differ from stdlib", name)
	}
	counts := make(map[T]int, len(source))
	for _, v := range source {
		counts[v]++
	}
	for _, v := range got {
		counts[v]--
	}
	for _, n := range counts {
		if n != 0 {
			t.Errorf("%s sorted records differ from input", name)
			break
		}
	}
}
