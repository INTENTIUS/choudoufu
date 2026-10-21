// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The azure lane's record-store helpers in live/e2e/lib/gauntlet.sh
// (#1120): corpus-azapi-role-assignment keeps its records in a blob
// container on floci-az, record_store "azurerm", and every stage gate that
// used to read .tofu-records now reads the container through these.

// TestGauntletFlociAzRecordStoreMakesTheAccountTheStoreAccepts drives
// gauntlet_floci_az_record_store with gauntlet_arm replaced by a logger:
// the account, its three settings and each container, in the order an
// operator would make them, with the settings record_store "azurerm"
// refuses an account without.
func TestGauntletFlociAzRecordStoreMakesTheAccountTheStoreAccepts(t *testing.T) {
	logf := filepath.Join(t.TempDir(), "calls.log")
	runBash(t, `source e2e/lib/gauntlet.sh
gauntlet_arm() { printf '%s\t%s\t%s\n' "$1" "$2" "${3:-}" >> "$LOGF"; }
gauntlet_floci_az_record_store recs acct adopted green`, "LOGF="+logf)
	calls := strings.Split(strings.TrimRight(readFile(t, logf), "\n"), "\n")
	acct := "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/recs/providers/Microsoft.Storage/storageAccounts/acct"
	want := []string{
		"PUT\t/subscriptions/00000000-0000-0000-0000-000000000001/resourcegroups/recs?api-version=2021-04-01",
		"PUT\t" + acct + "?api-version=2023-05-01\t",
		"PUT\t" + acct + "/blobServices/default?api-version=2023-05-01\t",
		"PUT\t" + acct + "/managementPolicies/default?api-version=2023-05-01\t",
		"PUT\t" + acct + "/blobServices/default/containers/adopted?api-version=2023-05-01\t",
		"PUT\t" + acct + "/blobServices/default/containers/green?api-version=2023-05-01\t",
	}
	if len(calls) != len(want) {
		t.Fatalf("want %d ARM calls, got %d:\n%s", len(want), len(calls), strings.Join(calls, "\n"))
	}
	for i, w := range want {
		if !strings.HasPrefix(calls[i], w) {
			t.Errorf("call %d = %q, want it to start %q", i, calls[i], w)
		}
	}
	for i, body := range map[int]string{
		1: `"allowBlobPublicAccess":false`,
		2: `"isVersioningEnabled":true`,
		3: `"version":{"delete":{"daysAfterCreationGreaterThan":30}}`,
	} {
		if !strings.Contains(calls[i], body) {
			t.Errorf("call %d does not send %s: %q", i, body, calls[i])
		}
	}
	// A rule that deletes current blobs, or one scoped to a prefix the
	// records do not sit under, is refused by the store's opening check.
	if strings.Contains(calls[3], "baseBlob") || strings.Contains(calls[3], "prefixMatch") {
		t.Errorf("the lifecycle rule touches current blobs or is prefix-scoped: %q", calls[3])
	}
}

