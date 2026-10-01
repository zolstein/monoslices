package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zolstein/monoslices/internal/config"
)

// integrationFixture exercises the actual package-wide command pipeline in a
// temporary module. Source directives, rather than command flags, define APIs.
type integrationFixture struct{ dir string }

type fixtureRun struct {
	status     int
	stdout     string
	stderr     string
	outputPath string
	source     []byte
}

func newIntegrationFixture(t *testing.T, files map[string]string) *integrationFixture {
	t.Helper()
	fixture := &integrationFixture{dir: t.TempDir()}
	writeFixtureFile(t, fixture.dir, "go.mod", "module fixture\n\ngo 1.24.0\n")
	for name, content := range files {
		writeFixtureFile(t, fixture.dir, name, content)
	}
	return fixture
}

func (fixture *integrationFixture) run(args ...string) fixtureRun {
	var stdout, stderr bytes.Buffer
	status := Run(context.Background(), fixture.dir, args, &stdout, &stderr)
	result := fixtureRun{status: status, stdout: stdout.String(), stderr: stderr.String()}
	cfg, err := config.ParseCLI(args)
	if err == nil {
		name := cfg.Out
		if name == "" {
			name = config.DefaultOutputFilename
		}
		if !filepath.IsAbs(name) {
			name = filepath.Join(fixture.dir, name)
		}
		result.outputPath = filepath.Clean(name)
		if status == 0 {
			result.source, _ = os.ReadFile(result.outputPath)
		}
	}
	return result
}

func (fixture *integrationFixture) generate(t *testing.T, args ...string) fixtureRun {
	t.Helper()
	result := fixture.run(args...)
	if result.status != 0 {
		t.Fatalf("monoslices %v failed with status %d: %s", args, result.status, result.stderr)
	}
	if len(result.source) == 0 {
		t.Fatalf("monoslices %v did not produce %q", args, result.outputPath)
	}
	return result
}

func (fixture *integrationFixture) test(t *testing.T) {
	t.Helper()
	result := fixture.command(t, "go", "test", "./...")
	if result.err != nil {
		t.Fatalf("fixture go test: %v\n%s", result.err, result.text)
	}
}

func (fixture *integrationFixture) generateAndTest(t *testing.T, args ...string) fixtureRun {
	t.Helper()
	result := fixture.generate(t, args...)
	fixture.test(t)
	return result
}

func (fixture *integrationFixture) rewrite(t *testing.T, name, content string) {
	t.Helper()
	writeFixtureFile(t, fixture.dir, name, content)
}

type fixtureCommandResult struct {
	text string
	err  error
}

func (fixture *integrationFixture) command(t *testing.T, name string, args ...string) fixtureCommandResult {
	return fixture.commandWithEnv(t, nil, name, args...)
}

func (fixture *integrationFixture) commandWithEnv(t *testing.T, environment []string, name string, args ...string) fixtureCommandResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = fixture.dir
	command.Env = append(os.Environ(), "GOWORK=off")
	for _, override := range environment {
		key, _, ok := strings.Cut(override, "=")
		if !ok {
			continue
		}
		prefix := key + "="
		filtered := command.Env[:0]
		for _, existing := range command.Env {
			if !strings.HasPrefix(existing, prefix) {
				filtered = append(filtered, existing)
			}
		}
		command.Env = filtered
	}
	command.Env = append(command.Env, environment...)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return fixtureCommandResult{text: string(output), err: fmt.Errorf("%s: %w", name, ctx.Err())}
	}
	return fixtureCommandResult{text: string(output), err: err}
}

func TestIntegrationPackageWideSuccessAndCompilation(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"a_specializations.go": `package fixture

type A struct{ N int }
func pred(a A) bool { return a.N > 0 }
func valuePred(a A) bool { return a.N > 0 }
func equal(a, b A) bool { return a.N == b.N }
func compare(a, b A) int { return a.N - b.N }
func comparePtr(a, b *A) int { return a.N - b.N }
//monoslices:generate name=As pred=pred eq=equal cmp=compare
//monoslices:generate name=Split pred=pred ops=contains
//monoslices:generate name=Split eq=equal ops=equal
//monoslices:generate name=SplitCmp cmp=compare ops=sort
//monoslices:generate name=SplitCmp cmp=compare ops=binary-search
//monoslices:generate name=Mixed pred=valuePred ops=contains
//monoslices:generate name=Mixed cmp=comparePtr byref=true ops=compare
`,
		"b_specializations.go": `package fixture

type B struct{ N int }
func compareB(a, b B) int { return a.N - b.N }
//monoslices:generate name=Bs cmp=compareB ops=sort,sort-stable,binary-search
`,
		"fixture_test.go": `package fixture
import "testing"
func TestGenerated(t *testing.T) {
	if !AsContains([]A{{1}}) || !AsEqual([]A{{1}}, []A{{1}}) || AsCompare([]A{{1}}, []A{{2}}) >= 0 { t.Fatal("As specialization functions") }
	values := []B{{3}, {1}, {2}}; BsSort(values); BsSortStable(values)
	if values[0].N != 1 || values[2].N != 3 { t.Fatal(values) }
	if i, ok := BsBinarySearch(values, B{2}); i != 1 || !ok { t.Fatal(i, ok) }
}
`,
	})
	result := fixture.generateAndTest(t)
	if filepath.Base(result.outputPath) != config.DefaultOutputFilename {
		t.Fatalf("default output = %q", result.outputPath)
	}
	for _, name := range []string{"AsContains", "AsCompact", "AsEqual", "AsCompare", "BsSort", "BsSortStable", "BsBinarySearch", "SplitContains", "SplitEqual", "SplitCmpSort", "SplitCmpBinarySearch", "MixedContains", "MixedCompare"} {
		if !strings.Contains(string(result.source), "func "+name) {
			t.Errorf("aggregate output lacks %s", name)
		}
	}
	if strings.Count(string(result.source), "Code generated by monoslices") != 1 {
		t.Fatalf("aggregate header repeated")
	}
	if strings.Contains(string(result.source), "type=") {
		t.Fatalf("generated source contains obsolete type= syntax:\n%s", result.source)
	}
}

func TestIntegrationRegeneratesAfterDeletingOutputUsedByProductionSource(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"records.go": `package fixture

type A int

func compare(a, b A) int {
	return int(a - b)
}

//monoslices:generate name=As cmp=compare ops=sort

func SortAs(values []A) {
	AsSort(values)
}
`,
		"fixture_test.go": `package fixture

import "testing"

func TestSortAs(t *testing.T) {
	values := []A{3, 1, 2}
	SortAs(values)
	if values[0] != 1 || values[1] != 2 || values[2] != 3 {
		t.Fatalf("SortAs = %v", values)
	}
}
`,
	})
	outputPath := filepath.Join(fixture.dir, config.DefaultOutputFilename)
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("generated output exists before first generation: err=%v", err)
	}

	first := fixture.generate(t)
	if !strings.Contains(string(first.source), "func AsSort(s []A)") {
		t.Fatalf("first generation lacks AsSort:\n%s", first.source)
	}
	fixture.test(t)

	if err := os.Remove(first.outputPath); err != nil {
		t.Fatalf("delete generated output: %v", err)
	}
	if _, err := os.Stat(first.outputPath); !os.IsNotExist(err) {
		t.Fatalf("generated output remains after deletion: err=%v", err)
	}

	second := fixture.generate(t)
	if !strings.Contains(string(second.source), "func AsSort(s []A)") {
		t.Fatalf("regeneration lacks AsSort:\n%s", second.source)
	}
	fixture.test(t)
}

