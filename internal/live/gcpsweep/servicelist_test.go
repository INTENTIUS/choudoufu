// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package gcpsweep

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// sharedListPath is the covered set both repos test against. plunge carries
// a copy at pkg/backend/plunge/gcplist/testdata/covered-types.txt.
var sharedListPath = filepath.Join("testdata", "covered-types.txt")

// TestCoveredTypesMatchTheSharedList: the types choudoufu lists per type
// are exactly testdata/covered-types.txt, which plunge's gcplist tests its
// own set against (INTENTIUS/plunge#15's ruling: the same in both repos).
// UPDATE_COVERED_TYPES=1 rewrites the list from CoveredTypes().
func TestCoveredTypesMatchTheSharedList(t *testing.T) {
	got := CoveredTypes()
	if os.Getenv("UPDATE_COVERED_TYPES") != "" {
		var header []string
		if f, err := os.Open(sharedListPath); err == nil {
			sc := bufio.NewScanner(f)
			for sc.Scan() && strings.HasPrefix(sc.Text(), "#") {
				header = append(header, sc.Text())
			}
			f.Close()
		}
		body := strings.Join(append(header, got...), "\n") + "\n"
		if err := os.WriteFile(sharedListPath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want := readSharedList(t)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		gotSet, wantSet := map[string]bool{}, map[string]bool{}
		for _, s := range got {
			gotSet[s] = true
		}
		for _, s := range want {
			wantSet[s] = true
		}
		var extra, missing []string
		for _, s := range got {
			if !wantSet[s] {
				extra = append(extra, s)
			}
		}
		for _, s := range want {
			if !gotSet[s] {
				missing = append(missing, s)
			}
		}
		t.Errorf("covered types differ from %s (copy the regenerated file to plunge too):\n  covered, not listed: %v\n  listed, not covered: %v", sharedListPath, extra, missing)
	}
	derived := 0
	for _, s := range got {
		if tl, _ := Covered(s); tl.Derived {
			derived++
		}
	}
	if hand := len(got) - derived; hand != len(typeLists) {
		t.Errorf("%d hand-written types covered, want all %d of typeLists", hand, len(typeLists))
	}
}

func readSharedList(t *testing.T) []string {
	t.Helper()
	f, err := os.Open(sharedListPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// scopeVarsForTest fills a derived type's list variables: project p1,
// region and location us-central1, zone us-central1-a, and a parent
// object's argument by the value parents gives it, else "parent-<name>".
func scopeVarsForTest(parents map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		switch name {
		case "project":
			return "p1", true
		case "region", "location":
			return "us-central1", true
		case "zone":
			return "us-central1-a", true
		}
		if v, ok := parents[name]; ok {
			return v, true
		}
		return "parent-" + name, true
	}
}

// listBody is one page of tl's list holding an object named obj, spelled
// the way its service spells a list item: a full relative name for an
// AIP-132 service, a bare name and a selfLink for Compute Engine, a bare
// name for a bucket. A second, other-estate object is on the page too.
func listBody(tl TypeList, coll, obj, next string) string {
	item := func(name, estate string) string {
		switch tl.Service {
		case "compute.googleapis.com":
			return fmt.Sprintf(`{"name": %q, "selfLink": "https://compute.googleapis.com/compute/v1/%s/%s", "labels": {"tofu-estate": %q, "tofu-address": "a1"}}`, name, coll, name, estate)
		case "storage.googleapis.com":
			return fmt.Sprintf(`{"name": %q, "labels": {"tofu-estate": %q, "tofu-address": "a1"}}`, name, estate)
		}
		return fmt.Sprintf(`{"name": "%s/%s", "labels": {"tofu-estate": %q, "tofu-address": "a1"}}`, coll, name, estate)
	}
	tok := ""
	if next != "" {
		tok = fmt.Sprintf(`, "nextPageToken": %q`, next)
	}
	return fmt.Sprintf(`{%q: [%s, %s]%s}`, tl.Field, item(obj, "est"), item(obj+"-neighbour", "est-east"), tok)
}

// TestListDerivedTypePerService is one list per service a derived type is
// listed through (every host in Services): the request goes to the type's
// collection under the service's custom endpoint, every page is read, the
// other estate's object is dropped client side, and the object is named the
// way its Asset name spells it.
func TestListDerivedTypePerService(t *testing.T) {
	byHost := map[string]string{}
	for _, name := range CoveredTypes() {
		tl, _ := Covered(name)
		if tl.Derived && byHost[tl.Service] == "" {
			byHost[tl.Service] = name
		}
	}
	hosts := make([]string, 0, len(Services))
	for h := range Services {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	for _, host := range hosts {
		typeName := byHost[host]
		t.Run(host, func(t *testing.T) {
			if typeName == "" {
				t.Fatalf("no covered type is listed through %s; drop the service or say why it has none", host)
			}
			tl, _ := Covered(typeName)
			scope, err := tl.Scope(scopeVarsForTest(nil))
			if err != nil {
				t.Fatalf("%s: %v", typeName, err)
			}
			f, c := newListFake(t, typeName, "/v1/"+scope.Collection,
				fakeResponse{200, listBody(tl, scope.Collection, "obj", "p2")},
				fakeResponse{200, listBody(tl, scope.Collection, "two", "")})
			got, err := c.ListType(context.Background(), typeName, scope, "est")
			if err != nil {
				t.Fatalf("%s list: %v", typeName, err)
			}
			if len(got) != 2 {
				t.Fatalf("got %d objects, want 2 (the neighbour estate's dropped): %+v", len(got), got)
			}
			want := "//" + host + "/" + scope.Collection + "/obj"
			if host == "storage.googleapis.com" {
				want = "//" + host + "/obj"
			}
			if got[0].Name != want || got[0].AssetType != tl.AssetType || got[0].Project != "projects/p1" {
				t.Errorf("first object = %+v, want name %s, asset type %s", got[0], want, tl.AssetType)
			}
			if len(f.requests) != 2 {
				t.Fatalf("%d requests, want 2 pages", len(f.requests))
			}
			if r := f.requests[0]; r.Header.Get("Authorization") != "Bearer tok" || r.URL.Query().Get("filter") != "" || r.URL.Query().Get("pageSize") != "" {
				t.Errorf("first request %s: want the bearer token and no filter or pageSize (the label is checked client side)", r.URL)
			}
			if tok := f.requests[1].URL.Query().Get("pageToken"); tok != "p2" {
				t.Errorf("second request pageToken = %q, want p2", tok)
			}
		})
	}
}

// TestDerivedTypeNamesItsAsset: a listed object of a derived type has the
// Asset name its pkg/identity row reads, so it binds by the same import ID a
// search hit would, for the four shapes a list item comes in.
func TestDerivedTypeNamesItsAsset(t *testing.T) {
	for _, tc := range []struct {
		typeName, path, query, obj, asset string
		parents                           map[string]string
	}{
		{"google_pubsub_topic", "/v1/projects/p1/topics", "", "events", "//pubsub.googleapis.com/projects/p1/topics/events", nil},
		{"google_compute_disk", "/v1/projects/p1/zones/us-central1-a/disks", "", "data", "//compute.googleapis.com/projects/p1/zones/us-central1-a/disks/data", nil},
		{"google_storage_bucket", "/v1/b", "project=p1", "logs", "//storage.googleapis.com/logs", nil},
		{"google_kms_crypto_key", "/v1/projects/p1/locations/global/keyRings/kr/cryptoKeys", "", "k", "//cloudkms.googleapis.com/projects/p1/locations/global/keyRings/kr/cryptoKeys/k",
			map[string]string{"key_ring": "projects/p1/locations/global/keyRings/kr"}},
	} {
		t.Run(tc.typeName, func(t *testing.T) {
			tl, ok := Covered(tc.typeName)
			if !ok || !tl.Derived {
				t.Fatalf("%s is not a derived covered type", tc.typeName)
			}
			scope, err := tl.Scope(scopeVarsForTest(tc.parents))
			if err != nil {
				t.Fatal(err)
			}
			if scope.Query != tc.query {
				t.Errorf("query = %q, want %q", scope.Query, tc.query)
			}
			f, c := newListFake(t, tc.typeName, tc.path, fakeResponse{200, listBody(tl, scope.Collection, tc.obj, "")})
			got, err := c.ListType(context.Background(), tc.typeName, scope, "est")
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].Name != tc.asset {
				t.Fatalf("got %+v, want one object named %s", got, tc.asset)
			}
			if !tl.Names(got[0]) {
				t.Errorf("%s's own Asset template does not read the listed name %s", tc.typeName, got[0].Name)
			}
			if q := f.requests[0].URL.RawQuery; q != tc.query {
				t.Errorf("request query = %q, want %q", q, tc.query)
			}
		})
	}
}

// TestDerivedScope: a derived type's scope names what it has no value for,
// and a listed type without a Collection is filled from project and
// location; Names refuses an Asset hit of another type wearing the same
// address label.
func TestDerivedScope(t *testing.T) {
	tl, _ := Covered("google_kms_crypto_key")
	if _, err := tl.Scope(func(string) (string, bool) { return "", false }); err == nil || !strings.Contains(err.Error(), "key_ring") {
		t.Errorf("err = %v, want one naming key_ring", err)
	}
	topic, _ := Covered("google_pubsub_topic")
	if topic.Names(Asset{AssetType: "pubsub.googleapis.com/Topic", Name: "//pubsub.googleapis.com/projects/p1/subscriptions/s"}) {
		t.Error("a subscription's name read as a topic's")
	}
	disk, _ := Covered("google_compute_disk")
	if disk.Names(Asset{AssetType: "compute.googleapis.com/Instance", Name: "//compute.googleapis.com/projects/p1/zones/z/instances/vm"}) {
		t.Error("an instance's name read as a disk's")
	}
	if got := Uncovered("google_pubsub_topic"); got != "" {
		t.Errorf("Uncovered(topic) = %q, want \"\"", got)
	}
	if got := Uncovered("google_dialogflow_cx_intent"); got != NotCovered["google_dialogflow_cx_intent"] {
		t.Errorf("Uncovered(intent) = %q", got)
	}
}
