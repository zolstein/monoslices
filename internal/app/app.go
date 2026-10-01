// Package app contains the testable package-wide monoslices command runner.
package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/zolstein/monoslices/internal/config"
	"github.com/zolstein/monoslices/internal/gen"
	"github.com/zolstein/monoslices/internal/loadpkg"
	"github.com/zolstein/monoslices/internal/model"
)

// Run parses one package-wide monoslices invocation, discovers and parses the
// current package's source, builds its aggregate operation model, writes any
// generated declarations, and removes stale monoslices-owned files.
func Run(ctx context.Context, cwd string, args []string, stdout, stderr io.Writer) int {
	cfg, err := config.ParseCLI(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			config.PrintUsage(stdout)
			return 0
		}
		fmt.Fprintln(stderr, err)
		return 2
	}

	loaded, err := loadpkg.LoadPackage(ctx, cwd, cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	plan, err := model.BuildPackage(loaded)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if len(plan.Directives) != 0 {
		source, err := gen.GeneratePackage(plan)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if err := writeAtomic(loaded.OutputPath, source); err != nil {
			fmt.Fprintln(stderr, fmt.Errorf("monoslices: cannot write %q: %w", loaded.OutputPath, err))
			return 1
		}
	}
	if err := removeStaleGeneratedFiles(loaded.OwnedFiles, loaded.OutputPath, len(plan.Directives) != 0); err != nil {
		fmt.Fprintln(stderr, fmt.Errorf("monoslices: cannot remove stale generated files: %w", err))
		return 1
	}
	return 0
}

// removeStaleGeneratedFiles runs only after a successful write, or when the
// package has no declarations to generate. The output path is retained when
// this run produced a file.
func removeStaleGeneratedFiles(paths []string, outputPath string, wroteOutput bool) error {
	for _, path := range paths {
		if wroteOutput && filepath.Clean(path) == filepath.Clean(outputPath) {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// writeAtomic replaces the generated file only after complete source has been
// generated and formatted. The temporary file lives beside the target, so the
// final rename can provide an atomic replacement on platforms that support it.
// os.Rename does not guarantee atomicity on non-Unix platforms.
func writeAtomic(path string, source []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".monoslices-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(source); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
