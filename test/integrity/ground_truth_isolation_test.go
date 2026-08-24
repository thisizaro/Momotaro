// Package integrity holds cross-cutting architectural invariants that are
// not scoped to any single service's own test suite.
//
// This file enforces docs/ARCHITECTURE.md section 5a's non-negotiable rule:
// the decision path must hold no query path to GROUND_TRUTH, the sealed
// answer key seeded by scripts/ and readable only by demo/world-simulator
// and the Reporting service's accuracy scorer (section 10a's table
// ownership list). AGENTS.md's locked decisions restate this and promise
// "there is a test for this."
//
// docs/PLAN.md's Phase 2 checkbox names only the Decision Engine. This test
// deliberately covers all three decision-path services instead: Decision
// Engine, Classifier and Executor. Section 5a states the rule in terms of
// "the decision path," not one service, and a guard that only watches one
// of the three services through which a record moves before money is spent
// is a weaker guarantee than the rule it is meant to enforce.
//
// This runs at the unit tier, no build tag, no infrastructure: an
// architectural invariant that only runs when docker is up is a weak
// guard, since it would not run on every CI pass the way the rest of the
// unit suite does.
//
// Two scanners, not one, because a query can reach the decision path
// without ever being Go source: .go files are parsed with go/parser and
// go/ast so that a comment documenting the rule can never trip the test
// (only a real identifier or string literal can); everything else that can
// carry a query into the compiled binary without being a .go file itself
// (.sql via //go:embed, at minimum, plus .yaml/.yml/.json/.tmpl) is checked
// with a plain normalized text match, since there is no AST for a SQL file.
// See docs/DECISIONS.md for what still is not covered even after that.
package integrity

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// groundTruthMarker is what a reference to the GROUND_TRUTH table looks
// like once both sides of a comparison are lowercased and stripped of
// underscores: "ground_truth", "GROUND_TRUTH", "GroundTruth", and
// "groundTruth" (a Go identifier naming it, or a db/struct tag) all reduce
// to this same token. That normalisation is deliberately generous: it is
// tuned to catch every spelling actually used in this repo (see
// migrations/00001_initial_schema.sql's `ground_truth` table and the
// existing `GROUND_TRUTH` references in comments and docs), at the cost of
// being willing to flag an unrelated identifier that happens to contain the
// same run of letters. See the test's doc comment for what this approach
// does not catch.
const groundTruthMarker = "groundtruth"

func normalize(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "_", ""))
}

func hasGroundTruthMarker(s string) bool {
	return strings.Contains(normalize(s), groundTruthMarker)
}

// violation is one place in source where the scanner found a live code
// reference (not a comment) to GROUND_TRUTH.
type violation struct {
	pos  token.Position
	kind string
	text string
}

func (v violation) String() string {
	return fmt.Sprintf("%s: %s %q", v.pos, v.kind, v.text)
}

// scanForGroundTruthReferences walks every .go file under root and reports
// any AST identifier or string literal (SQL text, a query-builder table
// name, a struct/db tag, an import path) that names GROUND_TRUTH.
//
// Deliberately go/parser + go/ast rather than a text grep: source is
// parsed with parser mode 0, which does not retain comments in the
// resulting tree at all, so a comment such as the one already living in
// services/executor/internal/ports/ports.go ("nothing here reads
// GROUND_TRUTH, and nothing here ever will") is structurally invisible to
// ast.Inspect and can never trip this test. A grep for the string
// "ground_truth" cannot make that distinction and would fail on that exact
// comment today.
func scanForGroundTruthReferences(root string) (fileCount int, violations []violation, err error) {
	fset := token.NewFileSet()
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk %s: %w", path, walkErr)
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		fileCount++

		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", path, readErr)
		}

		file, parseErr := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", path, parseErr)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.Ident:
				if hasGroundTruthMarker(node.Name) {
					violations = append(violations, violation{
						pos:  fset.Position(node.Pos()),
						kind: "identifier",
						text: node.Name,
					})
				}
			case *ast.BasicLit:
				if node.Kind != token.STRING {
					return true
				}
				val, uqErr := strconv.Unquote(node.Value)
				if uqErr != nil {
					// Malformed literal, fall back to the raw (still
					// quoted) token rather than skip it silently.
					val = node.Value
				}
				if hasGroundTruthMarker(val) {
					violations = append(violations, violation{
						pos:  fset.Position(node.Pos()),
						kind: "string literal",
						text: val,
					})
				}
			}
			return true
		})
		return nil
	})
	if walkErr != nil {
		return fileCount, violations, walkErr
	}
	return fileCount, violations, nil
}

