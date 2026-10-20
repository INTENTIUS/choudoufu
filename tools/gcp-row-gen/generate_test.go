// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

func loadForTest(t *testing.T) (string, Evidence, Facts) {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	ev, facts, err := loadInputs(root)
	if err != nil {
		t.Fatal(err)
	}
	return root, ev, facts
}

func loadNamesForTest(t *testing.T) AssetNames {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	names, err := loadAssetNames(root)
	if err != nil {
		t.Fatal(err)
	}
	return names
}

// TestGeneratedFilesAreCurrent regenerates the table and the survey from
// the committed evidence and requires both to match what is committed.
// Regenerate with `go run ./tools/gcp-row-gen`.
func TestGeneratedFilesAreCurrent(t *testing.T) {
	root, ev, facts := loadForTest(t)
	out, err := Generate(ev, facts, loadNamesForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	for rel, want := range out.Files() {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s has drifted from a fresh `go run ./tools/gcp-row-gen`; regenerate and commit it", rel)
		}
	}
	if got, want := len(identity.GoogleTable), out.Survey.Counts.Rowed; got != want {
		t.Errorf("identity.GoogleTable has %d rows, the survey %d rowed types", got, want)
	}
}

// TestEveryRowNamesRealSchemaAttributes checks the compiled
// identity.GoogleTable against live/gcp-schema-facts.json, which no part of
// the table is rendered from: every argument a component reads, every
// identity attribute it claims to supply and every attribute the row hands
// out as an identity source has to exist in the schema of every provider
// that serves the type. A row naming an attribute the schema lacks would
// fail at resolution with a message about the configuration rather than
// about the row.
func TestEveryRowNamesRealSchemaAttributes(t *testing.T) {
	_, _, facts := loadForTest(t)
	checked := 0
	for typeName, row := range identity.GoogleTable {
		if row.Type != typeName {
			t.Errorf("%s: row says its type is %q", typeName, row.Type)
		}
		if !row.NonAWSProvider {
			t.Errorf("%s: a hashicorp/google row must carry NonAWSProvider", typeName)
		}
		served := 0
		for p, pf := range facts.Providers {
			tf, ok := pf.Types[typeName]
			if !ok {
				continue
			}
			served++
			for i, c := range row.Components {
				for _, a := range c.Attrs {
					if c.Block != "" {
						b, ok := tf.Blocks[c.Block]
						if !ok || b.MaxItems != 1 {
							t.Errorf("%s (%s): component %d reads block %q, which is not a single nested block of the schema", typeName, p, i, c.Block)
							continue
						}
						if _, ok := b.Attrs[a]; !ok {
							t.Errorf("%s (%s): component %d reads %s.%s, which the schema lacks", typeName, p, i, c.Block, a)
						}
						continue
					}
					if _, ok := tf.Attrs[a]; !ok {
						t.Errorf("%s (%s): component %d reads %q, which the schema lacks", typeName, p, i, a)
					}
				}
				if c.IdentityAttr != "" && c.IdentityAttr != identity.SameNameIdentity && tf.Identity != nil {
					if _, ok := tf.Identity[c.IdentityAttr]; !ok {
						t.Errorf("%s (%s): component %d supplies identity attribute %q, which the identity schema lacks", typeName, p, i, c.IdentityAttr)
					}
				}
			}
			for _, a := range row.IdentityAttrs {
				if _, ok := tf.Attrs[a]; !ok {
					t.Errorf("%s (%s): IdentityAttrs hands out %q, which the schema lacks", typeName, p, a)
				}
			}
			checked++
		}
		if served == 0 {
			t.Errorf("%s has a row and no provider's schema serves it", typeName)
		}
	}
	if checked == 0 {
		t.Fatal("no row was checked against any schema")
	}
}

