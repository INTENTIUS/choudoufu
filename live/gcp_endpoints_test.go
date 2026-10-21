// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// A gcp-lane run talks only to floci-gcp. hashicorp/google reads each
// product's endpoint from its own *_custom_endpoint argument, and one the
// harness's provider block leaves unset goes to googleapis.com: the first
// reference-gcp and gcve runs (2026-10-20) sent google_service_account to the
// real IAM API (its client reads iam_beta_custom_endpoint) and the
// google_project read to the real Cloud Billing API. These tests hold both
// provider blocks, the gauntlet's and smoke's, to every argument the pinned
// provider accepts, and to the harness's fake token.
//
// Proven red three ways (2026-10-20): the e2e fallback skipping
// cloud_billing_custom_endpoint (with its named line removed) fails "is
// unset"; smoke's block without its access_token line fails the token check;
// e2e's gcp_harness_token accepting any token fails
// TestGCPProviderBlockRefusesARealToken.
//
// What this cannot see: a name missing from gcp-provider-endpoints.txt
// itself, since the list is the schema's copy and CI has no provider to read
// the schema from. The header must name the pinned version and the list must
// hold at least 150 names; regenerate it with the command in its header on
// every re-pin.

const gcpEndpointsFile = "e2e/lib/gcp-provider-endpoints.txt"

var endpointLine = regexp.MustCompile(`^\s*([a-z0-9_]+_custom_endpoint) = "([^"]*)"$`)

// gcpPinnedEndpoints reads the list and checks its header names the pinned
// provider version, so a re-pin without regenerating the list fails here.
func gcpPinnedEndpoints(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(gcpEndpointsFile)
	if err != nil {
		t.Fatal(err)
	}
	var pins struct {
		Google string `json:"google_provider_version"`
	}
	vraw, err := os.ReadFile("oracle-versions.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(vraw, &pins); err != nil || pins.Google == "" {
		t.Fatalf("oracle-versions.json has no google_provider_version: %v", err)
	}
	lines := strings.Split(string(raw), "\n")
	if !strings.Contains(lines[0], "hashicorp/google "+pins.Google+":") {
		t.Fatalf("%s's header is %q; it must be regenerated for the pinned hashicorp/google %s", gcpEndpointsFile, lines[0], pins.Google)
	}
	var names []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if !strings.HasSuffix(l, "_custom_endpoint") {
			t.Fatalf("%s lists %q, which is not a *_custom_endpoint argument", gcpEndpointsFile, l)
		}
		names = append(names, l)
	}
	// Sanity floor: 8.6.0 has 182. A truncated file must not pass as a short
	// list that the block then trivially covers.
	if len(names) < 150 {
		t.Fatalf("%s lists %d arguments; hashicorp/google %s has 182", gcpEndpointsFile, len(names), pins.Google)
	}
	return names
}

// renderGCPBlock sources a lib in bash and prints its provider block.
func renderGCPBlock(t *testing.T, lib, token string) (string, error) {
	t.Helper()
	const ep = "http://127.0.0.1:4788"
	var script string
	switch lib {
	case "e2e":
		script = `source e2e/lib/gcp.sh && gcp_provider_block "$EP" p us-central1 us-central1-a`
	case "smoke":
		script = `source smoke/lib.sh >/dev/null 2>&1; gcp_provider_block p`
	}
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(),
		"EP="+ep, "GCP_ENDPOINT="+ep, "GOOGLE_OAUTH_ACCESS_TOKEN="+token,
		"SMOKE_GCP_PROJECT=p", "SMOKE_GCP_REGION=us-central1", "SMOKE_GCP_ZONE=us-central1-a")
	out, err := cmd.Output()
	return string(out), err
}

func TestGCPProviderBlockSetsEveryEndpoint(t *testing.T) {
	want := gcpPinnedEndpoints(t)
	for _, lib := range []string{"e2e", "smoke"} {
		t.Run(lib, func(t *testing.T) {
			out, err := renderGCPBlock(t, lib, "chdf-test-fake-token")
			if err != nil {
				t.Fatalf("rendering the block failed: %v\n%s", err, out)
			}
			got := map[string]string{}
			for _, l := range strings.Split(out, "\n") {
				if m := endpointLine.FindStringSubmatch(l); m != nil {
					got[m[1]] = m[2]
				}
			}
			for _, name := range want {
				v, ok := got[name]
				switch {
				case !ok:
					t.Errorf("%s is unset: that product's requests would go to googleapis.com", name)
				case !strings.HasPrefix(v, "http://127.0.0.1:4788/"):
					t.Errorf("%s = %q, not the emulator", name, v)
				}
			}
			if strings.Contains(out, "googleapis.com") {
				t.Errorf("the block names googleapis.com:\n%s", out)
			}
			if !strings.Contains(out, `access_token = "chdf-test-fake-token"`) {
				t.Errorf("the block does not set the harness's fake access_token, so the provider could fall back to Application Default Credentials:\n%s", out)
			}
		})
	}
}

func TestGCPProviderBlockRefusesARealToken(t *testing.T) {
	for _, lib := range []string{"e2e", "smoke"} {
		t.Run(lib, func(t *testing.T) {
			out, err := renderGCPBlock(t, lib, "ya29.a-real-looking-token")
			if err == nil {
				t.Errorf("a non-chdf token rendered a block instead of refusing:\n%s", out)
			}
			if strings.Contains(out, "ya29.") {
				t.Errorf("the real-looking token was written out:\n%s", out)
			}
		})
	}
}
