// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// GitHub issue #1120's stage audit, the parts of it that live under live/.

// TestAzureCorpusWriteRootKeepsItsDirectory: corpus-azapi-role-assignment's
// write_root rewrites the adopted root and the oracle's at every stage from
// plan_approval on, and those roots hold what no stage writes again: the
// providers init installed (.terraform and its lock file), the estate's
// record store (.tofu-records, the role assignment's only marker) and, on
// the oracle, stock's terraform.tfstate. write_root cleared its destination
// first (rm -rf), so plan_approval's plan would have found no providers and
// stock's oracle no state. rm is stubbed to log here, so the old behaviour
// shows as a call rather than as files deleted.
func TestAzureCorpusWriteRootKeepsItsDirectory(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "main.tf"), []byte(corpusRootHeader), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "root")
	kept := map[string]string{
		".terraform/providers/marker":                      "installed",
		".terraform.lock.hcl":                              "# lock",
		".tofu-records/tofu-records/azapi_resource/record": `{"address":"azapi_resource.role_assignment"}`,
		"terraform.tfstate":                                `{"version":4}`,
		"approved.tfplan":                                  "plan",
	}
	stale := map[string]string{"moved.tf": "# stale", "shards.tf": "# stale", "replace.tf": "# stale", "crash.tf": "# stale"}
	for _, files := range []map[string]string{kept, stale} {
		for name, body := range files {
			p := filepath.Join(dst, name)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	rmLog := filepath.Join(t.TempDir(), "rm.log")
	drive := func(env string) {
		t.Helper()
		runBash(t, `source e2e/lib/gauntlet.sh
rm() { printf 'rm %s\n' "$*" >> "$RMLOG"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
eval "$(awk '/^write_root\(\) \{/{on=1} /^# ── substrate helpers/{on=0} on' e2e/corpus-azapi-role-assignment/run.sh)"
SRC="$CSRC" RA_GUID=6faae21a-0cd6-4536-8c23-a278823d12ed ESTATE=corpus-azapi-role-assignment
AZAPI_REQUIRED_PROVIDER="$(gauntlet_azapi_required_provider)" AZURERM_REQUIRED_PROVIDER="$(gauntlet_azurerm_required_provider)"
`+env+` write_root "$DST" stock`, "CSRC="+src, "DST="+dst, "RMLOG="+rmLog)
	}

	drive("")
	if b, err := os.ReadFile(rmLog); err == nil && len(b) > 0 {
		t.Errorf("write_root ran rm, which throws away what the root holds between stages:\n%s", b)
	}
	for name, body := range kept {
		if b, err := os.ReadFile(filepath.Join(dst, name)); err != nil || string(b) != body {
			t.Errorf("write_root did not keep %s (read %q, %v)", name, b, err)
		}
	}
	for name := range stale {
		if _, err := os.Stat(filepath.Join(dst, name)); !os.IsNotExist(err) {
			t.Errorf("write_root with MOVED, SHARDS, REPLACE and CRASH unset left %s in place (%v), so the root declares a block the shape has not got", name, err)
		}
	}
	if b, err := os.ReadFile(filepath.Join(dst, "main.tf")); err != nil || !strings.Contains(string(b), `provider "azapi"`) {
		t.Errorf("write_root did not write main.tf (%v)", err)
	}

	drive("MOVED=1 SHARDS=2")
	if b, err := os.ReadFile(filepath.Join(dst, "moved.tf")); err != nil || !strings.Contains(string(b), "to   = azapi_resource.role_assignment") {
		t.Errorf("MOVED=1 wrote no moved block (%q, %v)", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "shards.tf")); err != nil || !strings.Contains(string(b), "count     = 2") {
		t.Errorf("SHARDS=2 wrote no two-instance count (%q, %v)", b, err)
	}

	// day2_replace's and day2_crash's pairs (#1120): generation b names the
	// identity and the grant both by b, create_before_destroy on each; the
	// crash pair's second resource depends on its first.
	drive("REPLACE=b CRASH=both GRANT_GUID_A=aaaaaaaa-0000-4000-8000-000000000001 GRANT_GUID_B=bbbbbbbb-0000-4000-8000-000000000002 CRASH_GUID=cccccccc-0000-4000-8000-000000000003")
	rep := readFile(t, filepath.Join(dst, "replace.tf"))
	for _, want := range []string{`-rotating-b"`, `name      = "bbbbbbbb-0000-4000-8000-000000000002"`, "principalId      = azapi_resource.rotating.output.properties.principalId"} {
		if !strings.Contains(rep, want) {
			t.Errorf("REPLACE=b wrote a replace.tf without %q:\n%s", want, rep)
		}
	}
	if n := strings.Count(rep, "create_before_destroy = true"); n != 2 {
		t.Errorf("replace.tf has %d create_before_destroy blocks, want 2 (the identity and its grant)", n)
	}
	crash := readFile(t, filepath.Join(dst, "crash.tf"))
	for _, want := range []string{`resource "azapi_resource" "crash_first"`, `name      = "cccccccc-0000-4000-8000-000000000003"`, "depends_on = [azapi_resource.crash_first]"} {
		if !strings.Contains(crash, want) {
			t.Errorf("CRASH=both wrote a crash.tf without %q:\n%s", want, crash)
		}
	}
	drive("CRASH=first CRASH_GUID=cccccccc-0000-4000-8000-000000000003")
	if crash := readFile(t, filepath.Join(dst, "crash.tf")); strings.Contains(crash, "crash_second") {
		t.Errorf("CRASH=first, the oracle's position, still declares crash_second:\n%s", crash)
	}
	if _, err := os.Stat(filepath.Join(dst, "replace.tf")); !os.IsNotExist(err) {
		t.Errorf("a shape with REPLACE unset left replace.tf in place (%v)", err)
	}
}

// smokeNetworkPatch is an ARM PATCH a smoke scenario sends at a
// Microsoft.Network resource, read by the api-version variable every Azure
// scenario names for that namespace.
var smokeNetworkPatch = regexp.MustCompile(`\barm\s+PATCH\b[^\n]*NET_API`)

// TestAzureSmokeSendsNoNetworkPatch: az-identity-is-a-tag.sh's BREAK control
// stripped the VNet's tofu-estate tag with an ARM PATCH, and floci-az answers
// 405 to every PATCH on Microsoft.Network (lex00/floci-az#18), so the control
// would have failed before it measured anything. The scenarios' tag edit is
// arm_set_tag, a GET and a PUT of the body read back.
func TestAzureSmokeSendsNoNetworkPatch(t *testing.T) {
	paths, err := filepath.Glob("smoke/drafts/az-*.sh")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no Azure scripts found under live/smoke/drafts (%v)", err)
	}
	for _, p := range paths {
		for i, ln := range strings.Split(readFile(t, p), "\n") {
			if smokeNetworkPatch.MatchString(ln) {
				t.Errorf("%s:%d sends an ARM PATCH to a Microsoft.Network resource, which floci-az answers 405; use arm_set_tag: %s", p, i+1, strings.TrimSpace(ln))
			}
		}
	}
	if !strings.Contains(readFile(t, "smoke/drafts/az-identity-is-a-tag.sh"), `arm_set_tag "${VNET_ID}?api-version=${NET_API}" tofu-estate -`) {
		t.Error("az-identity-is-a-tag.sh's BREAK control no longer strips the VNet's tag through arm_set_tag; this test would be guarding nothing")
	}
}