// TestMarkerlessIsAVetoNotARow keeps the two generated sets disjoint, the
// way identity.MarkerlessTypes and DefaultTable are.
func TestMarkerlessIsAVetoNotARow(t *testing.T) {
	for typeName := range identity.GoogleMarkerlessTypes {
		if _, ok := identity.GoogleTable[typeName]; ok {
			t.Errorf("%s is both rowed and vetoed as markerless", typeName)
		}
	}
	if len(identity.GoogleMarkerlessTypes) == 0 {
		t.Error("no type is vetoed; the markerless rule never fired")
	}
}

func network() map[string]TypeFacts {
	tf := TypeFacts{
		Attrs:    map[string]string{"id": "OC", "name": "R", "project": "OC", "self_link": "C"},
		Identity: map[string]string{"name": "R", "project": "O"},
	}
	return map[string]TypeFacts{ProviderGoogle: tf, ProviderGoogleBeta: tf}
}

func networkSource() Source {
	return Source{
		Kind:           "resource",
		IDFormat:       "projects/{{project}}/global/networks/{{name}}",
		ImportIDFormat: "projects/{{project}}/global/networks/{{name}}",
	}
}

func TestDecideGlobalResource(t *testing.T) {
	d := Decide("google_compute_network", map[string]Source{ProviderGoogle: networkSource(), ProviderGoogleBeta: networkSource()}, network())
	if d.Verdict != VerdictRowed {
		t.Fatalf("verdict %s (%s), want rowed", d.Verdict, d.Reason)
	}
	want := []identity.Component{
		{Literal: "projects/"},
		{Attrs: []string{"project"}, Cloud: identity.CloudProject, IdentityAttr: identity.SameNameIdentity},
		{Literal: "/global/networks/"},
		{Attrs: []string{"name"}, IdentityAttr: identity.SameNameIdentity},
	}
	if !reflect.DeepEqual(d.Row.Components, want) {
		t.Errorf("components\n got %#v\nwant %#v", d.Row.Components, want)
	}
	if len(d.Parents) != 0 || d.Scoped {
		t.Errorf("parents %v, scoped %v; want neither: the network's name is its own", d.Parents, d.Scoped)
	}
}

func TestDecideDisagreeingProvidersHaveNoRow(t *testing.T) {
	beta := networkSource()
	beta.IDFormat = "projects/{{project}}/networks/{{name}}"
	beta.ImportIDFormat = beta.IDFormat
	d := Decide("google_compute_network", map[string]Source{ProviderGoogle: networkSource(), ProviderGoogleBeta: beta}, network())
	if d.Verdict != VerdictUnrowed {
		t.Errorf("verdict %s, want unrowed: one row cannot serve two identities", d.Verdict)
	}
}

func TestDecideServerAssigned(t *testing.T) {
	tf := TypeFacts{Attrs: map[string]string{"id": "OC", "name": "C", "project": "OC", "display_name": "R"}}
	src := Source{Kind: "resource", IDFormat: "projects/{{project}}/things/{{name}}", ImportIDFormat: "{{name}}"}
	d := Decide("google_thing", map[string]Source{ProviderGoogle: src}, map[string]TypeFacts{ProviderGoogle: tf})
	if d.Verdict != VerdictMarkerless || d.Row != nil {
		t.Errorf("unlabelled minted type: verdict %s, row %v; want markerless and no row", d.Verdict, d.Row)
	}

	tf.Attrs["labels"], tf.Attrs["effective_labels"] = "O", "C"
	d = Decide("google_thing", map[string]Source{ProviderGoogle: src}, map[string]TypeFacts{ProviderGoogle: tf})
	if d.Verdict != VerdictServerAssigned || d.Row == nil || !d.Row.ServerAssigned {
		t.Errorf("labelled minted type: verdict %s; want a server-assigned row", d.Verdict)
	}
}

