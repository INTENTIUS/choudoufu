// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// azureStorageEndpointTypes is every data-plane service azurerm 5.10.0
// builds a storage account URL for, https://<account>.<type>.<suffix>
// (internal/services/storage/client/helpers.go, EndpointType). floci-az's
// primaryEndpoints omit file, so azurerm always builds that one from the
// storage suffix.
var azureStorageEndpointTypes = []string{"blob", "dfs", "file", "queue", "table"}

// TestFlociAzCertCoversHarnessHosts holds scripts/floci-az-mac-cert.sh's
// SAN list to the hosts the Azure harness aims clients at: the host of
// AZURE_HOST and FLOCI_AZ_HOST (ARM, metadata, token endpoint), the address
// floci-az is published on, and <account>.<type>.<suffix host> for every
// storage endpoint type under FLOCI_AZ_STORAGE_SUFFIX. floci-az adds no SAN
// to a certificate it is handed, so a host the harness starts using that
// the list lacks would fail on the Mac with an x509 error, and fails here
// first.
func TestFlociAzCertCoversHarnessHosts(t *testing.T) {
	sans := flociAzCertSANs(t, readFile(t, "../scripts/floci-az-mac-cert.sh"))
	have := map[string]bool{}
	for _, s := range sans {
		have[s] = true
	}
	var missing []string
	for _, want := range flociAzHarnessSANs(t, readFile(t, "smoke/lib.sh"), readFile(t, "e2e/lib/gauntlet.sh"),
		readFile(t, "e2e/lib/floci-az.sh"), readFile(t, "smoke/docker-compose.yml")) {
		if !have[want] {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		t.Errorf("scripts/floci-az-mac-cert.sh's SANS lacks %v, which the Azure harness dials; add them there and to live/FLOCI.md's Azure host list", missing)
	}
}

// TestFlociAzCertMountAgrees holds the three places that name where the
// operator's certificate sits in the container to one path: the overlay
// compose file's mounts, floci_az_tls_env's FLOCI_AZ_TLS_*_PATH values and
// FLOCI_AZ_TLS_IN_CONTAINER, which gauntlet_floci_az_up mounts at.
func TestFlociAzCertMountAgrees(t *testing.T) {
	lib := readFile(t, "e2e/lib/floci-az.sh")
	m := regexp.MustCompile(`(?m)^FLOCI_AZ_TLS_IN_CONTAINER=(\S+)$`).FindStringSubmatch(lib)
	if m == nil {
		t.Fatal("floci-az.sh does not set FLOCI_AZ_TLS_IN_CONTAINER")
	}
	dir := m[1]
	env := shellFunc(t, lib, "floci_az_tls_env")
	overlay := readFile(t, "smoke/docker-compose.floci-az-tls.yml")
	for _, f := range []struct{ key, file string }{{"FLOCI_AZ_TLS_CERT_PATH", "cert.pem"}, {"FLOCI_AZ_TLS_KEY_PATH", "key.pem"}} {
		if !strings.Contains(env, `"`+f.key+`=$FLOCI_AZ_TLS_IN_CONTAINER/`+f.file+`"`) {
			t.Errorf("floci_az_tls_env does not set %s to $FLOCI_AZ_TLS_IN_CONTAINER/%s", f.key, f.file)
		}
		if !strings.Contains(overlay, ":"+dir+"/"+f.file+":ro") {
			t.Errorf("docker-compose.floci-az-tls.yml does not mount %s read-only at %s/%s", f.file, dir, f.file)
		}
	}
}

// flociAzCertSANs is the SANS array of floci-az-mac-cert.sh, as written
// there (DNS:<name> or IP:<address>).
func flociAzCertSANs(t *testing.T, script string) []string {
	t.Helper()
	m := regexp.MustCompile(`(?s)\nSANS=\(\n(.*?)\n\)\n`).FindStringSubmatch(script)
	if m == nil {
		t.Fatal("floci-az-mac-cert.sh has no SANS=( ... ) array")
	}
	var out []string
	for _, f := range strings.Fields(m[1]) {
		out = append(out, f)
	}
	if len(out) == 0 {
		t.Fatal("floci-az-mac-cert.sh's SANS array is empty")
	}
	return out
}

// flociAzHarnessSANs derives the SANs the harness needs from its own
// assignments, so a new host there is found without anyone listing it.
func flociAzHarnessSANs(t *testing.T, sources ...string) []string {
	t.Helper()
	all := strings.Join(sources, "\n")
	need := map[string]bool{}
	san := func(host string) string {
		if regexp.MustCompile(`^[0-9.]+$|:`).MatchString(host) {
			return "IP:" + host
		}
		return "DNS:" + host
	}
	hosts := regexp.MustCompile(`\b(?:AZURE_HOST|FLOCI_AZ_HOST)="([^"$:]+):`).FindAllStringSubmatch(all, -1)
	if len(hosts) == 0 {
		t.Fatal("found no AZURE_HOST or FLOCI_AZ_HOST assignment in the harness")
	}
	for _, m := range hosts {
		need[san(m[1])] = true
	}
	published := regexp.MustCompile(`"([0-9.]+):\$(?:\{FLOCI_AZ_PORT[^}]*\}|port):4577"`).FindAllStringSubmatch(all, -1)
	if len(published) == 0 {
		t.Fatal("found no published address for floci-az's port 4577 in the harness")
	}
	for _, m := range published {
		need[san(m[1])] = true
	}
	suffixes := regexp.MustCompile(`FLOCI_AZ_STORAGE_SUFFIX="?([^"$:]+):`).FindAllStringSubmatch(all, -1)
	if len(suffixes) == 0 {
		t.Fatal("found no FLOCI_AZ_STORAGE_SUFFIX assignment in the harness")
	}
	for _, m := range suffixes {
		for _, typ := range azureStorageEndpointTypes {
			need["DNS:*."+typ+"."+m[1]] = true
		}
	}
	var out []string
	for k := range need {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
