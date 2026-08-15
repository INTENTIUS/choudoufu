// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

// Temporary measurement harness for issue #136's retirement batches. Not
// part of any tier: it runs only when ESTATE_RETIRE_MEASURE=1, and it is
// deleted once the batch lands.
//
// For every type in typeOverrides it deletes the entry in-process,
// regenerates every cohort whose committed tree renders that type, and
// classifies the diff. A type is retirable when every differing line is
// either its own "# overrides:" provenance comment or its own GENERATED.md
// provenance row - the byte-identical bar HANDOFF.md records.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

var resourceBlockRe = regexp.MustCompile(`(?m)^resource "(aws_[a-z0-9_]+)"`)

// TestRegenerateAllCohorts rewrites every cohort with a recorded command in
// place, exactly as the recorded command would. Gated the same way as the
// measurement; used for the bulk regeneration after a seed-rule change.
func TestRegenerateAllCohorts(t *testing.T) {
	if os.Getenv("ESTATE_REGEN_ALL") != "1" {
		t.Skip("bulk regeneration; set ESTATE_REGEN_ALL=1")
	}
	flocitest.Gate(t, "estate-gen bulk regeneration")
	flocitest.RequireBinary(t, defaultInitBin)

	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	schemas, err := acquireSchemas(defaultInitBin, t.TempDir(), testLogWriter{t})
	if err != nil {
		t.Fatalf("acquiring provider schemas: %v", err)
	}
	estates := filepath.Join(root, "live", "e2e", "estates")
	entries, err := os.ReadDir(estates)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		cohort := e.Name()
		dir := filepath.Join(estates, cohort)
		if !holdsConfig(t, dir) {
			continue
		}
		types, hasCommand := recordedRegenTypes(t, dir)
		if !hasCommand {
			continue
		}
		if types == nil {
			types, err = defaultCohortTypes(root, cohort)
			if err != nil {
				t.Fatalf("defaultCohortTypes(%s): %v", cohort, err)
			}
		}
		g, err := planCohort(cohort, schemas, types)
		if err != nil {
			t.Fatalf("planCohort(%s): %v", cohort, err)
		}
		if err := writeCohort(dir, cohort, types, g, false, nil); err != nil {
			t.Fatalf("writeCohort(%s): %v", cohort, err)
		}
		if _, err := exec.LookPath(defaultFmtBin); err == nil {
			if err := formatWithBinary(defaultFmtBin, dir, runCombined); err != nil {
				t.Fatalf("formatting %s: %v", cohort, err)
			}
		}
		t.Logf("%s regenerated", cohort)
	}
}