func TestDecideDerivedPlaceholder(t *testing.T) {
	tf := TypeFacts{
		Attrs:    map[string]string{"id": "OC", "account_id": "R", "email": "C", "project": "OC"},
		Identity: map[string]string{"email": "R", "project": "O"},
	}
	src := Source{
		Kind:           "resource",
		IDFormat:       "projects/{{project}}/serviceAccounts/{{email}}",
		ImportIDFormat: "projects/{{project}}/serviceAccounts/{{email}}",
		Derived:        map[string]string{"email": "{{account_id}}@{{project}}.iam.gserviceaccount.com"},
	}
	d := Decide("google_service_account", map[string]Source{ProviderGoogle: src}, map[string]TypeFacts{ProviderGoogle: tf})
	if d.Verdict != VerdictRowed {
		t.Fatalf("verdict %s (%s), want rowed", d.Verdict, d.Reason)
	}
	want := []identity.Component{
		{Literal: "projects/"},
		{Attrs: []string{"project"}, Cloud: identity.CloudProject, IdentityAttr: identity.SameNameIdentity},
		{Literal: "/serviceAccounts/"},
		{Attrs: []string{"account_id"}, IdentityAttr: "email"},
		{Literal: "@", IdentityAttr: "email"},
		{Attrs: []string{"project"}, Cloud: identity.CloudProject, IdentityAttr: "email"},
		{Literal: ".iam.gserviceaccount.com", IdentityAttr: "email"},
	}
	if !reflect.DeepEqual(d.Row.Components, want) {
		t.Errorf("components\n got %#v\nwant %#v", d.Row.Components, want)
	}
}

func TestDecideOptionalPlaceholderNeedsADefault(t *testing.T) {
	tf := TypeFacts{Attrs: map[string]string{"id": "OC", "name": "R", "project": "OC", "location": "O"}}
	src := Source{Kind: "resource", IDFormat: "projects/{{project}}/locations/{{location}}/certs/{{name}}"}
	src.ImportIDFormat = src.IDFormat
	d := Decide("google_cert", map[string]Source{ProviderGoogle: src}, map[string]TypeFacts{ProviderGoogle: tf})
	if d.Verdict != VerdictUnrowed {
		t.Errorf("verdict %s, want unrowed without a default", d.Verdict)
	}
	src.Defaults = map[string]string{"location": "global"}
	d = Decide("google_cert", map[string]Source{ProviderGoogle: src}, map[string]TypeFacts{ProviderGoogle: tf})
	if d.Verdict != VerdictRowed || d.Row.Components[3].Default != "global" {
		t.Errorf("verdict %s; want a row whose location component defaults to global", d.Verdict)
	}
}

func TestDecideIAMMember(t *testing.T) {
	tf := TypeFacts{
		Attrs:    map[string]string{"id": "C", "project": "OC", "zone": "OC", "instance_name": "R", "role": "R", "member": "R", "etag": "C"},
		Blocks:   map[string]BlockFacts{"condition": {Attrs: map[string]string{"title": "R", "expression": "R"}, MaxItems: 1}},
		Identity: map[string]string{"project": "O", "zone": "O", "instance_name": "R", "role": "R", "member": "R", "condition_title": "O"},
	}
	src := Source{
		Kind:        "iam_member",
		IAMParent:   "projects/{{project}}/zones/{{zone}}/instances/{{instance_name}}",
		IAMGrammar:  "resource_name role member [condition_title]",
		IAMDefaults: []string{"project", "zone"},
	}
	d := Decide("google_compute_instance_iam_member", map[string]Source{ProviderGoogle: src}, map[string]TypeFacts{ProviderGoogle: tf})
	if d.Verdict != VerdictRowed {
		t.Fatalf("verdict %s (%s), want rowed", d.Verdict, d.Reason)
	}
	last := d.Row.Components[len(d.Row.Components)-1]
	if last.Block != "condition" || !last.OmitIfAbsent || last.Literal != " " || last.IdentityAttr != "condition_title" {
		t.Errorf("condition component %#v", last)
	}
	if want := []string{"google_compute_instance"}; !reflect.DeepEqual(d.Parents, want) {
		t.Errorf("parents %v, want %v", d.Parents, want)
	}
}