func TestIntegrationGeneratesWithoutLoadingIrrelevantDefaultImport(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"records.go": `package fixture

import "example.invalid/unavailable"

type Item int
func compare(a, b Item) int { return int(a - b) }
func irrelevant() { unavailable.Touch() }
//monoslices:generate name=Items cmp=compare ops=compare
`,
	})
	result := fixture.run()
	if result.status != 0 {
		t.Fatalf("monoslices failed with status %d: %s", result.status, result.stderr)
	}
	if !strings.Contains(string(result.source), "func ItemsCompare(a []Item, b []Item) int") {
		t.Fatalf("generated source lacks ItemsCompare:\n%s", result.source)
	}
	for _, loadingError := range []string{"example.invalid", "no required module provides package", "cannot find module", "failed to load"} {
		if strings.Contains(strings.ToLower(result.stderr), strings.ToLower(loadingError)) {
			t.Fatalf("monoslices reported an import/dependency loading error %q: %s", loadingError, result.stderr)
		}
	}
}

func TestIntegrationGeneratesWithPermissiveSourceTypeErrors(t *testing.T) {
	for _, test := range []struct {
		name          string
		source        string
		wantGenerated string
	}{
		{
			name: "unrelated undefined function",
			source: `package fixture

type Item int
func compare(a, b Item) int { return int(a - b) }
func unfinished() { missing() }
//monoslices:generate name=Items cmp=compare ops=compare
`,
			wantGenerated: "func ItemsCompare(a []Item, b []Item) int",
		},
		{
			name: "invalid specialization body",
			source: `package fixture

type Item int
func compare(a, b Item) int { return unresolved(a, b) }
//monoslices:generate name=Items cmp=compare ops=compare
`,
			wantGenerated: "func ItemsCompare(a []Item, b []Item) int",
		},
		{
			name: "unresolved specialization parameter type",
			source: `package fixture

func compare(a, b FutureType) int { return 0 }
//monoslices:generate name=Items cmp=compare ops=compare
`,
			wantGenerated: "func ItemsCompare(a []FutureType, b []FutureType) int",
		},
		{
			name: "invalid generated operation call",
			source: `package fixture

type Item int
func compare(a, b Item) int { return int(a - b) }
func use(values []Item) { ItemsSort(values, values) }
//monoslices:generate name=Items cmp=compare ops=sort
`,
			wantGenerated: "func ItemsSort(s []Item)",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newIntegrationFixture(t, map[string]string{"records.go": test.source})
			result := fixture.run()
			if result.status != 0 {
				t.Fatalf("monoslices rejected permissive source with status %d: %s", result.status, result.stderr)
			}
			if !strings.Contains(string(result.source), test.wantGenerated) {
				t.Fatalf("generated source lacks %q:\n%s", test.wantGenerated, result.source)
			}
		})
	}
}

func TestIntegrationRejectsDotImportAndMalformedGoSyntax(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "dot import",
			source: `package fixture

import . "fmt"

type Item int
func compare(a, b Item) int { return int(a - b) }
//monoslices:generate name=Items cmp=compare ops=compare
`,
			want: "dot imports are not supported when generation directives are present",
		},
		{
			name: "malformed Go syntax",
			source: `package fixture

type Item int
func compare(a, b Item) int { return int(a - b)
//monoslices:generate name=Items cmp=compare ops=compare
`,
			want: `cannot parse source file "records.go"`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newIntegrationFixture(t, map[string]string{"records.go": test.source})
			result := fixture.run()
			if result.status != 1 || !strings.Contains(result.stderr, test.want) {
				t.Fatalf("status=%d stderr=%q, want diagnostic containing %q", result.status, result.stderr, test.want)
			}
			if _, err := os.Stat(filepath.Join(fixture.dir, config.DefaultOutputFilename)); !os.IsNotExist(err) {
				t.Fatalf("invalid source created generated output: err=%v", err)
			}
		})
	}
}

func TestIntegrationRejectsSamePackageTestDeclarationConflict(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"records.go": `package fixture

type Item int
func compare(left, right Item) int { return int(left - right) }
//monoslices:generate name=Items cmp=compare ops=sort
`,
		"records_test.go": `package fixture

func ItemsSort() {}
`,
	})
	result := fixture.run()
	if result.status != 1 || !strings.Contains(result.stderr, "ItemsSort") || !strings.Contains(result.stderr, "records.go:") || !strings.Contains(result.stderr, "records_test.go:") {
		t.Fatalf("status=%d stderr=%q, want same-package test declaration conflict with both locations", result.status, result.stderr)
	}
	if _, err := os.Stat(filepath.Join(fixture.dir, config.DefaultOutputFilename)); !os.IsNotExist(err) {
		t.Fatalf("conflicting generation created output: err=%v", err)
	}
}

func TestIntegrationExternalTestPackageRemainsIndependent(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"records.go": `package fixture

type Item int
func compare(left, right Item) int { return int(left - right) }
//monoslices:generate name=Items cmp=compare ops=sort
`,
		"records_test.go": `package fixture_test

func ItemsSort() {}
`,
	})
	result := fixture.generateAndTest(t)
	if !strings.Contains(string(result.source), "func ItemsSort") {
		t.Fatalf("generated output lacks ItemsSort:\n%s", result.source)
	}
}

func TestIntegrationRejectsCrossFileSpecFunc(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"records.go": `package fixture

//monoslices:generate name=Items cmp=compare ops=compare
`,
		"specializations.go": `package fixture

func compare(left, right int) int { return left - right }
`,
	})
	result := fixture.run()
	if result.status != 1 || !strings.Contains(result.stderr, "cmp=compare must be declared in the same source file as the monoslices directive") {
		t.Fatalf("status=%d stderr=%q, want same-file specialization-function diagnostic", result.status, result.stderr)
	}
}

func TestIntegrationAcceptsSpecFuncBeforeDirective(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"records.go": `package fixture

type Item int
func compare(left, right Item) int { return int(left - right) }

//monoslices:generate name=Items cmp=compare ops=compare
`,
		"fixture_test.go": `package fixture

import "testing"

func TestGenerated(t *testing.T) {
	if ItemsCompare([]Item{1}, []Item{2}) >= 0 {
		t.Fatal("unexpected comparison")
	}
}
`,
	})
	result := fixture.generateAndTest(t)
	if !strings.Contains(string(result.source), "func ItemsCompare(a []Item, b []Item) int") {
		t.Fatalf("generated output lacks ItemsCompare:\n%s", result.source)
	}
}

func TestIntegrationAcceptsSpecFuncAfterDirective(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"records.go": `package fixture

type Item int

//monoslices:generate name=Items cmp=compare ops=compare

func compare(left, right Item) int { return int(left - right) }
`,
		"fixture_test.go": `package fixture

import "testing"

func TestGenerated(t *testing.T) {
	if ItemsCompare([]Item{1}, []Item{2}) >= 0 {
		t.Fatal("unexpected comparison")
	}
}
`,
	})
	result := fixture.generateAndTest(t)
	if !strings.Contains(string(result.source), "func ItemsCompare(a []Item, b []Item) int") {
		t.Fatalf("generated output lacks ItemsCompare:\n%s", result.source)
	}
}

