package main

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// repoRootForMakefileTest finds the repo root the same way other tests in
// this repo locate checked-in files relative to the test binary's build
// location, since `go test` runs with the package directory as its working
// directory, not the repo root.
func repoRootForMakefileTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// services/api-gateway/cmd/main_test.go -> repo root is four levels up.
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
}

// envFromDotFile parses a .env-shaped file (KEY=VALUE, one per line,
// "#"-prefixed comments and blank lines skipped) into a map. It is
// deliberately simple (no quoting, no export, no variable expansion): that
// is exactly the shape docs/ENGINEERING.md section 5 and the Makefile's own
// `include .env` both assume this repo's .env files have.
//
// Deliberately never pointed at the real `.env`: that file is gitignored
// (AGENTS.md: "the real .env is gitignored, never committed"), so it does
// not exist on a CI runner, and a test that opens it is green on a
// developer machine and red everywhere else (docs/INCIDENTS.md 2026-09-03).
// `.env.example` is tracked, carries the same real placeholder values for
// every var loadConfig needs, and is what every test in this file uses.
func envFromDotFile(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	env := make(map[string]string)
	kv := regexp.MustCompile(`^([A-Z_][A-Z0-9_]*)=(.*)$`)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if m := kv.FindStringSubmatch(line); m != nil {
			env[m[1]] = m[2]
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return env
}

// envFromMakeRecipe extracts the shell-prefix KEY=VALUE assignments a
// Makefile target's recipe sets before its command, e.g.
// `GRPC_PORT=9198 METRICS_PORT=9199 \` then `INGESTION_ADDR=localhost:9090
// ... \` then `go run ./services/api-gateway/cmd`. These are shell
// environment overrides for that one command, so (matching real `make`
// semantics) they take precedence over whatever `.env` already exported.
func envFromMakeRecipe(t *testing.T, makefilePath, target string) map[string]string {
	t.Helper()
	f, err := os.Open(makefilePath)
	if err != nil {
		t.Fatalf("open %s: %v", makefilePath, err)
	}
	defer f.Close()

	env := make(map[string]string)
	kv := regexp.MustCompile(`\b([A-Z_][A-Z0-9_]*)=(\S+)`)
	scanner := bufio.NewScanner(f)
	inTarget := false
	found := false
	for scanner.Scan() {
		line := scanner.Text()
		if !inTarget {
			if line == target+":" {
				inTarget = true
				found = true
			}
			continue
		}
		if !strings.HasPrefix(line, "\t") {
			break // recipe ended
		}
		for _, m := range kv.FindAllStringSubmatch(line, -1) {
			env[m[1]] = strings.TrimSuffix(m[2], `\`)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan %s: %v", makefilePath, err)
	}
	if !found {
		t.Fatalf("target %q not found in %s", target, makefilePath)
	}
	return env
}

// TestRunAPIGatewayRecipeItselfSetsDecisionEngineAddr is the narrow half of
// the regression test for docs/INCIDENTS.md 2026-09-03. `.env` always sets
// SOME value for every service-to-service address (a shared, usually wrong,
// single-service placeholder, see the comment above DECISION_ENGINE_ADDR in
// `.env`), so a test that only checks the environment loadConfig ends up
// with, after `.env` has already been merged in, can pass while the
// `run-api-gateway` recipe itself sets nothing at all: `.env`'s placeholder
// silently fills the gap and loadConfig never errors, so the service starts
// and then dials the wrong port for every downtime webhook, exactly the
// "no startup error, no crash, just a route that silently does the wrong
// thing" failure the fix for this incident called out. So this checks the
// recipe's OWN assignments in isolation, before any `.env` fallback is
// layered on, and additionally cross-checks the port against
// `run-decision-engine`'s own `GRPC_PORT` rather than a hardcoded literal,
// so a future port renumbering that updates one target and not the other
// still fails this test instead of silently pointing at the wrong service.
func TestRunAPIGatewayRecipeItselfSetsDecisionEngineAddr(t *testing.T) {
	root := repoRootForMakefileTest(t)
	makefilePath := filepath.Join(root, "Makefile")

	gwEnv := envFromMakeRecipe(t, makefilePath, "run-api-gateway")
	got, ok := gwEnv["DECISION_ENGINE_ADDR"]
	if !ok {
		t.Fatal("run-api-gateway's own recipe does not set DECISION_ENGINE_ADDR; `.env`'s placeholder would silently fill the gap with the wrong port instead of failing loudly")
	}

	deEnv := envFromMakeRecipe(t, makefilePath, "run-decision-engine")
	wantPort := deEnv["GRPC_PORT"]
	if wantPort == "" {
		t.Fatal("run-decision-engine's own recipe does not set GRPC_PORT, cannot cross-check the port")
	}
	if !strings.HasSuffix(got, ":"+wantPort) {
		t.Errorf("run-api-gateway sets DECISION_ENGINE_ADDR=%s, want it to end in :%s (run-decision-engine's own GRPC_PORT)", got, wantPort)
	}
}

// TestRunAPIGatewayMakeTargetSetsEveryRequiredAddr is the broad half: builds
// the environment `make run-api-gateway` runs with in practice (`.env`'s
// exports, overridden by the recipe's own command-line assignments), using
// the tracked `.env.example` as the stand-in for `.env` (see
// envFromDotFile's own comment on why, docs/INCIDENTS.md 2026-09-03: a test
// that reads the real, gitignored `.env` is green locally and red on every
// CI runner, since the file does not exist there at all), and calls the
// real loadConfig against it, so any field required at startup that ends up
// missing from BOTH sources, not just this one, fails one fast `go test`
// rather than nine slow e2e ones the way this incident's harness gap did.
func TestRunAPIGatewayMakeTargetSetsEveryRequiredAddr(t *testing.T) {
	root := repoRootForMakefileTest(t)

	env := envFromDotFile(t, filepath.Join(root, ".env.example"))
	for k, v := range envFromMakeRecipe(t, filepath.Join(root, "Makefile"), "run-api-gateway") {
		env[k] = v // the recipe's own assignments win, matching real `make`/shell precedence
	}

	for k, v := range env {
		t.Setenv(k, v)
	}

	if _, err := loadConfig(); err != nil {
		t.Fatalf("loadConfig with .env.example merged with the environment `make run-api-gateway` actually provides: %v", err)
	}
}

// TestE2EHarnessSetsDecisionEngineAddrForAPIGateway is the third leg: the
// e2e harness (test/e2e/harness_test.go) builds its own environment map per
// service from scratch, with no `.env` fallback at all, so this is the one
// call site where a missing required var is not merely wrong, it is
// genuinely absent and the Gateway subprocess dies at startup, exactly what
// made all nine e2e tests fail on this PR before the fix. Parsed as text
// (the harness lives behind the `e2e` build tag, in a different package,
// specifically so it never entangles with what it is testing) rather than
// executed, looking for DECISION_ENGINE_ADDR inside the api-gateway
// startProcess call specifically, not merely anywhere in the file (World
// Simulator's own block already sets it, and a substring search against the
// whole file would pass on that alone without checking the Gateway's own
// block at all).
func TestE2EHarnessSetsDecisionEngineAddrForAPIGateway(t *testing.T) {
	root := repoRootForMakefileTest(t)
	path := filepath.Join(root, "test", "e2e", "harness_test.go")

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	src := string(raw)

	marker := `startProcess(t, "api-gateway"`
	start := strings.Index(src, marker)
	if start == -1 {
		t.Fatalf("could not find the api-gateway startProcess call in %s; this test's own marker may be stale", path)
	}
	end := strings.Index(src[start:], "})))")
	if end == -1 {
		t.Fatalf("could not find the end of the api-gateway startProcess call in %s", path)
	}
	block := src[start : start+end]

	if !strings.Contains(block, "DECISION_ENGINE_ADDR") {
		t.Error("the e2e harness's api-gateway env block does not set DECISION_ENGINE_ADDR: the Gateway subprocess will die at startup and every e2e test will time out waiting for it")
	}
}
