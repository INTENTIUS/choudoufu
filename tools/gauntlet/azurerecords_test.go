// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"

	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// The azure lane's records live in record_store "azurerm" on floci-az's blob
// storage (the maintainer's ruling of 2026-10-21). live/e2e/lib/gauntlet.sh
// carries three generic helpers for it; these drive two of them against a
// local TLS server standing in for floci-az, since no run of the lane has
// measured them. gauntlet_floci_az_record_store's block is rendered by
// live/azure_endpoints_test.go's TestAzureReferenceProviderBlocksAreLocked.

// fakeFlociAz is the slice of floci-az the helpers reach: ARM PUTs, recorded,
// and one blob container served path-style.
type fakeFlociAz struct {
	mu    sync.Mutex
	puts  map[string]string // ARM path (no query) -> body
	blobs map[string]string // blob name -> body
	page  int               // blobs per listing page
}

func (f *fakeFlociAz) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer floci-az" {
		http.Error(w, "no bearer", http.StatusUnauthorized)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/subscriptions/") {
		if r.Method != http.MethodPut || r.URL.Query().Get("api-version") == "" {
			http.Error(w, "unexpected ARM call", http.StatusMethodNotAllowed)
			return
		}
		b, _ := io.ReadAll(r.Body)
		f.puts[r.URL.Path] = string(b)
		_, _ = w.Write(b)
		return
	}
	if r.URL.Path == "/acct/cont" && r.URL.Query().Get("comp") == "list" {
		names := make([]string, 0, len(f.blobs))
		for n := range f.blobs {
			names = append(names, n)
		}
		sortStrings(names)
		start := 0
		if m := r.URL.Query().Get("marker"); m != "" {
			fmt.Sscanf(m, "p%d", &start)
		}
		end := min(start+f.page, len(names))
		var sb strings.Builder
		sb.WriteString(`<?xml version="1.0" encoding="utf-8"?><EnumerationResults><Blobs>`)
		for _, n := range names[start:end] {
			fmt.Fprintf(&sb, "<Blob><Name>%s</Name></Blob>", n)
		}
		sb.WriteString("</Blobs>")
		if end < len(names) {
			fmt.Fprintf(&sb, "<NextMarker>p%d</NextMarker>", end)
		} else {
			sb.WriteString("<NextMarker />")
		}
		sb.WriteString("</EnumerationResults>")
		_, _ = w.Write([]byte(sb.String()))
		return
	}
	if name, ok := strings.CutPrefix(r.URL.Path, "/acct/cont/"); ok {
		if body, ok := f.blobs[name]; ok {
			_, _ = w.Write([]byte(body))
			return
		}
	}
	http.NotFound(w, r)
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// startFakeFlociAz serves f over TLS and returns the environment the
// helpers read: FLOCI_AZ_ENDPOINT and a CA file that trusts the server.
func startFakeFlociAz(t *testing.T, f http.Handler) []string {
	t.Helper()
	srv := httptest.NewTLSServer(f)
	t.Cleanup(srv.Close)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(ca, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return []string{"FLOCI_AZ_ENDPOINT=" + srv.URL, "FLOCI_AZ_CA_FILE=" + ca}
}

func libBash(t *testing.T, env []string, script string) (string, error) {
	t.Helper()
	lib := filepath.Join(testRoot(t), "live", "e2e", "lib", "gauntlet.sh")
	var exports strings.Builder
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		fmt.Fprintf(&exports, "export %s=%q\n", k, v)
	}
	out, err := runBash(fmt.Sprintf("set -uo pipefail\nROOT=%q\nsource %q\n%s%s\n", testRoot(t), lib, exports.String(), script))
	return string(out), err
}

func TestFlociAzBlobsCopiesEveryRecord(t *testing.T) {
	f := &fakeFlociAz{puts: map[string]string{}, page: 2, blobs: map[string]string{
		"tofu-records/reference-azure/azurerm_subnet/YXp1cmVybV9zdWJuZXQuYXBw":                        `{"address":"azurerm_subnet.app","identity":{"attrs":{"name":"refaz-subnet"}}}`,
		"tofu-records/reference-azure/azurerm_network_security_rule/YXp1cmVybV9uZXR3b3JrX3NlY3VyaXR5": `{"address":"azurerm_network_security_rule.ssh"}`,
		"tofu-records/reference-azure/.store-sentinel":                                                `sentinel`,
		"tofu-hints/reference-azure/guided":                                                           `{"hint":true}`,
	}}
	env := startFakeFlociAz(t, f)
	dir := filepath.Join(t.TempDir(), "copy")
	out, err := libBash(t, env, fmt.Sprintf(`n="$(gauntlet_floci_az_blobs acct cont %q)" || exit 9
printf 'copied=%%s\n' "$n"
printf 'records=%%s\n' "$(gauntlet_record_envelope_count %q)"
printf 'subnet=%%s\n' "$(gauntlet_record_file %q azurerm_subnet.app)"
gauntlet_record_file %q azurerm_public_ip.app >/dev/null && echo 'pip=found' || echo 'pip=absent'`, dir, dir, dir, dir))
	if err != nil {
		t.Fatalf("bash: %v\n%s", err, out)
	}
	// Four blobs over two pages, so the NextMarker loop is what reached the
	// last two.
	wantIn(t, out, "copied=4", "records=2", "subnet="+filepath.Join(dir, "tofu-records", "reference-azure", "azurerm_subnet"), "pip=absent")

	// A second reading into the same directory is refused: an earlier copy
	// must never answer for a later one.
	out, err = libBash(t, env, fmt.Sprintf(`gauntlet_floci_az_blobs acct cont %q`, dir))
	if err == nil || !strings.Contains(out, "not empty") {
		t.Errorf("a copy into a non-empty directory was not refused: %v\n%s", err, out)
	}

	// A listing that names a blob the next read cannot find fails the copy
	// rather than coming back short.
	f.mu.Lock()
	f.blobs["tofu-records/reference-azure/azurerm_vnet/gone"] = "x"
	f.mu.Unlock()
	ghost := &fakeFlociAz{puts: map[string]string{}, page: 10, blobs: f.blobs}
	env2 := startFakeFlociAz(t, &listsButLoses{fakeFlociAz: ghost, lose: "tofu-records/reference-azure/azurerm_vnet/gone"})
	out, err = libBash(t, env2, fmt.Sprintf(`gauntlet_floci_az_blobs acct cont %q`, filepath.Join(t.TempDir(), "c2")))
	if err == nil || !strings.Contains(out, "could not read blob") {
		t.Errorf("a blob the listing named and the read could not find did not fail the copy: %v\n%s", err, out)
	}
}

