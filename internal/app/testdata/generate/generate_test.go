package generate

import "testing"

func TestGenerated(t *testing.T) {
	values := []Foo{{3}, {1}, {2}}
	FoosSort(values)
	if values[0].Key != 1 || values[1].Key != 2 || values[2].Key != 3 {
		t.Fatalf("sorted values = %#v", values)
	}
}
