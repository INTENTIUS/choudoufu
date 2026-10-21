// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// GitHub issue #1120's second Azure lane run. reference-azure crossed
// migrate, test_plan and test_apply, then drift_reconverge stopped at its
// first line: the tamper PATCHed the VNet's tags and floci-az answered 405,
// because its Microsoft.Network handler serves PUT, GET and DELETE only
// (lex00/floci-az 30000b0, NetworkService.handleArm). The log was
// live/gauntlet/logs/reference-azure.20261021T032252Z.fail.log:
//
//	curl: (22) The requested URL returned error: 405
//	FAIL: could not tamper the VNet on A
//
// The tamper is now gauntlet_arm_set_tag: a GET, the tag edited in the body
// read back, and a PUT of that whole body, which real ARM and floci-az both
// serve on every type the two Azure estates touch.

var azureEstates = []string{"reference-azure", "corpus-azapi-role-assignment"}

// armPatchCall is any ARM PATCH the harness sends itself, through the
// library's helper or a bare curl.
var armPatchCall = regexp.MustCompile(`gauntlet_arm\s+PATCH\b|curl\b[^\n]*-X\s*PATCH\b`)

func TestAzureEstatesSendNoARMPatch(t *testing.T) {
	root := testRoot(t)
	for _, estate := range azureEstates {
		path := filepath.Join(root, "live", "e2e", estate, "run.sh")
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for i, ln := range strings.Split(string(b), "\n") {
			if armPatchCall.MatchString(ln) {
				t.Errorf("%s:%d sends an ARM PATCH, which floci-az answers 405 on Microsoft.Network; use gauntlet_arm_set_tag: %s", path, i+1, strings.TrimSpace(ln))
			}
		}
		verdictScriptFunc(t, estate, "", `tag_set() { "on_$1" gauntlet_arm_set_tag "$2?api-version=$3" "$4" "$5" >/dev/null; }`)
		verdictScriptFunc(t, estate, "", `tag_set a "$`)
	}
}

// driveSetTag sources the real library with curl replaced by a function
// that answers a GET with getBody, logs every call's method and body to a
// file, and returns that log.
func driveSetTag(t *testing.T, getBody, key, val string) []string {
	t.Helper()
	lib := filepath.Join(testRoot(t), "live", "e2e", "lib", "gauntlet.sh")
	dir := t.TempDir()
	logf := filepath.Join(dir, "calls.log")
	get := filepath.Join(dir, "get.json")
	if err := os.WriteFile(get, []byte(getBody), 0o600); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`set -euo pipefail
source %q
FLOCI_AZ_CA_FILE=/dev/null FLOCI_AZ_ENDPOINT=https://127.0.0.1:1
curl() {
  local m="" d="" u=""
  while [ $# -gt 0 ]; do
    case "$1" in
      -X) m="$2"; shift 2 ;;
      --data) d="$2"; shift 2 ;;
      --cacert|--max-time|-H) shift 2 ;;
      -*) shift ;;
      *) u="$1"; shift ;;
    esac
  done
  printf '%%s %%s %%s\n' "$m" "$u" "$d" >> %q
  [ "$m" = GET ] && cat %q
  return 0
}
gauntlet_arm_set_tag "/subscriptions/s/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/v?api-version=2024-05-01" %q %q
`, lib, logf, get, key, val)
	if out, err := runBash(script); err != nil {
		t.Fatalf("bash: %v\n%s", err, out)
	}
	b, err := os.ReadFile(logf)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

const vnetGet = `{"id":"/subscriptions/s/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/v","name":"v","type":"Microsoft.Network/virtualNetworks","location":"eastus","tags":{"tofu-estate":"reference-azure","Drift":"old"},"properties":{"addressSpace":{"addressPrefixes":["10.0.0.0/16"]},"subnets":[{"name":"s1"}]}}`

func TestARMSetTagGetsThenPutsTheWholeBody(t *testing.T) {
	putBody := func(t *testing.T, calls []string) map[string]any {
		t.Helper()
		if len(calls) != 2 || !strings.HasPrefix(calls[0], "GET ") || !strings.HasPrefix(calls[1], "PUT ") {
			t.Fatalf("want a GET then a PUT and nothing else, got %q", calls)
		}
		url := "https://127.0.0.1:1/subscriptions/s/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/v?api-version=2024-05-01"
		for _, c := range calls {
			if strings.Fields(c)[1] != url {
				t.Errorf("call %q is not at %s", c, url)
			}
		}
		var body map[string]any
		if err := json.Unmarshal([]byte(strings.SplitN(calls[1], " ", 3)[2]), &body); err != nil {
			t.Fatalf("PUT body: %v: %q", err, calls[1])
		}
		props, _ := body["properties"].(map[string]any)
		if subnets, _ := props["subnets"].([]any); len(subnets) != 1 {
			t.Errorf("the PUT dropped the VNet's subnets, which a real ARM PUT would delete: %v", body)
		}
		if body["location"] != "eastus" {
			t.Errorf("the PUT dropped location: %v", body)
		}
		return body
	}

	t.Run("set replaces a key that differs only by case and keeps the marker", func(t *testing.T) {
		body := putBody(t, driveSetTag(t, vnetGet, "drift", "yes"))
		tags, _ := body["tags"].(map[string]any)
		want := map[string]any{"tofu-estate": "reference-azure", "drift": "yes"}
		if fmt.Sprint(tags) != fmt.Sprint(want) {
			t.Errorf("tags %v, want %v", tags, want)
		}
	})
	t.Run("a dash removes the key and keeps the rest", func(t *testing.T) {
		body := putBody(t, driveSetTag(t, vnetGet, "drift", "-"))
		tags, _ := body["tags"].(map[string]any)
		want := map[string]any{"tofu-estate": "reference-azure"}
		if fmt.Sprint(tags) != fmt.Sprint(want) {
			t.Errorf("tags %v, want %v", tags, want)
		}
	})
	t.Run("a resource with no tags gains one", func(t *testing.T) {
		body := putBody(t, driveSetTag(t, `{"location":"eastus","properties":{"subnets":[{}]}}`, "stray", "yes"))
		if tags, _ := body["tags"].(map[string]any); fmt.Sprint(tags) != "map[stray:yes]" {
			t.Errorf("tags %v", tags)
		}
	})
}
