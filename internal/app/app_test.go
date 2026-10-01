package app

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func TestRunEmptyDirectoryFailsPackageLoading(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if status := Run(context.Background(), dir, nil, &stdout, &stderr); status != 1 {
		t.Fatalf("Run() status = %d, want 1; stderr = %q", status, stderr.String())
	}
	if !strings.Contains(stderr.String(), "monoslices: package in") || !strings.Contains(stderr.String(), "has no production source package name") {
		t.Fatalf("source-discovery diagnostic = %q", stderr.String())
	}
}

func TestRunDoesNotChangeProcessWorkingDirectory(t *testing.T) {
	before, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	writeFixtureFile(t, cwd, "go.mod", "module fixture\n\ngo 1.24.0\n")
	writeFixtureFile(t, cwd, "specializations.go", `package fixture

type Foo struct{}
func isFoo(Foo) bool { return true }
//monoslices:generate name=Foos pred=isFoo ops=contains
`)
	var stdout, stderr bytes.Buffer
	if status := Run(context.Background(), cwd, nil, &stdout, &stderr); status != 0 {
		t.Fatalf("Run() status = %d, stderr = %q", status, stderr.String())
	}
	after, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("Run() changed process cwd from %q to %q", before, after)
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("successful Run wrote stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunUsageErrorsAndHelp(t *testing.T) {
	for _, args := range [][]string{{"-name=Foos"}, {"-pred=isFoo"}, {"-eq=equalFoo"}, {"-cmp=compareFoo"}, {"-byref=all"}, {"-ops=sort"}, {"-type=int"}, {"./..."}} {
		var stdout, stderr bytes.Buffer
		if status := Run(context.Background(), t.TempDir(), args, &stdout, &stderr); status != 2 {
			t.Errorf("Run(%v) status = %d, want 2; stderr=%q", args, status, stderr.String())
		}
		if !strings.HasPrefix(stderr.String(), "monoslices:") {
			t.Errorf("Run(%v) diagnostic = %q", args, stderr.String())
		}
	}
	for _, arg := range []string{"-h", "-help"} {
		var stdout, stderr bytes.Buffer
		if status := Run(context.Background(), t.TempDir(), []string{arg}, &stdout, &stderr); status != 0 {
			t.Fatalf("Run(%s) status=%d", arg, status)
		}
		if stderr.Len() != 0 || !strings.Contains(stdout.String(), "monoslices_gen.go") {
			t.Fatalf("help output stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
	}
}