func TestIntegrationCompilesMultipleSemanticSpecFuncs(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"specializations.go": `package fixture

type Item int
func pred(item Item) bool { return item > 0 }
func equal(left, right Item) bool { return left == right }
func compare(left, right Item) int { return int(left - right) }
//monoslices:generate name=Items pred=pred eq=equal cmp=compare ops=contains,equal,compare
`,
		"fixture_test.go": `package fixture

import "testing"

func TestGenerated(t *testing.T) {
	values := []Item{1, 2}
	if !ItemsContains(values) || !ItemsEqual(values, []Item{1, 2}) || ItemsCompare(values, []Item{2, 3}) >= 0 {
		t.Fatal("generated specializations")
	}
}
`,
	})
	fixture.generateAndTest(t)
}

func TestIntegrationRejectsCrossFileSemanticSpecFunc(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"records.go": `package fixture

type Item int

func pred(item Item) bool { return item > 0 }
func equal(left, right Item) bool { return left == right }

//monoslices:generate name=Items pred=pred eq=equal cmp=compare ops=contains,equal,compare
`,
		"specializations.go": `package fixture

func compare(left, right Item) int { return int(left - right) }
`,
	})
	result := fixture.run()
	if result.status != 1 || !strings.Contains(result.stderr, "cmp=compare must be declared in the same source file as the monoslices directive") {
		t.Fatalf("status=%d stderr=%q, want same-file specialization-function diagnostic", result.status, result.stderr)
	}
}

func TestIntegrationPreservesPlatformSpecificArrayLengthSpecFuncTypeAcrossGOOS(t *testing.T) {
	t.Setenv("GOOS", "linux")
	t.Setenv("GOARCH", runtime.GOARCH)
	fixture := newIntegrationFixture(t, map[string]string{
		"records.go": `package fixture

func compare(left, right [N]int) int { return 0 }
//monoslices:generate name=Items cmp=compare ops=compare
`,
		"length_linux.go": `package fixture

const N = 4
`,
		"length_windows.go": `package fixture

const N = 8
`,
	})
	result := fixture.generate(t)
	if !strings.Contains(string(result.source), "[N]int") {
		t.Fatalf("generated output did not preserve symbolic array length:\n%s", result.source)
	}
	for _, goos := range []string{"linux", "windows"} {
		build := fixture.commandWithEnv(t, []string{"GOOS=" + goos, "GOARCH=" + runtime.GOARCH}, "go", "build", "./...")
		if build.err != nil {
			t.Fatalf("generated package does not build for GOOS=%s: %v\n%s", goos, build.err, build.text)
		}
	}
}

func TestIntegrationPreservesSymbolicArrayLengthAcrossBuildTags(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"records.go": `package fixture

func compare(left, right [N]int) int { return left[0] - right[0] }
//monoslices:generate name=Items cmp=compare ops=compare
`,
		"constants_default.go": `//go:build !alternate

package fixture

const N = 4
`,
		"constants_alternate.go": `//go:build alternate

package fixture

const N = 8
`,
	})
	result := fixture.generate(t)
	if !strings.Contains(string(result.source), "[N]int") {
		t.Fatalf("generated output did not preserve symbolic array length:\n%s", result.source)
	}
	for _, args := range [][]string{{"go", "build", "./..."}, {"go", "build", "-tags", "alternate", "./..."}} {
		build := fixture.command(t, args[0], args[1:]...)
		if build.err != nil {
			t.Fatalf("generated package does not build with %v: %v\n%s", args, build.err, build.text)
		}
	}
}

func TestIntegrationPreservesBuildVaryingLocalTypeAcrossGOOS(t *testing.T) {
	t.Setenv("GOOS", "linux")
	t.Setenv("GOARCH", runtime.GOARCH)
	fixture := newIntegrationFixture(t, map[string]string{
		"records.go": `package fixture

func compare(left, right Item) int { return 0 }
//monoslices:generate name=Items cmp=compare ops=compare
`,
		"item_linux.go": `package fixture

type Item int
`,
		"item_windows.go": `package fixture

type Item int64
`,
	})
	result := fixture.generate(t)
	if !strings.Contains(string(result.source), "func ItemsCompare(a []Item, b []Item)") {
		t.Fatalf("generated output did not preserve local symbolic type:\n%s", result.source)
	}
	for _, goos := range []string{"linux", "windows"} {
		build := fixture.commandWithEnv(t, []string{"GOOS=" + goos, "GOARCH=" + runtime.GOARCH}, "go", "build", "./...")
		if build.err != nil {
			t.Fatalf("generated package does not build for GOOS=%s: %v\n%s", goos, build.err, build.text)
		}
	}
}

func TestIntegrationPreservesImportedGenericSpecFuncType(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"helper/box.go": `package helper

type Box[T any] struct{ Value T }
`,
		"records.go": `package fixture

import helper "fixture/helper"

type platformInt int
func compare(left, right helper.Box[platformInt]) int {
	return int(left.Value - right.Value)
}
//monoslices:generate name=Items cmp=compare ops=compare
`,
	})
	result := fixture.generateAndTest(t)
	generated := string(result.source)
	if !strings.Contains(generated, "helper.Box[platformInt]") {
		t.Fatalf("generated output did not preserve imported generic source type:\n%s", generated)
	}
	if !strings.Contains(generated, `"fixture/helper"`) {
		t.Fatalf("generated output did not import helper package:\n%s", generated)
	}
}

func TestIntegrationPreservesExplicitImportedSpecFuncType(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"model/item.go": `package model

type Key string
type Value int
`,
		"specializations.go": `package fixture

import model "fixture/model"

func compare(left, right map[model.Key]model.Value) int { return 0 }
//monoslices:generate name=Items cmp=compare ops=compare
`,
		"fixture_test.go": `package fixture

import (
	"testing"
	model "fixture/model"
)

func TestGeneratedImportedType(t *testing.T) {
	left := []map[model.Key]model.Value{{"left": 1}}
	right := []map[model.Key]model.Value{{"right": 2}}
	if ItemsCompare(left, right) != 0 {
		t.Fatal("unexpected comparison")
	}
}
`,
	})
	result := fixture.generateAndTest(t)
	generated := string(result.source)
	if !strings.Contains(generated, `model "fixture/model"`) || !strings.Contains(generated, "[]map[model.Key]model.Value") {
		t.Fatalf("generated source did not preserve explicit source-type alias and nested type:\n%s", generated)
	}
}

