package benchmarks

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/zolstein/monoslices/internal/app"
)

func TestGeneratedBenchmarkCasesAreCurrent(t *testing.T) {
	fixtureDir := sourceDirectory(t)
	generator, err := os.ReadFile(filepath.Join(fixtureDir, "internal", "casegen", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), generator, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "run", "main.go")
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate benchmark cases: %v: %s", err, output)
	}
	got, err := os.ReadFile(filepath.Join(dir, "cases_generated_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(fixtureDir, "cases_generated_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("generated benchmark cases differ from checked-in cases_generated_test.go")
	}
}

func TestGeneratedFixtureIsCurrentAndDeterministic(t *testing.T) {
	fixtureDir := sourceDirectory(t)
	regeneratedDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(regeneratedDir, "go.mod"), []byte("module fixture.bench\n\ngo 1.24.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"record_sizes.go"} {
		source, err := os.ReadFile(filepath.Join(fixtureDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(regeneratedDir, name), source, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var stderr bytes.Buffer
	if status := app.Run(context.Background(), regeneratedDir, nil, bytes.NewBuffer(nil), &stderr); status != 0 {
		t.Fatalf("regeneration returned status %d: %s", status, stderr.String())
	}
	outputPath := filepath.Join(regeneratedDir, "monoslices_gen.go")
	first, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if status := app.Run(context.Background(), regeneratedDir, nil, bytes.NewBuffer(nil), &stderr); status != 0 {
		t.Fatalf("second regeneration returned status %d: %s", status, stderr.String())
	}
	second, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("repeated aggregate regeneration changed output bytes")
	}

	want, err := os.ReadFile(filepath.Join(fixtureDir, "monoslices_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, want) {
		t.Fatal("generated output differs from checked-in monoslices_gen.go")
	}
}