func TestMeasureOverrideRetirements(t *testing.T) {
	if os.Getenv("ESTATE_RETIRE_MEASURE") != "1" {
		t.Skip("measurement harness; set ESTATE_RETIRE_MEASURE=1")
	}
	flocitest.Gate(t, "estate-gen retirement measurement")
	flocitest.RequireBinary(t, defaultInitBin)

	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	schemas, err := acquireSchemas(defaultInitBin, t.TempDir(), testLogWriter{t})
	if err != nil {
		t.Fatalf("acquiring provider schemas: %v", err)
	}

	estates := filepath.Join(root, "live", "e2e", "estates")
	entries, err := os.ReadDir(estates)
	if err != nil {
		t.Fatal(err)
	}

	// cohort -> recorded roster, and type -> cohorts that RENDER it (from
	// the committed .tf, so supporting renders count too - a roster alone
	// would miss the shared aws_iam_role).
	rosters := map[string][]string{}
	typeCohorts := map[string]map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		cohort := e.Name()
		dir := filepath.Join(estates, cohort)
		if !holdsConfig(t, dir) {
			continue
		}
		types, hasCommand := recordedRegenTypes(t, dir)
		if !hasCommand {
			continue
		}
		if types == nil {
			types, err = defaultCohortTypes(root, cohort)
			if err != nil {
				t.Fatalf("defaultCohortTypes(%s): %v", cohort, err)
			}
		}
		rosters[cohort] = types
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".tf") {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, m := range resourceBlockRe.FindAllStringSubmatch(string(data), -1) {
				if typeCohorts[m[1]] == nil {
					typeCohorts[m[1]] = map[string]bool{}
				}
				typeCohorts[m[1]][cohort] = true
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	haveFmt := false
	if _, err := exec.LookPath(defaultFmtBin); err == nil {
		haveFmt = true
	}

	regen := func(cohort string) (string, error) {
		out := filepath.Join(t.TempDir(), cohort)
		g, err := planCohort(cohort, schemas, rosters[cohort])
		if err != nil {
			return "", err
		}
		if err := writeCohort(out, cohort, rosters[cohort], g, false, nil); err != nil {
			return "", err
		}
		if haveFmt {
			if err := formatWithBinary(defaultFmtBin, out, runCombined); err != nil {
				return "", err
			}
		}
		return out, nil
	}

	type result struct {
		Type      string   `json:"type"`
		Cohorts   []string `json:"cohorts"`
		Retirable bool     `json:"retirable"`
		Detail    string   `json:"detail,omitempty"`
	}
	var results []result

	var overrideTypes []string
	for typ := range typeOverrides {
		overrideTypes = append(overrideTypes, typ)
	}
	sort.Strings(overrideTypes)

	for _, typ := range overrideTypes {
		var cohorts []string
		for c := range typeCohorts[typ] {
			cohorts = append(cohorts, c)
		}
		sort.Strings(cohorts)
		res := result{Type: typ, Cohorts: cohorts}
		if len(cohorts) == 0 {
			res.Detail = "no committed cohort renders this type"
			results = append(results, res)
			continue
		}

		saved := typeOverrides[typ]
		delete(typeOverrides, typ)
		res.Retirable = true
		for _, cohort := range cohorts {
			out, err := regen(cohort)
			if err != nil {
				res.Retirable = false
				res.Detail = cohort + ": regeneration failed: " + err.Error()
				break
			}
			if detail := classifyDiff(t, filepath.Join(estates, cohort), out, typ); detail != "" {
				res.Retirable = false
				res.Detail = cohort + ": " + detail
				break
			}
		}
		typeOverrides[typ] = saved
		results = append(results, res)
		t.Logf("%s retirable=%v %s", typ, res.Retirable, res.Detail)
	}

	outPath := os.Getenv("ESTATE_RETIRE_REPORT")
	if outPath == "" {
		outPath = filepath.Join(root, "retire-report.json")
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	retirable := 0
	for _, r := range results {
		if r.Retirable {
			retirable++
		}
	}
	t.Logf("measured %d override types: %d retirable, report at %s", len(results), retirable, outPath)
}

// classifyDiff returns "" when the regenerated tree matches the committed
// one apart from typ's own provenance surface: a 1:1 change of a
// "# overrides:" comment line, or a 1:1 change of typ's GENERATED.md table
// row. Anything else - added lines, removed lines, a changed argument, a
// change on another type's row - is a reason not to retire.
func classifyDiff(t *testing.T, committed, generated, typ string) string {
	t.Helper()

	collect := func(root string) map[string]bool {
		out := map[string]bool{}
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if !isConfigFile(d.Name()) && d.Name() != "GENERATED.md" {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			out[filepath.ToSlash(rel)] = true
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	files := collect(committed)
	for name := range collect(generated) {
		files[name] = true
	}

	for name := range files {
		a, errA := os.ReadFile(filepath.Join(committed, filepath.FromSlash(name)))
		b, errB := os.ReadFile(filepath.Join(generated, filepath.FromSlash(name)))
		if errA != nil {
			return name + ": only in the regeneration"
		}
		if errB != nil {
			return name + ": only in the committed tree"
		}
		if string(a) == string(b) {
			continue
		}
		al := strings.Split(string(a), "\n")
		bl := strings.Split(string(b), "\n")
		if len(al) != len(bl) {
			return name + ": line count changed"
		}
		for i := range al {
			if al[i] == bl[i] {
				continue
			}
			overridesLine := strings.HasPrefix(strings.TrimSpace(al[i]), "# overrides:") &&
				strings.HasPrefix(strings.TrimSpace(bl[i]), "# overrides:")
			mdRow := strings.HasSuffix(name, "GENERATED.md") &&
				strings.Contains(al[i], "`"+typ+".") && strings.Contains(bl[i], "`"+typ+".")
			if !overridesLine && !mdRow {
				return name + ": non-provenance change at line " + itoa(i+1)
			}
		}
	}
	return ""
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