func TestIntegrationRejectsDefaultImportedSpecFuncTypeAndPreservesOutput(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"model/item.go": `package model

type Item int
`,
		"specializations.go": `package fixture

import "fixture/model"

func compare(left, right model.Item) int { return int(left - right) }
//monoslices:generate name=Items cmp=compare ops=compare
`,
	})
	path := filepath.Join(fixture.dir, config.DefaultOutputFilename)
	const sentinel = "package fixture\n\nfunc sentinel() {}\n"
	writeFixtureFile(t, fixture.dir, config.DefaultOutputFilename, sentinel)
	result := fixture.run()
	if result.status != 1 || !strings.Contains(result.stderr, "imported qualifiers used in specialization function parameter types must have explicit aliases") {
		t.Fatalf("status=%d stderr=%q, want unresolved explicit-alias diagnostic", result.status, result.stderr)
	}
	if got := strings.Count(result.stderr, "monoslices:"); got != 1 {
		t.Fatalf("diagnostic contains %d monoslices prefixes: %q", got, result.stderr)
	}
	if !strings.Contains(result.stderr, "specializations.go:5:26: monoslices:") {
		t.Fatalf("diagnostic does not point at specialization function type: %q", result.stderr)
	}
	if strings.Contains(result.stderr, fixture.dir) || strings.Contains(result.stderr, string(filepath.Separator)+"tmp"+string(filepath.Separator)) {
		t.Fatalf("diagnostic exposes an absolute temporary path: %q", result.stderr)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != sentinel {
		t.Fatalf("default-import failure replaced output: %q", got)
	}
}

func TestIntegrationAllowsDefaultImportBehindLocalSymbolicSpecFuncType(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"model/item.go": `package model

type Item int
`,
		"specializations.go": `package fixture

import "fixture/model"

type Item = model.Item
func compare(left, right Item) int { return int(left - right) }
//monoslices:generate name=Items cmp=compare ops=compare
`,
	})
	result := fixture.generateAndTest(t)
	generated := string(result.source)
	if !strings.Contains(generated, "[]Item") || strings.Contains(generated, `"fixture/model"`) {
		t.Fatalf("generated source did not preserve local symbolic type:\n%s", generated)
	}
}

func TestIntegrationAllowsDefaultImportUsedOnlyBySpecFuncBody(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"specializations.go": `package fixture

import "fmt"

type Item int
func compare(left, right Item) int {
	_ = fmt.Sprint(left, right)
	return int(left - right)
}
//monoslices:generate name=Items cmp=compare ops=compare
`,
	})
	result := fixture.generateAndTest(t)
	if strings.Contains(string(result.source), `"fmt"`) {
		t.Fatalf("generated source imported specialization function-body dependency:\n%s", result.source)
	}
}

func TestIntegrationCompilesHeterogeneousSpecFuncOperations(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"records.go": `package fixture

type Left = int
type Right = int
func compare(left Left, right Right) int { return left - right }
//monoslices:generate name=Items cmp=compare ops=compare,binary-search
`,
		"records_test.go": `package fixture

import "testing"

func TestGeneratedHeterogeneous(t *testing.T) {
	if ItemsCompare([]Left{1}, []Right{2}) >= 0 {
		t.Fatal("unexpected comparison")
	}
	if index, ok := ItemsBinarySearch([]Left{1, 2, 3}, Right(2)); index != 1 || !ok {
		t.Fatal(index, ok)
	}
}
`,
	})
	fixture.generateAndTest(t)
}

func TestIntegrationBuildsPlatformImplementationBehindUnconditionalSpecFunc(t *testing.T) {
	t.Setenv("GOOS", "linux")
	t.Setenv("GOARCH", runtime.GOARCH)
	fixture := newIntegrationFixture(t, map[string]string{
		"specializations.go": `package fixture

type Item int

func compare(left, right Item) int {
	return platformCompare(left, right)
}
//monoslices:generate name=Items cmp=compare ops=compare
`,
		"platform_linux.go": `package fixture

func platformCompare(left, right Item) int { return int(left - right) }
`,
		"platform_windows.go": `package fixture

func platformCompare(left, right Item) int { return int(right - left) }
`,
	})
	result := fixture.generate(t)
	if !strings.Contains(string(result.source), "func ItemsCompare(a []Item, b []Item) int") {
		t.Fatalf("generated output did not preserve local specialization function type:\n%s", result.source)
	}
	for _, goos := range []string{"linux", "windows"} {
		build := fixture.commandWithEnv(t, []string{"GOOS=" + goos, "GOARCH=" + runtime.GOARCH}, "go", "build", "./...")
		if build.err != nil {
			t.Fatalf("generated package does not build for GOOS=%s: %v\n%s", goos, build.err, build.text)
		}
	}
}

func TestIntegrationRejectsCgoSourceSpecFuncAsCrossFile(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"records.go": `package fixture

//monoslices:generate name=Items cmp=compare ops=compare
`,
		"compare_cgo.go": `package fixture

/*
#include <stdint.h>
*/
import "C"

func compare(left, right C.int) int { return int(left - right) }
`,
	})
	result := fixture.run()
	if result.status != 1 || !strings.Contains(result.stderr, "cmp=compare must be declared in the same source file as the monoslices directive") {
		t.Fatalf("status=%d stderr=%q, want same-file specialization-function diagnostic", result.status, result.stderr)
	}
	if _, err := os.Stat(filepath.Join(fixture.dir, config.DefaultOutputFilename)); !os.IsNotExist(err) {
		t.Fatalf("rejected cgo specialization output created output: err=%v", err)
	}
}

func TestIntegrationRejectsDirectiveInCgoSource(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"cgo.go": `package fixture

/*
#include <stdint.h>
*/
import "C"

type Item int
func compare(left, right Item) int { return int(left - right) }
//monoslices:generate name=Items cmp=compare ops=compare
`,
	})
	result := fixture.run()
	if result.status != 1 || !strings.Contains(result.stderr, "cgo.go") || !strings.Contains(result.stderr, "build-constrained files are not supported") {
		t.Fatalf("status=%d stderr=%q, want cgo build-constraint diagnostic", result.status, result.stderr)
	}
	if _, err := os.Stat(filepath.Join(fixture.dir, config.DefaultOutputFilename)); !os.IsNotExist(err) {
		t.Fatalf("rejected cgo directive created output: err=%v", err)
	}
}

func TestIntegrationInactiveCgoSourceDoesNotBlockGeneration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the cgo source is active on Windows")
	}
	fixture := newIntegrationFixture(t, map[string]string{
		"specializations.go": `package fixture

type Item int
func compare(left, right Item) int { return int(left - right) }
//monoslices:generate name=Items cmp=compare ops=compare
`,
		"cgo_windows.go": `//go:build windows && cgo

package fixture

/*
#include <stdint.h>
*/
import "C"

var _ = C.int(0)
`,
	})
	result := fixture.generateAndTest(t)
	if !strings.Contains(string(result.source), "func ItemsCompare") {
		t.Fatalf("aggregate output lacks ItemsCompare:\n%s", result.source)
	}
}