// nonGoTextExtensions are the non-Go file types checked for a plain
// (non-AST) text reference to GROUND_TRUTH: anything that can carry a
// query or a table name into the compiled binary without ever being a .go
// file itself, most concretely a SQL file pulled in with //go:embed. This
// is deliberately not exhaustive, see the test's own doc comment and
// docs/DECISIONS.md for what is and is not covered.
//
// .md is deliberately excluded even though it is plausible: both
// services/classifier/SPEC.md and services/executor/SPEC.md already carry
// a "hard boundary: no ground truth" section that names GROUND_TRUTH in
// prose, exactly the documentation case the AST path exists to leave
// alone for .go files. Scanning .md as plain text would flag that
// legitimate documentation as a violation, which is the wrong trade.
var nonGoTextExtensions = map[string]bool{
	".sql":  true,
	".yaml": true,
	".yml":  true,
	".json": true,
	".tmpl": true,
}

// scanNonGoTextFilesForGroundTruthReferences walks root looking at files
// whose extension is in nonGoTextExtensions for a plain, normalized text
// match against GROUND_TRUTH. There is no AST for a .sql or .yaml file, so
// unlike the .go scan above, a comment inside one of these files (a SQL
// "-- " comment, for instance) is not distinguished from code: a plain
// text match is the right and honest tool here, not a limitation snuck in
// silently. See the package doc comment and docs/DECISIONS.md.
func scanNonGoTextFilesForGroundTruthReferences(root string) (fileCount int, violations []violation, err error) {
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk %s: %w", path, walkErr)
		}
		if d.IsDir() || !nonGoTextExtensions[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		fileCount++

		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", path, readErr)
		}
		content := string(src)
		if !hasGroundTruthMarker(content) {
			return nil
		}

		line, snippet := firstMatchingLine(content)
		violations = append(violations, violation{
			pos:  token.Position{Filename: path, Line: line},
			kind: fmt.Sprintf("text reference (%s)", filepath.Ext(path)),
			text: snippet,
		})
		return nil
	})
	if walkErr != nil {
		return fileCount, violations, walkErr
	}
	return fileCount, violations, nil
}

// firstMatchingLine returns the 1-based line number and trimmed text of the
// first line in content that itself contains the marker, for a readable
// violation message. Falls back to line 1 if the marker only appears when
// spanning a line break (a real table name never does this in practice).
func firstMatchingLine(content string) (int, string) {
	for i, line := range strings.Split(content, "\n") {
		if hasGroundTruthMarker(line) {
			return i + 1, strings.TrimSpace(line)
		}
	}
	return 1, strings.TrimSpace(content)
}

// repoRoot locates the repository root relative to this test file's own
// path, so the test works regardless of the working directory `go test`
// is invoked from.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("determine repo root: runtime.Caller failed")
	}
	// thisFile is <repoRoot>/test/integrity/ground_truth_isolation_test.go
	return filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
}