// TestSmokeARMSetTagGetsThenPuts drives live/smoke/lib.sh's arm_set_tag, read
// out of the library, with arm replaced by a logger that answers the GET.
func TestSmokeARMSetTagGetsThenPuts(t *testing.T) {
	dir := t.TempDir()
	get := filepath.Join(dir, "get.json")
	logf := filepath.Join(dir, "calls.log")
	vnet := `{"id":"/subscriptions/s/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/v","location":"eastus","tags":{"Tofu-Estate":"smoke-az","tofu-address":"azurerm_virtual_network.app"},"properties":{"subnets":[{"name":"s1"}]}}`
	if err := os.WriteFile(get, []byte(vnet), 0o600); err != nil {
		t.Fatal(err)
	}
	runBash(t, `eval "$(awk '/^arm_set_tag\(\) \{/{on=1} on{print} on&&/^\}$/{exit}' smoke/lib.sh)"
arm() { printf '%s\t%s\t%s\n' "$1" "$2" "${3:-}" >> "$LOGF"; [ "$1" = GET ] && cat "$GETF"; return 0; }
arm_set_tag "/v?api-version=2024-05-01" tofu-estate -`, "LOGF="+logf, "GETF="+get)
	b, err := os.ReadFile(logf)
	if err != nil {
		t.Fatal(err)
	}
	calls := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(calls) != 2 || !strings.HasPrefix(calls[0], "GET\t/v?api-version=2024-05-01") || !strings.HasPrefix(calls[1], "PUT\t/v?api-version=2024-05-01\t") {
		t.Fatalf("want a GET then a PUT at the same path and nothing else, got %q", calls)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(strings.SplitN(calls[1], "\t", 3)[2]), &body); err != nil {
		t.Fatalf("PUT body: %v", err)
	}
	if tags, _ := body["tags"].(map[string]any); len(tags) != 1 || tags["tofu-address"] != "azurerm_virtual_network.app" {
		t.Errorf("removing tofu-estate (stored as Tofu-Estate) left tags %v, want tofu-address alone", body["tags"])
	}
	if props, _ := body["properties"].(map[string]any); props == nil || len(props["subnets"].([]any)) != 1 || body["location"] != "eastus" {
		t.Errorf("the PUT dropped fields it read back, which a real ARM PUT would clear: %v", body)
	}
}