// TestGauntletFlociAzBlobMirrorReadsRecordsBack serves a container listing
// in two pages over TLS and checks that the mirror copies every blob, so
// gauntlet_record_file finds a record by its envelope's address exactly as
// it does in a local store.
func TestGauntletFlociAzBlobMirrorReadsRecordsBack(t *testing.T) {
	const (
		recName  = "tofu-records/corpus-azapi-role-assignment/azapi_resource/YXphcGlfcmVzb3VyY2Uucm9sZUFzc2lnbm1lbnRz"
		sentinel = "tofu-records/corpus-azapi-role-assignment/.store-sentinel"
	)
	var unauthorized []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer floci-az" || r.Header.Get("x-ms-version") == "" {
			unauthorized = append(unauthorized, r.URL.String())
		}
		switch {
		case r.URL.Path == "/acct/adopted" && r.URL.Query().Get("comp") == "list" && r.URL.Query().Get("marker") == "":
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?><EnumerationResults><Blobs><Blob><Name>` + recName + `</Name></Blob></Blobs><NextMarker>page2</NextMarker></EnumerationResults>`))
		case r.URL.Path == "/acct/adopted" && r.URL.Query().Get("marker") == "page2":
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?><EnumerationResults><Blobs><Blob><Name>` + sentinel + `</Name></Blob></Blobs><NextMarker/></EnumerationResults>`))
		case r.URL.Path == "/acct/adopted/"+recName:
			_, _ = w.Write([]byte(`{"address":"azapi_resource.roleAssignments","identity":{"import_id":"/subscriptions/s/resourceGroups/acctest0001/providers/Microsoft.Authorization/roleAssignments/g"}}`))
		case r.URL.Path == "/acct/adopted/"+sentinel:
			_, _ = w.Write([]byte("sentinel"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "mirror")
	out := runBash(t, `source e2e/lib/gauntlet.sh
n="$(gauntlet_floci_az_blob_mirror acct adopted "$D")" || exit 1
printf 'copied=%s\n' "$n"
printf 'record=%s\n' "$(gauntlet_record_file "$D" azapi_resource.roleAssignments)"
printf 'envelopes=%s\n' "$(gauntlet_record_envelope_count "$D")"`,
		"FLOCI_AZ_ENDPOINT="+srv.URL, "FLOCI_AZ_CA_FILE="+ca, "D="+dir)
	for _, want := range []string{"copied=2", "record=" + filepath.Join(dir, recName), "envelopes=1"} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("the mirror's output lacks %q:\n%s", want, out)
		}
	}
	if len(unauthorized) != 0 {
		t.Errorf("requests without floci-az's bearer token or an x-ms-version: %v", unauthorized)
	}
}

// TestGauntletFlociAzRequestsBetweenCountsOneSpan reads a stubbed container
// log: the requests between the two marks are counted, the marks and
// everything outside them are not, and a span whose end mark is missing (an
// access log that is off writes no marks at all) is a failure, never 0.
func TestGauntletFlociAzRequestsBetweenCountsOneSpan(t *testing.T) {
	log := strings.Join([]string{
		`127.0.0.1 - - [21/Oct/2026:10:00:00 +0000] "GET /subscriptions/s/resourceGroups/rg?api-version=1 HTTP/1.1" 200 10`,
		`127.0.0.1 - - [21/Oct/2026:10:00:01 +0000] "GET /ready?gauntlet-mark=p-start HTTP/1.1" 200 2`,
		`127.0.0.1 - - [21/Oct/2026:10:00:02 +0000] "POST /tenant/oauth2/v2.0/token HTTP/1.1" 200 900`,
		`2026-10-21 10:00:02,100 INFO  [i.f.a.s.a.ArmHandler] some log line that is not a request`,
		`127.0.0.1 - - [21/Oct/2026:10:00:03 +0000] "POST /providers/Microsoft.ResourceGraph/resources?api-version=2022-10-01 HTTP/1.1" 200 50`,
		`127.0.0.1 - - [21/Oct/2026:10:00:04 +0000] "GET /choudoufuazrarecords/green?restype=container&comp=list HTTP/1.1" 200 400`,
		`127.0.0.1 - - [21/Oct/2026:10:00:05 +0000] "GET /ready?gauntlet-mark=p-end HTTP/1.1" 200 2`,
		`127.0.0.1 - - [21/Oct/2026:10:00:06 +0000] "PUT /after HTTP/1.1" 200 2`,
	}, "\n")
	logf := filepath.Join(t.TempDir(), "docker.log")
	if err := os.WriteFile(logf, []byte(log+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := runBash(t, `source e2e/lib/gauntlet.sh
docker() { [ "$1" = logs ] && cat "$LOGF"; }
printf 'span=%s\n' "$(gauntlet_floci_az_requests_between c p-start p-end)"
if gauntlet_floci_az_requests_between c p-start missing-end >/dev/null; then echo open=ok; else echo open=refused; fi`, "LOGF="+logf)
	if !strings.Contains(out, "span=3\n") {
		t.Errorf("want the three requests between the marks counted:\n%s", out)
	}
	if !strings.Contains(out, "open=refused\n") {
		t.Errorf("a span with no end mark was counted rather than refused:\n%s", out)
	}
}