// TestDecisionPathHasNoGroundTruthQueryPath is the enforcement test closing
// the docs/PLAN.md Phase 2 item. Table-driven per docs/AGENTS.md, one case
// per decision-path service; services/reporting and demo/world-simulator
// are the two places the ownership table in ARCHITECTURE.md section 10a
// permits to read GROUND_TRUTH and are deliberately not in this table.
func TestDecisionPathHasNoGroundTruthQueryPath(t *testing.T) {
	root := repoRoot(t)

	cases := []struct {
		name    string
		service string // relative to repo root
	}{
		{name: "decision-engine", service: "services/decision-engine"},
		{name: "classifier", service: "services/classifier"},
		{name: "executor", service: "services/executor"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(root, tc.service)
			if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
				t.Fatalf("service directory %s not found: %v (has the repo layout changed under this test?)", dir, statErr)
			}

			goFileCount, violations, err := scanForGroundTruthReferences(dir)
			if err != nil {
				t.Fatalf("scan %s: %v", tc.service, err)
			}

			// A scanner that silently walks zero files passes forever and
			// guards nothing. This is what would have hidden a typo'd
			// path or a directory that moved out from under this test.
			if goFileCount == 0 {
				t.Fatalf("scanned zero .go files under %s; this test would pass vacuously and prove nothing", tc.service)
			}
			t.Logf("scanned %d .go file(s) under %s", goFileCount, tc.service)

			// QA found that a query can also ship in the binary via a
			// non-Go file (their repro: a .sql file pulled in with
			// //go:embed), which the .go-only scan above never opens.
			textFileCount, textViolations, err := scanNonGoTextFilesForGroundTruthReferences(dir)
			if err != nil {
				t.Fatalf("scan non-Go text files under %s: %v", tc.service, err)
			}
			if textFileCount == 0 {
				// Not a vacuous pass: no .sql/.yaml/.yml/.json/.tmpl file
				// exists in this service today, so zero is the honest,
				// expected count, not a sign the walk silently found
				// nothing it should have. The scanner's ability to catch a
				// real match in these file types is proven independently,
				// against a synthetic fixture, by
				// TestNonGoTextScanCatchesGroundTruthInEmbeddedFile.
				t.Logf("scanned 0 non-Go text file(s) (.sql/.yaml/.yml/.json/.tmpl) under %s: none exist in this service today", tc.service)
			} else {
				t.Logf("scanned %d non-Go text file(s) under %s", textFileCount, tc.service)
			}
			violations = append(violations, textViolations...)

			if len(violations) > 0 {
				lines := make([]string, len(violations))
				for i, v := range violations {
					lines[i] = v.String()
				}
				t.Errorf("%s has a query path to GROUND_TRUTH, violating the integrity rule in docs/ARCHITECTURE.md section 5a (see also section 10a's ownership table); %d reference(s) found:\n%s",
					tc.service, len(violations), strings.Join(lines, "\n"))
			}
		})
	}
}

// TestScannerDistinguishesCommentsFromCode is a self-test of the detector
// above, run against a synthetic fixture rather than real service code. It
// exists because a test asserting an absence proves nothing unless it can
// also be shown to detect a presence: this proves the scanner catches a
// real code-level violation (a SQL string and a Go identifier) while
// leaving a comment mentioning the exact same words alone, which is the
// property a naive `grep ground_truth` could not give us.
func TestScannerDistinguishesCommentsFromCode(t *testing.T) {
	dir := t.TempDir()
	src := `package fixture

// This comment mentions ground_truth and GROUND_TRUTH on purpose. A
// comment must never fail the isolation test, only code does.
func Innocent() string {
	return "hello world"
}

func Violation() string {
	return "SELECT * FROM ground_truth WHERE record_id = $1"
}

type GroundTruthRow struct {
	RecordID string
}
`
	if err := os.WriteFile(filepath.Join(dir, "fixture.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	fileCount, violations, err := scanForGroundTruthReferences(dir)
	if err != nil {
		t.Fatalf("scan fixture dir: %v", err)
	}
	if fileCount != 1 {
		t.Fatalf("expected to scan exactly 1 file, scanned %d", fileCount)
	}
	// Exactly two code-level references: the SQL string literal and the
	// GroundTruthRow type identifier. The two mentions inside the comment
	// must contribute nothing, which is the assertion that proves comments
	// are ignored rather than merely assumed to be.
	if len(violations) != 2 {
		t.Fatalf("expected exactly 2 violations (1 string literal + 1 identifier), got %d:\n%s",
			len(violations), joinViolations(violations))
	}
}

func joinViolations(violations []violation) string {
	lines := make([]string, len(violations))
	for i, v := range violations {
		lines[i] = v.String()
	}
	return strings.Join(lines, "\n")
}

// writeEmbeddedSQLFixture reproduces QA's reported gap as a fixture: a Go
// file that pulls a query in with //go:embed, where the actual query text,
// and the actual GROUND_TRUTH reference, lives in a .sql file the Go-only
// scanner never opens. fetch.go itself names no GROUND_TRUTH anything, only
// a relative file path.
func writeEmbeddedSQLFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "queries"), 0o755); err != nil {
		t.Fatalf("make queries dir: %v", err)
	}

	goSrc := `package fixture

import _ "embed"

// lookupQuery is compiled into the binary at build time; the query text
// lives in queries/lookup.sql, not in this file.
//go:embed queries/lookup.sql
var lookupQuery string
`
	sqlSrc := "-- looks up whether a record is truly recoverable\n" +
		"SELECT truly_recoverable FROM ground_truth WHERE record_id = $1\n"

	if err := os.WriteFile(filepath.Join(dir, "fetch.go"), []byte(goSrc), 0o644); err != nil {
		t.Fatalf("write fetch.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "queries", "lookup.sql"), []byte(sqlSrc), 0o644); err != nil {
		t.Fatalf("write lookup.sql: %v", err)
	}
	return dir
}