func TestIntegrationSpecFuncNamesDoNotShadowGeneratedLocals(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"specializations.go": `package fixture

type A int
func i(value A) bool { return value > 0 }

type Item struct{ Key int }
func data(left, right Item) int { return left.Key - right.Key }
func k(left, right Item) int { return left.Key - right.Key }

//monoslices:generate name=As pred=i ops=contains
//monoslices:generate name=Items cmp=data ops=sort
//monoslices:generate name=Stable cmp=k ops=sort-stable
`,
		"fixture_test.go": `package fixture

import "testing"

func TestGeneratedCollidingSpecFuncs(t *testing.T) {
	if !AsContains([]A{1}) {
		t.Fatal("contains")
	}
	values := []Item{{Key: 3}, {Key: 1}, {Key: 2}}
	ItemsSort(values)
	if values[0].Key != 1 || values[1].Key != 2 || values[2].Key != 3 {
		t.Fatalf("sorted values = %#v", values)
	}
	stable := []Item{{Key: 2}, {Key: 1}, {Key: 2}}
	StableSortStable(stable)
	if stable[0].Key != 1 || stable[1].Key != 2 || stable[2].Key != 2 {
		t.Fatalf("stable values = %#v", stable)
	}
}
`,
	})
	result := fixture.generateAndTest(t)
	source := string(result.source)
	if !strings.Contains(source, "for i_ := range s") {
		t.Fatalf("ordinary generated local was not renamed:\n%s", source)
	}
	if !strings.Contains(source, "if i(s[i_])") {
		t.Fatalf("ordinary function call was not direct:\n%s", source)
	}
	if !strings.Contains(source, "func monoslices_ItemsSort_InsertionSort_helper(data_ []Item") || !strings.Contains(source, "data(data_[j], data_[j-1])") {
		t.Fatalf("sorting comparator was not kept unshadowed:\n%s", source)
	}
	if !strings.Contains(source, "func monoslices_StableSortStable_SymMerge_helper(data []Item, a, m, b int)") || !strings.Contains(source, "for k_ := a; k_ < i-1; k_++") || !strings.Contains(source, "k(data[h], data[a])") {
		t.Fatalf("stable sorting comparator was not kept unshadowed:\n%s", source)
	}
}

func TestIntegrationExternalTypesAcrossDirectives(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"one/value.go": "package shared\ntype Value int\n",
		"two/value.go": "package shared\ntype Value int\n",
		"specializations.go": `package fixture
import one "fixture/one"
import two "fixture/two"
func compareOne(a,b one.Value) int { return int(a-b) }
func compareTwo(a,b two.Value) int { return int(a-b) }
//monoslices:generate name=One cmp=compareOne ops=compare
//monoslices:generate name=Two cmp=compareTwo ops=compare
`,
		"fixture_test.go": `package fixture
import (
	"testing"
	one "fixture/one"
	two "fixture/two"
)
func TestExternalGenerated(t *testing.T) {
	if OneCompare([]one.Value{1}, []one.Value{2}) >= 0 { t.Fatal("one") }
	if TwoCompare([]two.Value{1}, []two.Value{2}) >= 0 { t.Fatal("two") }
}
`,
	})
	result := fixture.generateAndTest(t)
	if !strings.Contains(string(result.source), `one "fixture/one"`) || !strings.Contains(string(result.source), `two "fixture/two"`) {
		t.Fatalf("aggregate imports did not preserve source-type aliases:\\n%s", result.source)
	}
}

func TestIntegrationPreservesSourceTypeImportAliasWhenDependencyUsesPredeclaredPackageName(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"dependency/value.go": `package string

type Value int
`,
		"specializations.go": `package fixture

import s "fixture/dependency"

func compare(left, right map[string]s.Value) int { return 0 }
//monoslices:generate name=Items cmp=compare ops=compare
`,
		"fixture_test.go": `package fixture

import (
	"testing"
	s "fixture/dependency"
)

func TestGeneratedAlias(t *testing.T) {
	if ItemsCompare([]map[string]s.Value{{"one": 1}}, []map[string]s.Value{{"two": 2}}) != 0 {
		t.Fatal("comparison")
	}
}
`,
	})
	result := fixture.generateAndTest(t)
	source := string(result.source)
	if !strings.Contains(source, `s "fixture/dependency"`) || !strings.Contains(source, "[]map[string]s.Value") {
		t.Fatalf("generated source did not preserve source-type alias and predeclared type:\\n%s", source)
	}
}

func TestIntegrationPreservesDifferentAliasesForOneDependencyPath(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"dependency/value.go": "package helper\ntype Value int\n",
		"first.go": `package fixture

import h "fixture/dependency"

func compareFirst(left, right h.Value) int { return int(left - right) }
//monoslices:generate name=First cmp=compareFirst ops=compare
`,
		"second.go": `package fixture

import helper "fixture/dependency"

func compareSecond(left, right helper.Value) int { return int(left - right) }
//monoslices:generate name=Second cmp=compareSecond ops=compare
`,
		"fixture_test.go": `package fixture

import (
	"testing"
	h "fixture/dependency"
)

func TestGeneratedAliases(t *testing.T) {
	if FirstCompare([]h.Value{1}, []h.Value{2}) >= 0 || SecondCompare([]h.Value{1}, []h.Value{2}) >= 0 {
		t.Fatal("comparison")
	}
}
`,
	})
	result := fixture.generateAndTest(t)
	source := string(result.source)
	if !strings.Contains(source, `h "fixture/dependency"`) || !strings.Contains(source, `helper "fixture/dependency"`) {
		t.Fatalf("generated source did not preserve both aliases:\\n%s", source)
	}
}

func TestIntegrationRejectsConflictingSourceTypeImportAliases(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"one/value.go": "package one\ntype Value int\n",
		"two/value.go": "package two\ntype Value int\n",
		"first.go": `package fixture

import h "fixture/one"

func compareFirst(left, right h.Value) int { return 0 }
//monoslices:generate name=First cmp=compareFirst ops=compare
`,
		"second.go": `package fixture

import h "fixture/two"

func compareSecond(left, right h.Value) int { return 0 }
//monoslices:generate name=Second cmp=compareSecond ops=compare
`,
	})
	result := fixture.run()
	if result.status == 0 || !strings.Contains(result.stderr, `source-type import alias "h"`) || !strings.Contains(result.stderr, "fixture/one") || !strings.Contains(result.stderr, "fixture/two") {
		t.Fatalf("generation status=%d stderr=%q, want aggregate specialization function-alias conflict", result.status, result.stderr)
	}
}

