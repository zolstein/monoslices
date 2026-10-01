package benchmarks

import (
	"cmp"
	"strconv"
)

// keyDifference preserves ordering on 32-bit targets, where subtracting two
// int32 keys in an int can overflow. On 64-bit targets the branch is constant
// and the compiler can inline the subtraction into each comparator.
func keyDifference(a, b int32) int {
	if strconv.IntSize == 32 {
		return cmp.Compare(a, b)
	}
	return int(a) - int(b)
}

// All benchmarks compare only the key. Aux and the initialized payload remain
// part of the slice element, including when stdlib callbacks receive a copy.
type Record008 struct{ Key, Aux int32 }
type Record016 struct {
	Key, Aux int32
	Pad      [8]byte
}
type Record064 struct {
	Key, Aux int32
	Pad      [56]byte
}
type Record256 struct {
	Key, Aux int32
	Pad      [248]byte
}
type SearchKey struct{ Key int32 }

func record008Predicate(v Record008) bool                 { return v.Key&31 == 0 }
func record008Equal(a, b Record008) bool                  { return a.Key == b.Key }
func record008Compare(a, b Record008) int                 { return keyDifference(a.Key, b.Key) }
func record008SearchCompare(a Record008, b SearchKey) int { return keyDifference(a.Key, b.Key) }

func record016Predicate(v Record016) bool                       { return v.Key&31 == 0 }
func record016Equal(a, b Record016) bool                        { return a.Key == b.Key }
func record016Compare(a, b Record016) int                       { return keyDifference(a.Key, b.Key) }
func record016PredicateByref(v *Record016) bool                 { return v.Key&31 == 0 }
func record016EqualByref(a, b *Record016) bool                  { return a.Key == b.Key }
func record016CompareByref(a, b *Record016) int                 { return keyDifference(a.Key, b.Key) }
func record016SearchCompare(a Record016, b SearchKey) int       { return keyDifference(a.Key, b.Key) }
func record016SearchCompareByref(a *Record016, b SearchKey) int { return keyDifference(a.Key, b.Key) }

func record064PredicateByref(v *Record064) bool                 { return v.Key&31 == 0 }
func record064EqualByref(a, b *Record064) bool                  { return a.Key == b.Key }
func record064CompareByref(a, b *Record064) int                 { return keyDifference(a.Key, b.Key) }
func record064SearchCompareByref(a *Record064, b SearchKey) int { return keyDifference(a.Key, b.Key) }

func record256PredicateByref(v *Record256) bool                 { return v.Key&31 == 0 }
func record256EqualByref(a, b *Record256) bool                  { return a.Key == b.Key }
func record256CompareByref(a, b *Record256) int                 { return keyDifference(a.Key, b.Key) }
func record256SearchCompareByref(a *Record256, b SearchKey) int { return keyDifference(a.Key, b.Key) }

//monoslices:generate name=Record008Value pred=record008Predicate eq=record008Equal cmp=record008Compare
//monoslices:generate name=Record016Value pred=record016Predicate eq=record016Equal cmp=record016Compare
//monoslices:generate name=Record016Byref pred=record016PredicateByref eq=record016EqualByref cmp=record016CompareByref byref=true
//monoslices:generate name=Record064Byref pred=record064PredicateByref eq=record064EqualByref cmp=record064CompareByref byref=true
//monoslices:generate name=Record256Byref pred=record256PredicateByref eq=record256EqualByref cmp=record256CompareByref byref=true
//monoslices:generate name=Record008ValueSearch cmp=record008SearchCompare ops=binary-search
//monoslices:generate name=Record016ValueSearch cmp=record016SearchCompare ops=binary-search
//monoslices:generate name=Record016ByrefSearch cmp=record016SearchCompareByref byref=first ops=binary-search
//monoslices:generate name=Record064ByrefSearch cmp=record064SearchCompareByref byref=first ops=binary-search
//monoslices:generate name=Record256ByrefSearch cmp=record256SearchCompareByref byref=first ops=binary-search
