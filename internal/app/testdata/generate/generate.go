package generate

//go:generate go tool monoslices
//monoslices:generate name=Foos cmp=compareFoo

type Foo struct {
	Key int
}

func compareFoo(a, b Foo) int { return a.Key - b.Key }