func TestIntegrationByrefSpecFuncsAndPackageWideGeneration(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"specializations.go": `package fixture

type A struct {
	N     int
	Token *int
}

var firstSlots map[*A]struct{}
var secondSlots map[*A]struct{}
var searchTarget *A

func requireSlot(kind string, value *A, slots map[*A]struct{}) {
	if value == nil {
		panic(kind + " specialization function received nil")
	}
	if _, ok := slots[value]; !ok {
		panic(kind + " specialization function did not receive a live slice slot")
	}
}

func pred(value (*A)) bool {
	requireSlot("predicate", value, firstSlots)
	return value.N == 2
}

func equal(left, right *A) bool {
	requireSlot("equality left", left, firstSlots)
	requireSlot("equality right", right, secondSlots)
	return left.N == right.N
}

func compare(left, right *A) int {
	requireSlot("comparator left", left, firstSlots)
	if searchTarget != nil {
		if right != searchTarget {
			panic("binary-search target pointer was not passed unchanged")
		}
	} else {
		requireSlot("comparator right", right, secondSlots)
	}
	return left.N - right.N
}

//monoslices:generate name=As pred=pred eq=equal cmp=compare byref=true
`,
		"fixture_test.go": `package fixture

import "testing"

func record(n int) A {
	token := new(int)
	*token = n
	return A{N: n, Token: token}
}

func records(values ...int) []A {
	result := make([]A, len(values))
	for i, value := range values {
		result[i] = record(value)
	}
	return result
}

func slots(s []A) map[*A]struct{} {
	result := make(map[*A]struct{}, len(s))
	for i := range s {
		result[&s[i]] = struct{}{}
	}
	return result
}

func trackOne(s []A) {
	firstSlots = slots(s)
	secondSlots = firstSlots
	searchTarget = nil
}

func trackTwo(left, right []A) {
	firstSlots = slots(left)
	secondSlots = slots(right)
	searchTarget = nil
}

func trackSearch(s []A, target *A) {
	firstSlots = slots(s)
	secondSlots = nil
	searchTarget = target
}

func TestByrefOperations(t *testing.T) {
	s := records(1, 2, 2, 3)
	trackOne(s)
	if !AsContains(s) {
		t.Fatal("contains")
	}

	s = records(1, 2, 2, 3)
	trackOne(s)
	if got := AsIndex(s); got != 1 {
		t.Fatalf("index = %d, want 1", got)
	}

	s = records(1, 2, 2, 3)
	trackOne(s)
	deleted := AsDelete(s)
	if len(deleted) != 2 || deleted[0].N != 1 || deleted[1].N != 3 || s[2] != (A{}) || s[3] != (A{}) {
		t.Fatalf("delete = %#v, source = %#v", deleted, s)
	}

	s = records(1, 1, 2)
	trackOne(s)
	compacted := AsCompact(s)
	if len(compacted) != 2 || compacted[0].N != 1 || compacted[1].N != 2 || s[2] != (A{}) {
		t.Fatalf("compact = %#v, source = %#v", compacted, s)
	}

	left, right := records(1, 2), records(1, 2)
	trackTwo(left, right)
	if !AsEqual(left, right) {
		t.Fatal("equal")
	}
	trackTwo(left, right)
	if got := AsCompare(left, right); got != 0 {
		t.Fatalf("compare = %d, want 0", got)
	}

	ordered := records(3, 1, 1, 2)
	trackOne(ordered)
	minimum := AsMin(ordered)
	if minimum.N != 1 || minimum.Token != ordered[1].Token {
		t.Fatalf("min = %#v, want first minimum slot", minimum)
	}
	trackOne(ordered)
	maximum := AsMax(ordered)
	if maximum.N != 3 || maximum.Token != ordered[0].Token {
		t.Fatalf("max = %#v, want maximum slot", maximum)
	}

	sorted := records(1, 2, 3)
	trackOne(sorted)
	if !AsIsSorted(sorted) {
		t.Fatal("is-sorted rejected sorted input")
	}
	unsorted := records(1, 3, 2)
	trackOne(unsorted)
	if AsIsSorted(unsorted) {
		t.Fatal("is-sorted accepted unsorted input")
	}

	s = records(3, 1, 2)
	trackOne(s)
	AsSort(s)
	if s[0].N != 1 || s[1].N != 2 || s[2].N != 3 {
		t.Fatalf("sort = %#v", s)
	}

	stable := records(2, 1, 2)
	firstTwoToken := stable[0].Token
	secondTwoToken := stable[2].Token
	trackOne(stable)
	AsSortStable(stable)
	if stable[0].N != 1 || stable[1].Token != firstTwoToken || stable[2].Token != secondTwoToken {
		t.Fatalf("stable sort = %#v", stable)
	}

	s = records(1, 2, 3)
	targetValue := record(2)
	target := &targetValue
	trackSearch(s, target)
	if index, ok := AsBinarySearch(s, target); index != 1 || !ok {
		t.Fatalf("binary-search = (%d, %v), want (1, true)", index, ok)
	}
}
`,
	})
	fixture.generateAndTest(t)
}

func TestIntegrationByrefParameterPositions(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"specializations.go": `package fixture

type Large struct{ N int }
type Key struct{ N int }

var firstSlot *Large
var secondSlot **Key

func compareFirst(left *Large, right *Key) int {
	if left != firstSlot { panic("first parameter did not receive the slice slot") }
	return left.N - right.N
}
func compareSecond(left Large, right **Key) int {
	if right != secondSlot { panic("second parameter did not receive the slice slot") }
	return left.N - (*right).N
}
func compareAll(left *Large, right **Key) int {
	if left != firstSlot || right != secondSlot { panic("true did not pass both slice slots") }
	return left.N - (*right).N
}
func compareForSort(left Large, right *Large) int { return left.N - right.N }

//monoslices:generate name=First cmp=compareFirst byref=first ops=compare
//monoslices:generate name=Second cmp=compareSecond byref=second ops=compare
//monoslices:generate name=All cmp=compareAll byref=true ops=compare
//monoslices:generate name=SecondSort cmp=compareForSort byref=second ops=sort
`,
		"fixture_test.go": `package fixture

import "testing"

func TestPositions(t *testing.T) {
	left := []Large{{N: 1}}
	key := &Key{N: 1}
	right := []*Key{key}
	firstSlot = &left[0]
	secondSlot = &right[0]
	if FirstCompare(left, right) != 0 || SecondCompare(left, right) != 0 || AllCompare(left, right) != 0 {
		t.Fatal("positional compare")
	}
	values := []Large{{N: 3}, {N: 1}, {N: 2}}
	SecondSortSort(values)
	if values[0].N != 1 || values[1].N != 2 || values[2].N != 3 {
		t.Fatalf("second-parameter sort = %#v", values)
	}
}
`,
	})
	fixture.generateAndTest(t)
}

func TestIntegrationOwnershipAndStaleReplacement(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"specializations.go": `package fixture

type A int
func compare(a,b A) int { return int(a-b) }
//monoslices:generate name=As cmp=compare ops=compare
//monoslices:generate name=Bs cmp=compare ops=sort
`,
	})
	first := fixture.generate(t)
	if !strings.Contains(string(first.source), "AsCompare") || !strings.Contains(string(first.source), "BsSort") {
		t.Fatal("initial aggregate omitted directive family")
	}
	writeFixtureFile(t, fixture.dir, "older_gen.go", string(first.source))
	writeFixtureFile(t, fixture.dir, "user_generated.go", "package fixture\nfunc Manual() {}\n")
	fixture.rewrite(t, "specializations.go", `package fixture

type A int
func compare(a,b A) int { return int(a-b) }
//monoslices:generate name=As cmp=compare ops=compare
`)
	second := fixture.generate(t, "-out=alternate_gen.go")
	if !strings.Contains(string(second.source), "AsCompare") || strings.Contains(string(second.source), "BsSort") {
		t.Fatal("stale Bs declaration survived")
	}
	for _, name := range []string{config.DefaultOutputFilename, "older_gen.go"} {
		if _, err := os.Stat(filepath.Join(fixture.dir, name)); !os.IsNotExist(err) {
			t.Fatalf("stale owned file %q remains after successful regeneration: err=%v", name, err)
		}
	}
	if _, err := os.Stat(second.outputPath); err != nil {
		t.Fatalf("new output was not retained: %v", err)
	}
	manualPath := filepath.Join(fixture.dir, "user_generated.go")
	if _, err := os.Stat(manualPath); err != nil {
		t.Fatalf("file without ownership comment was removed: %v", err)
	}

	fixture.rewrite(t, "specializations.go", "package fixture\n")
	third := fixture.run("-out=alternate_gen.go")
	if third.status != 0 || len(third.source) != 0 {
		t.Fatalf("empty package generation: status=%d source=%q stderr=%q", third.status, third.source, third.stderr)
	}
	if _, err := os.Stat(second.outputPath); !os.IsNotExist(err) {
		t.Fatalf("output remains after removing all directives: err=%v", err)
	}
	if _, err := os.Stat(manualPath); err != nil {
		t.Fatalf("file without ownership comment was removed with no directives: %v", err)
	}
	fixture.test(t)
}