// listsButLoses lists every blob f holds but answers 404 for one of them.
type listsButLoses struct {
	*fakeFlociAz
	lose string
}

func (l *listsButLoses) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/acct/cont/"+l.lose {
		http.NotFound(w, r)
		return
	}
	l.fakeFlociAz.ServeHTTP(w, r)
}

// TestFlociAzRecordAccountPassesTheStoreContract makes the account through
// the helper and hands what it PUT to the record store's own opening checks
// (internal/live/staterecord's CheckAzureContract), the external source
// here: every setting must pass, so a run's first contact is not refused or
// waived.
func TestFlociAzRecordAccountPassesTheStoreContract(t *testing.T) {
	f := &fakeFlociAz{puts: map[string]string{}, blobs: map[string]string{}, page: 1}
	env := append(startFakeFlociAz(t, f), "FLOCI_AZ_SUBSCRIPTION_ID=00000000-0000-0000-0000-000000000001")
	out, err := libBash(t, env, `gauntlet_floci_az_record_account refaz-records-rg refazrecords adopted green strict`)
	if err != nil {
		t.Fatalf("bash: %v\n%s", err, out)
	}
	acct := "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/refaz-records-rg/providers/Microsoft.Storage/storageAccounts/refazrecords"
	for _, p := range []string{
		"/subscriptions/00000000-0000-0000-0000-000000000001/resourcegroups/refaz-records-rg",
		acct, acct + "/blobServices/default", acct + "/managementPolicies/default",
		acct + "/blobServices/default/containers/adopted", acct + "/blobServices/default/containers/green", acct + "/blobServices/default/containers/strict",
	} {
		if _, ok := f.puts[p]; !ok {
			t.Errorf("no PUT to %s; got %v", p, keys(f.puts))
		}
	}
	if strings.Contains(f.puts[acct], "tofu-") || strings.Contains(f.puts["/subscriptions/00000000-0000-0000-0000-000000000001/resourcegroups/refaz-records-rg"], "tofu-") {
		t.Error("the records account or its group carries a marker tag, which would put it in the estate's sweep")
	}

	api := &putContract{}
	for path, into := range map[string]any{
		acct + "/blobServices/default":       &api.blob,
		acct + "/managementPolicies/default": &api.policy,
		acct:                                 &api.account,
	} {
		if err := json.Unmarshal([]byte(f.puts[path]), into); err != nil {
			t.Fatalf("%s's body is not what ARM reads: %v\n%s", path, err, f.puts[path])
		}
	}
	for _, container := range []string{"adopted", "green", "strict"} {
		findings, err := staterecord.CheckAzureContract(context.Background(), api, container, []string{"tofu-records/reference-azure/", "tofu-hints/reference-azure/"})
		if err != nil {
			t.Fatal(err)
		}
		for _, fd := range findings {
			if !fd.OK() {
				t.Errorf("container %s: the store's opening check %s does not pass on the account the helper made: %s", container, fd.Setting, fd.Found)
			}
		}
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

// putContract answers the contract's reads with the bodies the helper PUT,
// which is what floci-az stores and reads back (live/FLOCI.md, "Record store
// opening checks").
type putContract struct {
	blob    armstorage.BlobServiceProperties
	policy  armstorage.ManagementPolicy
	account armstorage.Account
}

func (p *putContract) BlobServiceProperties(context.Context) (armstorage.BlobServiceProperties, error) {
	return p.blob, nil
}
func (p *putContract) ManagementPolicy(context.Context) (armstorage.ManagementPolicy, error) {
	return p.policy, nil
}
func (p *putContract) Account(context.Context) (armstorage.Account, error) { return p.account, nil }
func (p *putContract) Container(context.Context) (armstorage.BlobContainer, error) {
	return armstorage.BlobContainer{}, nil
}