// TestNonGoTextScanCatchesGroundTruthInEmbeddedFile is QA's reported gap,
// reproduced as a fixture rather than as real files added to a service
// tree. Before scanNonGoTextFilesForGroundTruthReferences existed, the
// only scanner was the .go-suffix AST scan above, which sees fetch.go (no
// GROUND_TRUTH reference at all, only a relative file path) and never
// opens queries/lookup.sql, where the actual violating query lives. It
// compiled, the query shipped in the binary, and the old scanner stayed
// green. Confirmed as a real regression before this fix: running the
// go-only scan alone here finds nothing, which is exactly the gap; the
// combined scan below is what closes it.
func TestNonGoTextScanCatchesGroundTruthInEmbeddedFile(t *testing.T) {
	dir := writeEmbeddedSQLFixture(t)

	goFiles, goViolations, err := scanForGroundTruthReferences(dir)
	if err != nil {
		t.Fatalf("scan go source: %v", err)
	}
	if goFiles != 1 {
		t.Fatalf("expected to scan exactly 1 .go file, scanned %d", goFiles)
	}
	if len(goViolations) != 0 {
		t.Fatalf("expected the .go-only scan to find nothing on its own (the reference lives in the embedded .sql file, not in Go source): the gap this test exists to close, got %d: %s",
			len(goViolations), joinViolations(goViolations))
	}

	textFiles, textViolations, err := scanNonGoTextFilesForGroundTruthReferences(dir)
	if err != nil {
		t.Fatalf("scan non-Go text files: %v", err)
	}
	if textFiles != 1 {
		t.Fatalf("expected to scan exactly 1 non-Go text file, scanned %d", textFiles)
	}
	if len(textViolations) != 1 {
		t.Fatalf("expected the embedded lookup.sql to be caught as exactly 1 violation, got %d: %s",
			len(textViolations), joinViolations(textViolations))
	}
	if !strings.Contains(textViolations[0].text, "ground_truth") {
		t.Fatalf("violation text %q does not contain the actual matched line", textViolations[0].text)
	}
}

// TestNonGoTextScanIgnoresUnlistedExtensions proves the .sql/.yaml/.yml/
// .json/.tmpl allowlist is not accidentally wide open to every file: a
// GROUND_TRUTH reference sitting in, say, a .txt or .md file (documentation
// prose is exactly this case, see services/classifier/SPEC.md and
// services/executor/SPEC.md's "hard boundary: no ground truth" sections)
// must not be flagged.
func TestNonGoTextScanIgnoresUnlistedExtensions(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"notes.md":   "GROUND_TRUTH must never be read from the decision path.",
		"README.txt": "See ground_truth for the answer key.",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	fileCount, violations, err := scanNonGoTextFilesForGroundTruthReferences(dir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if fileCount != 0 {
		t.Fatalf("expected .md/.txt files to be skipped entirely, but scanned %d", fileCount)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations from unlisted extensions, got %d: %s", len(violations), joinViolations(violations))
	}
}
