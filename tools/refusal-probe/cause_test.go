// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/stamp"
)

// realAddr is a subject the catalog was NOT built from. Every test here
// renders through [stamp.UnmarkedDiscoveryDetail] with values the catalog
// never saw, which is the point: a fingerprint set that only reproduces its
// own sentinels is a ratchet measuring itself.
var realAddr = addrs.ConfigResource{
	Module: addrs.RootModule,
	Resource: addrs.Resource{
		Mode: addrs.ManagedResourceMode,
		Type: "aws_instance",
		Name: "web",
	},
}

func TestCauseCatalogClassifiesSentencesItNeverSaw(t *testing.T) {
	c, err := newCauseCatalog()
	if err != nil {
		t.Fatalf("building the catalog: %v", err)
	}

	cases := []struct {
		name string
		disc identity.BlockDiscovery
		want string
	}{
		{
			name: "cloud property, one subject",
			disc: identity.BlockDiscovery{Cause: identity.DiscoveryCloudUnknown, Args: []string{string(identity.CloudAccountID)}},
			want: string(identity.DiscoveryCloudUnknown),
		},
		{
			name: "cloud property with a named fix",
			disc: identity.BlockDiscovery{Cause: identity.DiscoveryCloudUnknown, Args: []string{string(identity.CloudRegion), "catalog_id"}},
			want: string(identity.DiscoveryCloudUnknown),
		},
		{
			name: "name omitted",
			disc: identity.BlockDiscovery{Cause: identity.DiscoveryNameOmitted, Args: []string{"bucket"}},
			want: string(identity.DiscoveryNameOmitted),
		},
		{
			name: "name prefix",
			disc: identity.BlockDiscovery{Cause: identity.DiscoveryNamePrefix, Args: []string{"name", "name_prefix"}},
			want: string(identity.DiscoveryNamePrefix),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			detail := stamp.UnmarkedDiscoveryDetail(realAddr, tc.disc)
			if got := c.discovery(detail); got != tc.want {
				t.Errorf("classified as %q, want %q\ndetail: %s", got, tc.want, detail)
			}
		})
	}
}

// TestCauseCatalogRefusesToInventASubjectlessCause is the honesty half.
//
// [stamp.UnmarkedDiscoveryDetail] renders one sentence for a server-assigned
// identity AND for every cause whose subjects did not arrive, so that
// sentence is evidence for a set and not for a member. The catalog must
// report the set. Attributing it to SERVER_ASSIGNED alone would read as a
// precise finding built on a sentence that cannot support it.
func TestCauseCatalogRefusesToInventASubjectlessCause(t *testing.T) {
	c, err := newCauseCatalog()
	if err != nil {
		t.Fatalf("building the catalog: %v", err)
	}

	detail := stamp.UnmarkedDiscoveryDetail(realAddr, identity.BlockDiscovery{Cause: identity.DiscoveryServerAssigned})
	label := c.discovery(detail)
	if !strings.Contains(label, string(identity.DiscoveryServerAssigned)) {
		t.Fatalf("the server-assigned sentence classified as %q, which does not name that cause", label)
	}

	// Every cause that renders the same sentence with no subjects must be in
	// the same label, because nothing in the sentence tells them apart.
	for _, cause := range identity.AllDiscoveryCauses() {
		bare := stamp.UnmarkedDiscoveryDetail(realAddr, identity.BlockDiscovery{Cause: cause})
		if bare != detail {
			continue
		}
		if !strings.Contains(label, string(cause)) {
			t.Errorf("cause %s renders the identical sentence but is not named in the label %q", cause, label)
		}
	}
}

// TestCauseCatalogCoversEveryCause fails when a new cause is added to
// identity and this program silently starts reporting it as unclassified.
func TestCauseCatalogCoversEveryCause(t *testing.T) {
	c, err := newCauseCatalog()
	if err != nil {
		t.Fatalf("building the catalog: %v", err)
	}
	var all strings.Builder
	for _, p := range c.prints {
		all.WriteString(p.label)
		all.WriteByte(' ')
	}
	for _, cause := range identity.AllDiscoveryCauses() {
		if !strings.Contains(all.String(), string(cause)) {
			t.Errorf("cause %s appears in no label; the breakdown would report its sites as %s", cause, causeUnclassified)
		}
	}
}

func TestCauseCatalogSaysUnclassifiedRatherThanGuessing(t *testing.T) {
	c, err := newCauseCatalog()
	if err != nil {
		t.Fatalf("building the catalog: %v", err)
	}
	if got := c.discovery("Ownership markers not stamped for some other reason entirely."); got != causeUnclassified {
		t.Errorf("a detail from no rendered sentence classified as %q, want %q", got, causeUnclassified)
	}
}

// TestDiffRefusesToCompareAcrossSchemaModes is the guard on the one
// comparison that would produce a large, meaningless, flattering number.
func TestDiffRefusesToCompareAcrossSchemaModes(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, r run) string {
		path := filepath.Join(dir, name)
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	base := run{Manifest: "live/corpus-manifest.json", Root: "."}
	without := write("without.json", base)
	with := base
	with.Schemas = true
	withPath := write("with.json", with)

	if err := runDiff(without + "," + withPath); err == nil {
		t.Fatal("comparing a schema-less run against a schema-backed one was allowed")
	}
	if err := runDiff(without + "," + without); err != nil {
		t.Fatalf("comparing two schema-less runs failed: %v", err)
	}
	if err := runDiff(withPath + "," + withPath); err != nil {
		t.Fatalf("comparing two schema-backed runs failed: %v", err)
	}
}
