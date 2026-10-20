// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestFlociAzServiceEnvMatchesCompose holds the gauntlet's floci-az start
// to the smoke compose service's (#2140): every FLOCI_AZ_* variable the
// compose service sets, gauntlet_floci_az_up passes too, either as a
// literal -e or through live/e2e/lib/floci-az.sh's floci_az_service_env,
// which live/smoke/lib.sh sources as well. The audit found the gauntlet
// start without the storage suffix, Policy evaluation and RBAC
// enforcement the compose service had.
func TestFlociAzServiceEnvMatchesCompose(t *testing.T) {
	compose := flociAzComposeEnvKeys(t, readFile(t, "smoke/docker-compose.yml"))
	gauntlet := flociAzGauntletEnvKeys(t, readFile(t, "e2e/lib/gauntlet.sh"), readFile(t, "e2e/lib/floci-az.sh"))
	if missing := minus(compose, gauntlet); len(missing) > 0 {
		t.Errorf("the smoke compose service sets %v and gauntlet_floci_az_up does not", missing)
	}
	if extra := minus(gauntlet, compose); len(extra) > 0 {
		t.Errorf("gauntlet_floci_az_up sets %v and the smoke compose service does not", extra)
	}
	if lib := readFile(t, "smoke/lib.sh"); !strings.Contains(lib, `. "$ROOT/live/e2e/lib/floci-az.sh"`) {
		t.Error("live/smoke/lib.sh does not source live/e2e/lib/floci-az.sh, so its azure_up and the gauntlet's can drift")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// flociAzComposeEnvKeys is the FLOCI_AZ_* keys of the floci-az service's
// environment block.
func flociAzComposeEnvKeys(t *testing.T, compose string) []string {
	t.Helper()
	i := strings.Index(compose, "\n  floci-az:\n")
	if i < 0 {
		t.Fatal("docker-compose.yml has no floci-az service")
	}
	svc := compose[i+1:]
	if j := regexp.MustCompile(`\n  [a-z][a-z0-9-]*:\n`).FindStringIndex(svc[1:]); j != nil {
		svc = svc[:j[0]+1]
	}
	var keys []string
	for _, m := range regexp.MustCompile(`(?m)^\s+(FLOCI_AZ_[A-Z_]+):`).FindAllStringSubmatch(svc, -1) {
		keys = append(keys, m[1])
	}
	if len(keys) == 0 {
		t.Fatal("found no FLOCI_AZ_* keys in the floci-az service")
	}
	return keys
}

// flociAzGauntletEnvKeys is the FLOCI_AZ_* keys gauntlet_floci_az_up
// passes: its literal -e flags, and floci_az_service_env's when it calls it.
func flociAzGauntletEnvKeys(t *testing.T, gauntlet, lib string) []string {
	t.Helper()
	i := strings.Index(gauntlet, "\ngauntlet_floci_az_up() {")
	if i < 0 {
		t.Fatal("gauntlet.sh has no gauntlet_floci_az_up")
	}
	body := gauntlet[i:]
	body = body[:strings.Index(body, "\n}\n")]
	var keys []string
	for _, m := range regexp.MustCompile(`-e (FLOCI_AZ_[A-Z_]+)=`).FindAllStringSubmatch(body, -1) {
		keys = append(keys, m[1])
	}
	if strings.Contains(body, "floci_az_service_env ") {
		j := strings.Index(lib, "\nfloci_az_service_env() {")
		if j < 0 {
			t.Fatal("floci-az.sh has no floci_az_service_env")
		}
		fn := lib[j:]
		fn = fn[:strings.Index(fn, "\n}\n")]
		for _, m := range regexp.MustCompile(`"(FLOCI_AZ_[A-Z_]+)=`).FindAllStringSubmatch(fn, -1) {
			keys = append(keys, m[1])
		}
	}
	return keys
}

func minus(a, b []string) []string {
	in := map[string]bool{}
	for _, k := range b {
		in[k] = true
	}
	var out []string
	for _, k := range a {
		if !in[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
