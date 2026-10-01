package fuzzfixture

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/zolstein/monoslices/internal/app"
)

func TestGeneratedFixtureIsCurrent(t *testing.T) {
	fixtureDir := sourceDirectory(t)
	specializations, err := os.ReadFile(filepath.Join(fixtureDir, "specializations.go"))
	if err != nil {
		t.Fatal(err)
	}
	regeneratedDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(regeneratedDir, "go.mod"), []byte("module fixture.fuzz\n\ngo 1.24.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(regeneratedDir, "specializations.go"), specializations, 0o644); err != nil {
		t.Fatal(err)
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

func sourceDirectory(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(file)
}
