// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
)

// ---------------------------------------------------------------------------
// GitHub issue #243. markers.Taggable tested the shape of a "tags" attribute
// and not its meaning, so it accepted GCP resource-manager tag bindings as a
// free-form marker surface and this pass wrote an ownership marker into one.
// The write is rejected by the API - "tofu-estate" is not a tagKeys/{id} -
// and on the types the provider documents as immutable a later marker
// rewrite forces the resource to be replaced. 17 google types at
// hashicorp/google 7.44.0 passed the old predicate; 11 of them are also
// admitted by the schema fallback, so they reached this pass.
//
// These assert on the rendered stamp rather than on the predicate's boolean,
// because a predicate has been green here while the markers were wrong twice
// before. What the plan sends to the cloud is the evaluated tags argument,
// and that is what evalTags returns.
//
// The description strings below are transcribed verbatim from
// hashicorp/google 7.44.0's GetProviderSchema response, read through
// internal/live/pluginschema on 2026-08-16. They are the external source
// this test consults: mutate the rule to agree with itself and these
// strings, which are the provider's and not this fork's, stop agreeing.
// ---------------------------------------------------------------------------

// googleProjectTagsDescription is google_project.tags's description, verbatim.
// The final sentence is the one that makes the old behavior destructive
// rather than merely rejected.
const googleProjectTagsDescription = "A map of resource manager tags. Resource manager tag keys and values have the same definition as resource manager tags. Keys must be in the format tagKeys/{tag_key_id}, and values are in the format tagValues/456. The field is ignored when empty. This field is only set at create time and modifying this field after creation will trigger recreation. To apply tags to an existing resource, see the google_tags_tag_value resource."

// googleWorkstationsTagsDescription is
// google_workstations_workstation_cluster.tags's description, verbatim. It
// is the one of the 17 that states no rule at all and only shows an example
// key, which is why the rule reads exhibited formats as well as stated ones.
const googleWorkstationsTagsDescription = "Resource manager tags bound to this resource.\nFor example:\n\"123/environment\": \"production\",\n\"123/costCenter\": \"marketing\""

// tfeWorkspaceTagsDescription is tfe_workspace.tags's description, verbatim
// from hashicorp/tfe 0.80.0. It is the control in the other direction: a
// non-AWS provider that documents its tags map and means a free-form one.
// The rule must leave it alone, or it has become "documented, therefore
// refused", which is the inverse of a good signal.
const tfeWorkspaceTagsDescription = "A map of key value tags for this workspace."

// describedTagsSchema is [taggedSchema] with the provider's own words on the
// tags attribute, which is the only channel configschema.Attribute has for
// this. There is no ForceNew or requires-replacement flag on an attribute at
// either plugin protocol version.
func describedTagsSchema(desc string, names ...string) *configschema.Block {
	block := taggedSchema(names...)
	block.Attributes["tags"] = &configschema.Attribute{
		Type:        cty.Map(cty.String),
		Optional:    true,
		Description: desc,
	}
	return block
}

func keyspaceSchemas() Schemas {
	s := testSchemaSource{
		"google_project": describedTagsSchema(googleProjectTagsDescription, "id", "project_id", "name"),
		"google_workstations_workstation_cluster": describedTagsSchema(googleWorkstationsTagsDescription, "id", "name", "location"),
		"tfe_workspace": describedTagsSchema(tfeWorkspaceTagsDescription, "id", "name"),
		// The AWS controls. Every one of the 847 settable tags maps at
		// hashicorp/aws 6.59.0 carries an empty description, so the first
		// is what the provider that works actually looks like; the second
		// is one of the many AWS types with no tag surface at all, which
		// must stay silent.
		"aws_s3_bucket":        describedTagsSchema("", "id", "bucket"),
		"aws_s3_bucket_policy": untaggedSchema("id", "bucket", "policy"),
	}
	return s
}

// TestStamp_resourceManagerTagBindingIsNotStamped is issue #243's own
// reproduction. The marker must not appear in the rendered tags, and the
// author's own binding must survive untouched.
func TestStamp_resourceManagerTagBindingIsNotStamped(t *testing.T) {
	cfg := loadSource(t, `
resource "google_project" "p" {
  project_id = "demo-123"
  name       = "demo"

  tags = {
    "tagKeys/281476476068062" = "tagValues/281479908658637"
  }
}

resource "aws_s3_bucket" "b" {
  bucket = "demo"
}
`)

	res, diags := Stamp(t.Context(), Request{
		Estate:  "demo",
		Config:  cfg,
		Schemas: keyspaceSchemas(),
	})
	assertNoErrors(t, diags)

	// The rendered stamp, which is what the plan sends to GCP.
	assertTags(t, evalTags(t, cfg, "google_project.p", nil), map[string]string{
		"tagKeys/281476476068062": "tagValues/281479908658637",
	})

	// The AWS resource in the same configuration is stamped exactly as
	// before. A regression here is worse than the bug: AWS is the path that
	// works.
	assertTags(t, evalTags(t, cfg, "aws_s3_bucket.b", nil), map[string]string{
		"tofu-estate":  "demo",
		"tofu-address": "aws_s3_bucket.b",
	})

	if len(res.Stamped) != 1 || res.Stamped[0].Addr.String() != "aws_s3_bucket.b" {
		t.Fatalf("expected only the bucket to be stamped, got %+v", res.Stamped)
	}
	assertSkippedUntaggable(t, res, "google_project.p")
}

