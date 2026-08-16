// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// A component that carries both a cloud property and the argument the
// provider documents as defaulting to it (#241) has to pick between the two
// per instance, and every assertion below is on the RENDERED identity - the
// import ID and the identity object - rather than on a resolution class or
// any other predicate. A wrong catalog_id names a real Data Catalog in
// someone else's account, so "it classified concrete" is not the property
// worth checking; "it built this exact string" is.

// TestCloudDefaultAttrPrefersTheStatedValue is the headline: a configuration
// that states catalog_id resolves without any cloud context at all, and
// resolves to the catalog it named rather than to whatever account the run
// turns out to be against.
func TestCloudDefaultAttrPrefersTheStatedValue(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "cloud-default-attr"), nil)
	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	for _, tc := range []struct {
		addr     string
		importID string
		identity map[string]string
	}{
		{`aws_glue_catalog_database.peer`, "999988887777:shared", map[string]string{"id": "999988887777:shared"}},
		{`aws_glue_catalog_table.peer`, "999988887777:shared:events", map[string]string{"id": "999988887777:shared:events"}},
	} {
		res, ok := result.Get(mustAddr(t, tc.addr))
		if !ok {
			t.Errorf("%s missing from the result", tc.addr)
			continue
		}
		if res.Class != ClassConcrete {
			t.Errorf("%s = %s (%s), want CONCRETE", tc.addr, res.Class, res.Reason)
			continue
		}
		if res.ImportID != tc.importID {
			t.Errorf("%s import ID = %q, want %q", tc.addr, res.ImportID, tc.importID)
		}
		for attr, want := range tc.identity {
			if got := res.IdentityValues[attr]; got != want {
				t.Errorf("%s identity %s = %q, want %q", tc.addr, attr, got, want)
			}
		}
	}
}

// TestCloudDefaultAttrFallsBackToTheCloudValue is the other half: an omitted
// argument still means the account, and the account is still something only
// the run can say. Both spellings of absence - never written, and written as
// a conditional null - defer, and neither raises an error.
func TestCloudDefaultAttrFallsBackToTheCloudValue(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "cloud-default-attr"), nil)

	offline, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)
	for _, addr := range []string{`aws_glue_catalog_database.own`, `aws_glue_catalog_database.maybe`} {
		res, ok := offline.Get(mustAddr(t, addr))
		if !ok {
			t.Fatalf("%s missing from the result", addr)
		}
		if res.Class != ClassNeedsDiscovery {
			t.Errorf("%s = %s with no cloud context, want NEEDS_DISCOVERY", addr, res.Class)
		}
		if res.Cause != DiscoveryCloudUnknown {
			t.Errorf("%s cause = %q, want %q", addr, res.Cause, DiscoveryCloudUnknown)
		}
		if res.ImportID != "" {
			t.Errorf("%s carries an import ID with no account known: %q", addr, res.ImportID)
		}
		if !strings.Contains(res.Reason, "AWS account ID") {
			t.Errorf("%s reason does not name the missing value: %q", addr, res.Reason)
		}
	}

	told, diags := ResolveIn(context.Background(), cfg, CloudContext{AccountID: "000000000000", Region: "us-east-1"})
	assertNoErrors(t, diags)
	want := map[string]string{
		`aws_glue_catalog_database.own`:   "000000000000:local",
		`aws_glue_catalog_database.maybe`: "000000000000:maybe",
		// The stated ones do NOT move: being told the account does not
		// override what the configuration said, which is the injectivity
		// this whole change turns on.
		`aws_glue_catalog_database.peer`: "999988887777:shared",
		`aws_glue_catalog_table.peer`:    "999988887777:shared:events",
	}
	for addr, wantID := range want {
		res, ok := told.Get(mustAddr(t, addr))
		if !ok {
			t.Fatalf("%s missing from the result", addr)
		}
		if res.Class != ClassConcrete {
			t.Errorf("%s = %s with a cloud context, want CONCRETE (%s)", addr, res.Class, res.Reason)
			continue
		}
		if res.ImportID != wantID {
			t.Errorf("%s import ID = %q, want %q", addr, res.ImportID, wantID)
		}
		if got := res.IdentityValues["id"]; got != wantID {
			t.Errorf("%s identity id = %q, want %q", addr, got, wantID)
		}
	}
}

// TestCloudDefaultAttrStillCollides is the safety property the change is most
// able to break: turning a refusal into a resolution must not turn a
// duplicate-identity error into silence. Two blocks naming one catalog and
// one database name are one live object claimed twice, and the value the
// collision check compares is now the one the argument supplied.
func TestCloudDefaultAttrStillCollides(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "cloud-default-attr-collision"), nil)
	_, diags := Resolve(context.Background(), cfg)

	if !diags.HasErrors() {
		t.Fatal("two Glue databases in one catalog with one name were accepted")
	}
	if !hasDiag(diags, "Two resources with the same identity",
		`aws_glue_catalog_database.clash_a and aws_glue_catalog_database.clash_b both resolve to the identity "111122223333:clash"`) {
		t.Errorf("wrong diagnostic:\n%s", renderDiags(diags))
	}
}

// TestCloudDefaultAttrIsSingleSourced pins the modelling rule the resolver
// relies on, over the whole table rather than over the rows that have it
// today: a cloud-bearing component offers at most one argument, so
// cloudComponentAttr chooses between exactly two sources and never between a
// preference list and a cloud value. It also states what the emitted rows
// actually are, so a merge that started filling in the wrong argument shows
// up here rather than as a wrong marker.
func TestCloudDefaultAttrIsSingleSourced(t *testing.T) {
	got := map[string]string{}
	for _, typeName := range AdmittedTypes() {
		entry, _ := LookupType(typeName)
		for _, comp := range entry.Components {
			if comp.Cloud == CloudNone || len(comp.Attrs) == 0 {
				continue
			}
			if len(comp.Attrs) != 1 {
				t.Errorf("%s: a cloud-bearing component offers %d arguments, want 1: %v", typeName, len(comp.Attrs), comp.Attrs)
				continue
			}
			if comp.Cloud == CloudAccountID {
				got[typeName] = comp.Attrs[0]
			}
		}
	}
	// Only the account side is pinned by name. The region side is the AWS
	// provider's per-resource override, present on 1488 of 1699 v6.59.0
	// resource docs, so listing it here would be a transcription of the
	// provider's release rather than a claim about this table.
	want := map[string]string{
		"aws_glue_catalog_database":                 "catalog_id",
		"aws_glue_catalog_table":                    "catalog_id",
		"aws_glue_connection":                       "catalog_id",
		"aws_glue_data_catalog_encryption_settings": "catalog_id",
	}
	for typeName, wantAttr := range want {
		if got[typeName] != wantAttr {
			t.Errorf("%s account component reads %q, want %q", typeName, got[typeName], wantAttr)
		}
		delete(got, typeName)
	}
	for typeName, attr := range got {
		t.Errorf("%s has an account component reading %q, which no ratified evidence in this test covers; if the provider documents it, add it here", typeName, attr)
	}
}