func TestIntegrationRegeneratesAfterPackageRenameWithStaleOutput(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"records.go": `package oldpkg

type Item int
func compare(a, b Item) int { return int(a - b) }
//monoslices:generate name=Items cmp=compare ops=compare
`,
	})
	first := fixture.generate(t)
	if !strings.Contains(string(first.source), "package oldpkg") {
		t.Fatalf("initial output has wrong package clause:\n%s", first.source)
	}

	fixture.rewrite(t, "records.go", `package newpkg

type Item int
func compare(a, b Item) int { return int(a - b) }
//monoslices:generate name=Items cmp=compare ops=compare
`)
	second := fixture.generate(t)
	if !strings.Contains(string(second.source), "package newpkg") || strings.Contains(string(second.source), "package oldpkg") {
		t.Fatalf("regenerated output did not replace stale package clause:\n%s", second.source)
	}
	if bytes.Equal(first.source, second.source) {
		t.Fatal("package rename did not replace generated output")
	}
}

func TestIntegrationOwnedOutputAndAtomicFailures(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"specializations.go": `package fixture

type A int
func compare(a,b A) int { return int(a-b) }
//monoslices:generate name=As cmp=compare ops=compare
`,
	})
	generated := fixture.generate(t)
	stalePath := filepath.Join(fixture.dir, "legacy_gen.go")
	writeFixtureFile(t, fixture.dir, filepath.Base(stalePath), string(generated.source))
	path := filepath.Join(fixture.dir, config.DefaultOutputFilename)
	const sentinel = "package fixture\n\nfunc sentinel() {}\n"
	for _, test := range []struct{ name, source, want string }{
		{"malformed", "package fixture\n//monoslices:generate name=As cmp\n", "expected '='"},
		{"missing specialization function", "package fixture\n//monoslices:generate name=As cmp=missing ops=compare\n", "cmp=missing must be declared in the same source file as the monoslices directive"},
		{"inapplicable", "package fixture\ntype A int\nfunc compare(a,b A) int { return 0 }\n//monoslices:generate name=As cmp=compare byref=true ops=sort\n", "cmp=compare parameter 1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture.rewrite(t, "specializations.go", test.source)
			writeFixtureFile(t, fixture.dir, filepath.Base(path), sentinel)
			result := fixture.run()
			if result.status != 1 || !strings.Contains(result.stderr, test.want) {
				t.Fatalf("status=%d stderr=%q", result.status, result.stderr)
			}
			for _, stale := range []string{"-pred", "-eq", "-cmp"} {
				if strings.Contains(result.stderr, stale) {
					t.Fatalf("source diagnostic contains stale CLI flag %q: %q", stale, result.stderr)
				}
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != sentinel {
				t.Fatalf("failure replaced output: %q", got)
			}
			got, err = os.ReadFile(stalePath)
			if err != nil {
				t.Fatalf("failure removed stale generated file: %v", err)
			}
			if string(got) != string(generated.source) {
				t.Fatalf("failure changed stale generated file: %q", got)
			}
		})
	}
}

func TestIntegrationRejectsExplicitEmptyOutput(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"specializations.go": "package fixture\nfunc compare(a,b int) int { return a-b }\n",
	})
	result := fixture.run("-out=")
	if result.status != 2 || !strings.Contains(result.stderr, `-out="" must not be empty`) {
		t.Fatalf("status=%d stderr=%q, want explicit empty output diagnostic", result.status, result.stderr)
	}
}

func TestIntegrationRejectsNonProductionOutputBasenames(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"records.go": "package fixture\n",
	})
	for _, name := range []string{"_monoslices.go", ".monoslices.go", "monoslices_test.go", "monoslices_windows.go"} {
		t.Run(name, func(t *testing.T) {
			result := fixture.run("-out=" + name)
			if result.status != 1 || !strings.Contains(result.stderr, "ordinary production Go source filename") || !strings.Contains(result.stderr, name) {
				t.Fatalf("status=%d stderr=%q, want rejected output basename", result.status, result.stderr)
			}
			if _, err := os.Stat(filepath.Join(fixture.dir, name)); !os.IsNotExist(err) {
				t.Fatalf("rejected output %q was created: err=%v", name, err)
			}
		})
	}
}

func TestIntegrationDiagnosticsArePositioned(t *testing.T) {
	cases := []struct{ name, source, want string }{
		{"missing name", "package fixture\n//monoslices:generate cmp=compare\n", "specializations.go:5:1: monoslices: directive name is required"},
		{"missing function role", "package fixture\n//monoslices:generate name=As\n", "at least one of pred, eq, or cmp is required"},
		{"unknown key", "package fixture\n//monoslices:generate name=As cmp=compare nope=x\n", "specializations.go:5:1"},
		{"repeated key", "package fixture\n//monoslices:generate name=As cmp=compare cmp=compare\n", "appears more than once"},
		{"bad selector", "package fixture\n//monoslices:generate name=As cmp=compare byref=other\n", "byref must be one of true, first, or second"},
		{"old byref syntax", "package fixture\n//monoslices:generate name=As cmp=compare byref=all\n", "byref must be one of true, first, or second"},
		{"byref list", "package fixture\n//monoslices:generate name=As cmp=compare byref=first,second\n", "byref must be one of true, first, or second"},
		{"byref false", "package fixture\n//monoslices:generate name=As cmp=compare byref=false\n", "byref must be one of true, first, or second"},
		{"unknown operation", "package fixture\n//monoslices:generate name=As cmp=compare ops=wat\n", "unknown operation"},
		{"duplicate operation", "package fixture\n//monoslices:generate name=As cmp=compare ops=compare,compare\n", "duplicate operation"},
		{"inapplicable operation", "package fixture\n//monoslices:generate name=As cmp=compare ops=contains\n", `operation "contains" requires pred`},
		{"wrong result spelling", "package fixture\n//monoslices:generate name=As cmp=bad ops=compare\n", "must spell its result type int"},
		{"non-function specialization", "package fixture\n//monoslices:generate name=As pred=value ops=contains\n", "pred=value must be declared in the same source file as the monoslices directive"},
		{"wrong result function", "package fixture\n//monoslices:generate name=As pred=wrongResult ops=contains\n", "pred=wrongResult must be declared in the same source file as the monoslices directive"},
		{"wrong arity function", "package fixture\n//monoslices:generate name=As pred=wrongArity ops=contains\n", "pred=wrongArity must be declared in the same source file as the monoslices directive"},
		{"variadic function", "package fixture\n//monoslices:generate name=As pred=variadic ops=contains\n", "pred=variadic must be declared in the same source file as the monoslices directive"},
		{"generic function", "package fixture\n//monoslices:generate name=As pred=generic ops=contains\n", "pred=generic must be declared in the same source file as the monoslices directive"},
		{"unknown directive key", "package fixture\n//monoslices:generate name=As cmp=compare type=Item ops=compare\n", `unknown directive key "type"`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture := newIntegrationFixture(t, map[string]string{
				"specializations.go": "package fixture\ntype A int\nfunc compare(a,b int) int { return a-b }\nfunc bad(a,b int) bool { return a == b }\n" + strings.TrimPrefix(test.source, "package fixture\n"),
				"invalid.go": `package fixture

var value = 1
func wrongResult(value int) int { return value }
func wrongArity(left, right int) bool { return left > right }
func variadic(values ...int) bool { return len(values) > 0 }
func generic[T any](value int) bool { return value > 0 }
type Record struct{}
func makeValue() int { return 1 }
`,
			})
			result := fixture.run()
			if result.status != 1 || !strings.Contains(result.stderr, test.want) {
				t.Fatalf("status=%d stderr=%q", result.status, result.stderr)
			}
			if strings.Count(result.stderr, "monoslices:") != 1 {
				t.Fatalf("diagnostic was double-prefixed: %q", result.stderr)
			}
			for _, stale := range []string{"-pred", "-eq", "-cmp"} {
				if strings.Contains(result.stderr, stale) {
					t.Fatalf("source diagnostic contains stale CLI flag %q: %q", stale, result.stderr)
				}
			}
		})
	}
}