// TestStamp_exhibitedKeyFormatIsNotStamped covers the type whose description
// states no rule and only shows an example key. It is the one of the 17 a
// stated-requirement rule alone would miss.
func TestStamp_exhibitedKeyFormatIsNotStamped(t *testing.T) {
	cfg := loadSource(t, `
resource "google_workstations_workstation_cluster" "c" {
  name     = "cluster"
  location = "us-central1"
}
`)

	res, diags := Stamp(t.Context(), Request{
		Estate:  "demo",
		Config:  cfg,
		Schemas: keyspaceSchemas(),
	})
	assertNoErrors(t, diags)

	if got := evalTags(t, cfg, "google_workstations_workstation_cluster.c", nil); len(got) != 0 {
		t.Errorf("a tags argument was written into a block that had none: %v", got)
	}
	assertSkippedUntaggable(t, res, "google_workstations_workstation_cluster.c")
}

// TestStamp_freeFormTagsOnANonAWSProviderAreStillStamped is the guard
// against the fix becoming "any provider that documents its tags map loses
// them". tfe's tags map is free-form and its description says so.
func TestStamp_freeFormTagsOnANonAWSProviderAreStillStamped(t *testing.T) {
	cfg := loadSource(t, `
resource "tfe_workspace" "w" {
  name = "app"
}
`)

	res, diags := Stamp(t.Context(), Request{
		Estate:  "demo",
		Config:  cfg,
		Schemas: keyspaceSchemas(),
	})
	assertNoErrors(t, diags)

	assertTags(t, evalTags(t, cfg, "tfe_workspace.w", nil), map[string]string{
		"tofu-estate":  "demo",
		"tofu-address": "tfe_workspace.w",
	})
	if len(res.Stamped) != 1 {
		t.Fatalf("the workspace was not stamped: %+v (skipped %v)", res.Stamped, res.Skipped)
	}
}

// TestStamp_constrainedKeySpaceRefusalSaysWhy: a type that can only be found
// by its marker, and cannot carry one, is a hard refusal already. What
// changed is the sentence. Telling the reader that a resource whose schema
// plainly has a tags map "has no tags map this configuration can set" sends
// them looking for a typo.
func TestStamp_constrainedKeySpaceRefusalSaysWhy(t *testing.T) {
	cfg := loadSource(t, `
resource "google_project" "p" {
  project_id = "demo-123"
  name       = "demo"
}
`)

	_, diags := Stamp(t.Context(), Request{
		Estate:         "demo",
		Config:         cfg,
		Schemas:        keyspaceSchemas(),
		NeedsDiscovery: needsDiscovery("google_project.p"),
	})
	if !diags.HasErrors() {
		t.Fatalf("a marker-discovered resource with no marker surface was not refused: %s", diags.ErrWithWarnings())
	}
	msg := diags.Err().Error()
	for _, want := range []string{
		"cannot round-trip",
		"namespaced",
		"tagKeys/{tag_key_id}",
		"tofu-estate",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not say why; missing %q in:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "has no tags map this configuration can set") {
		t.Errorf("the refusal asserts a fact the schema contradicts:\n%s", msg)
	}
}

func assertSkippedUntaggable(t *testing.T, res *Result, addr string) {
	t.Helper()
	for _, s := range res.Skipped {
		if s.Addr.String() == addr {
			if s.Reason != SkipUntaggable {
				t.Errorf("%s was skipped as %s, want %s", addr, s.Reason, SkipUntaggable)
			}
			return
		}
	}
	t.Errorf("%s does not appear in the skip list at all: %+v", addr, res.Skipped)
}

// TestStamp_unusableTagSurfaceWarnsAndNoTagSurfaceStaysSilent is the
// maintainer ruling on #223 landing in code: the non-AWS marker path is
// refused explicitly rather than left as a silent gap. A resource the author
// can see a tags argument on, which this fork will not use, gets told so.
//
// The split between warning and silence is the whole care in it. Hundreds of
// AWS types have no tag surface at all and are identified by an argument
// instead; warning on each would drown the run and would be wrong, because
// nothing about them is unexpected. What is unexpected is a tags map that
// exists and cannot be used.
func TestStamp_unusableTagSurfaceWarnsAndNoTagSurfaceStaysSilent(t *testing.T) {
	cfg := loadSource(t, `
resource "google_project" "p" {
  project_id = "demo-123"
  name       = "demo"
}

resource "aws_s3_bucket_policy" "b" {
  bucket = "demo"
  policy = "{}"
}
`)

	_, diags := Stamp(t.Context(), Request{
		Estate:  "demo",
		Config:  cfg,
		Schemas: keyspaceSchemas(),
	})
	assertNoErrors(t, diags)

	if len(diags) != 1 {
		t.Fatalf("want exactly one warning, got %d: %s", len(diags), diags.ErrWithWarnings())
	}
	msg := diags.ErrWithWarnings().Error()
	if strings.Contains(msg, "aws_s3_bucket_policy") {
		t.Errorf("an AWS type with no tag surface was reported; it must stay silent:\n%s", msg)
	}
	for _, want := range []string{
		"google_project.p",
		"namespaced",
		"tagKeys/{tag_key_id}",
		"cannot carry an ownership marker",
		`estate "demo"`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the warning does not say %q:\n%s", want, msg)
		}
	}
}
