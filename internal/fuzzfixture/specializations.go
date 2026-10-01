package fuzzfixture

//go:generate go run ../..
//monoslices:generate name=Ints pred=isInteresting eq=equalInt cmp=compareInt
//monoslices:generate name=RefInts pred=isInterestingRef eq=equalIntRef cmp=compareIntRef byref=true
//monoslices:generate name=Records cmp=compareRecord ops=sort,sort-stable
//monoslices:generate name=RecordKeys cmp=compareRecordKey ops=binary-search
//monoslices:generate name=RefRecordKeys cmp=compareRecordKeyRef byref=first ops=binary-search
//monoslices:generate name=Weird eq=weirdEqual ops=compact

// Record keeps an original sequence number so stable sorting can be checked
// independently from its key ordering.
type Record struct {
	Key int
	Seq int
}

type SearchKey struct {
	Key int
}

func isInteresting(value int) bool {
	return value&7 == 0
}

func isInterestingRef(value *int) bool {
	return isInteresting(*value)
}

func equalInt(left, right int) bool {
	return left == right
}

func equalIntRef(left, right *int) bool {
	return equalInt(*left, *right)
}

// compareInt deliberately returns magnitudes other than one. Compare must
// preserve the first nonzero comparator result rather than normalize it.
func compareInt(left, right int) int {
	if left < right {
		return -7
	}
	if left > right {
		return 23
	}
	return 0
}

func compareIntRef(left, right *int) int {
	return compareInt(*left, *right)
}

func compareRecord(left, right Record) int {
	if left.Key < right.Key {
		return -7
	}
	if left.Key > right.Key {
		return 23
	}
	return 0
}

func compareRecordKey(record Record, key SearchKey) int {
	if record.Key < key.Key {
		return -7
	}
	if record.Key > key.Key {
		return 23
	}
	return 0
}

func compareRecordKeyRef(record *Record, key SearchKey) int {
	return compareRecordKey(*record, key)
}

// weirdEqual is intentionally non-transitive: adjacent values one apart are
// equal. It exercises Compact's control flow beyond ordinary equivalence.
func weirdEqual(left, right int) bool {
	if left == right {
		return true
	}
	if left < right {
		return right-left == 1
	}
	return left-right == 1
}