func TestIntegrationRejectsConstrainedDirectiveFiles(t *testing.T) {
	for _, test := range []struct{ name, filename, source, want string }{
		{"test file", "records_test.go", "package fixture\n//monoslices:generate name=As cmp=compare\n", "records_test.go:2:1"},
		{"build tag", "records.go", "//go:build never\n\npackage fixture\n//monoslices:generate name=As cmp=compare\n", "records.go:4:1"},
		{"inactive GOOS", "records_windows.go", "package fixture\n//monoslices:generate name=As cmp=compare\n", "records_windows.go:2:1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newIntegrationFixture(t, map[string]string{
				"specializations.go": "package fixture\nfunc compare(a,b int) int { return a-b }\n",
				test.filename:        test.source,
			})
			result := fixture.run()
			if result.status != 1 || !strings.Contains(result.stderr, test.want) {
				t.Fatalf("status=%d stderr=%q", result.status, result.stderr)
			}
		})
	}
}

func TestIntegrationConflictsReportBothLocationsAndPreserveOutput(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"a.go":    "package fixture\nfunc compare(a,b int) int { return a-b }\n//monoslices:generate name=As cmp=compare ops=compare\n",
		"b.go":    "package fixture\nfunc compareOther(a,b int) int { return a-b }\n//monoslices:generate name=As cmp=compareOther ops=compare\n",
		"hand.go": "package fixture\nfunc AsCompare([]int, []int) int { return 0 }\n",
	})
	path := filepath.Join(fixture.dir, config.DefaultOutputFilename)
	const sentinel = "package fixture\n\nfunc sentinel() {}\n"
	writeFixtureFile(t, fixture.dir, filepath.Base(path), sentinel)
	result := fixture.run()
	if result.status != 1 || !strings.Contains(result.stderr, "a.go:") || !strings.Contains(result.stderr, "b.go:") {
		t.Fatalf("duplicate diagnostic=%q", result.stderr)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != sentinel {
		t.Fatalf("duplicate failure replaced output")
	}

	handwritten := newIntegrationFixture(t, map[string]string{
		"specializations.go": "package fixture\ntype A int\nfunc compare(a,b A) int { return int(a-b) }\n//monoslices:generate name=As cmp=compare ops=compare\n",
		"hand.go":            "package fixture\nfunc AsCompare([]A, []A) int { return 0 }\n",
	})
	handPath := filepath.Join(handwritten.dir, config.DefaultOutputFilename)
	writeFixtureFile(t, handwritten.dir, filepath.Base(handPath), sentinel)
	handResult := handwritten.run()
	if handResult.status != 1 || !strings.Contains(handResult.stderr, "specializations.go:") || !strings.Contains(handResult.stderr, "hand.go:") {
		t.Fatalf("handwritten conflict diagnostic=%q", handResult.stderr)
	}
	got, err = os.ReadFile(handPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != sentinel {
		t.Fatalf("handwritten conflict replaced output")
	}
}

func TestIntegrationImportBindingsReserveGeneratedNames(t *testing.T) {
	private := newIntegrationFixture(t, map[string]string{
		"specializations.go": `package fixture

import monoslices_AsSort_PDQSort_helper "fmt"
var _ = monoslices_AsSort_PDQSort_helper.Sprintf

type A int
func compare(a, b A) int { return int(a - b) }
//monoslices:generate name=As cmp=compare ops=sort
`,
	})
	privateResult := private.generateAndTest(t)
	if !strings.Contains(string(privateResult.source), "func monoslices_AsSort_PDQSort_helper2") {
		t.Fatalf("private helper did not avoid import binding:\n%s", privateResult.source)
	}

	public := newIntegrationFixture(t, map[string]string{
		"specializations.go": `package fixture

import AsSort "fmt"
var _ = AsSort.Sprintf

type A int
func compare(a, b A) int { return int(a - b) }
//monoslices:generate name=As cmp=compare ops=sort
`,
	})
	publicResult := public.run()
	if publicResult.status != 1 || !strings.Contains(publicResult.stderr, "import binding") || !strings.Contains(publicResult.stderr, "specializations.go:") {
		t.Fatalf("public import-binding conflict: status=%d stderr=%q", publicResult.status, publicResult.stderr)
	}
}

func TestIntegrationGoToolGenerateSmoke(t *testing.T) {
	dir := filepath.Join(repositoryRoot(t), "internal", "app", "testdata", "generate")
	generated := filepath.Join(dir, "monoslices_gen.go")
	_ = os.Remove(generated)
	defer os.Remove(generated)
	command := exec.Command("go", "generate", ".")
	command.Dir = dir
	command.Env = append(os.Environ(), "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("go generate smoke: %v\\n%s", err, output)
	}
	content, err := os.ReadFile(generated)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "func FoosSort(s []Foo)") {
		t.Fatalf("go generate output lacks FoosSort:\\n%s", content)
	}
}

func TestIntegrationSubprocessAndGoGenerateSmoke(t *testing.T) {
	fixture := newIntegrationFixture(t, map[string]string{
		"specializations.go": "package fixture\ntype A int\nfunc compare(a,b A) int { return int(a-b) }\n//monoslices:generate name=As cmp=compare ops=binary-search\n",
		"fixture_test.go":    "package fixture\nimport \"testing\"\nfunc TestGenerated(t *testing.T) { if i,ok:=AsBinarySearch([]A{1,2},2); i != 1 || !ok { t.Fatal(i,ok) } }\n",
	})
	root := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "monoslices")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = root
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build monoslices: %v\n%s", err, output)
	}
	command := exec.Command(binary)
	command.Dir = fixture.dir
	command.Env = append(os.Environ(), "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("subprocess generation: %v\n%s", err, output)
	}
	fixture.test(t)
}

func writeFixtureFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
