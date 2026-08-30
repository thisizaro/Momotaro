package server

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"testing"
)

// TestReportingCostsMatchInterventionCostsYAML is the drift guard for
// baseline.go's naiveRetryCostPaise/naiveNudgeCostPaise, the same pattern
// services/executor/internal/ports/cost_reconciliation_test.go already
// uses for its own copy of these numbers. Three checked-in copies of the
// same two figures now exist (the Decision Engine's economics model reads
// the YAML directly; the Executor and this package each carry a literal
// copy, since a cross-service import is a compile error). If any of them
// drifts from configs/intervention_costs.yaml, Unit K's baseline
// comparison is computed against a cost model nothing else in the system
// actually charges, and nobody would notice, because nothing else compares
// them.
func TestReportingCostsMatchInterventionCostsYAML(t *testing.T) {
	content := readInterventionCostsYAML(t)

	channelsBlock := yamlBlock(t, content, `(?m)^channels:\s*$`, `(?m)^[a-z_]+:\s*$`)
	wantSMS := yamlInt(t, channelsBlock, "sms_paise")

	retryBlock := yamlBlock(t, content, `(?m)^\s{2}RETRY:\s*$`, `(?m)^\s{2}[A-Z_]+:\s*$`)
	wantRetry := yamlInt(t, retryBlock, "direct_cost_paise")

	cases := []struct {
		name string
		got  int64
		want int64
	}{
		{"naiveRetryCostPaise vs actions.RETRY.direct_cost_paise", naiveRetryCostPaise, wantRetry},
		{"naiveNudgeCostPaise vs channels.sms_paise", naiveNudgeCostPaise, wantSMS},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s: reporting constant = %d paise, configs/intervention_costs.yaml = %d paise. "+
				"These must agree, or Unit K's baseline comparison is priced against a different cost "+
				"model than the one that actually charges. Update baseline.go to match the YAML "+
				"(the YAML is the source of truth).", tc.name, tc.got, tc.want)
		}
	}
}

// readInterventionCostsYAML locates and reads configs/intervention_costs.yaml
// relative to the repo root, found by walking up from this test file rather
// than assuming a working directory, so it works under `go test ./...` from
// any directory.
func readInterventionCostsYAML(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine this test file's own path")
	}
	// this file:  <root>/services/reporting/internal/server/cost_reconciliation_test.go
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..")
	path := filepath.Join(root, "configs", "intervention_costs.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// yamlBlock returns the slice of content starting after the line matching
// startPattern up to (not including) the next line matching endPattern,
// scoping a key lookup to one section instead of matching the first
// occurrence anywhere in the file (several action blocks share key names
// like direct_cost_paise).
func yamlBlock(t *testing.T, content, startPattern, endPattern string) string {
	t.Helper()
	start := regexp.MustCompile(startPattern)
	loc := start.FindStringIndex(content)
	if loc == nil {
		t.Fatalf("could not find section start matching %q in configs/intervention_costs.yaml", startPattern)
	}
	rest := content[loc[1]:]
	end := regexp.MustCompile(endPattern)
	endLoc := end.FindStringIndex(rest)
	if endLoc == nil {
		return rest
	}
	return rest[:endLoc[0]]
}

// yamlInt extracts the integer value of `key: <value>` from a YAML text
// block, tolerating leading indentation.
func yamlInt(t *testing.T, block, key string) int64 {
	t.Helper()
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `:\s*(-?\d+)\s*$`)
	m := re.FindStringSubmatch(block)
	if m == nil {
		t.Fatalf("could not find %q in the expected section of configs/intervention_costs.yaml", key)
	}
	v, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		t.Fatalf("parsing %q value %q: %v", key, m[1], err)
	}
	return v
}
