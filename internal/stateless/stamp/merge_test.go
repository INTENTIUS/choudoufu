// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// Audit finding C2's regression, in one sentence: a resource whose identity
// the provider assigns must never be applied without its ownership marker,
// because the marker is the only handle any later run will have on it.
//
// The bug was a tags argument this pass could not read entry by entry. A
// merge() call, a variable, a conditional - all of them produced a skip, a
// warning, and a run that carried on. Applying that plan created a subnet with
// no marker; the next plan could not find it, proposed creating another, and
// so did every plan after that. A duplicate-creation loop with a warning in
// front of it.
//
// Two changes, both asserted here: what cannot be read is merged into rather
// than skipped, and a skip on a resource only its marker could find is an
// error rather than a warning.

// needsDiscovery is the request field the caller fills from identity
// resolution: these blocks' instances have server-assigned identities.
func needsDiscovery(addrs ...string) map[string]bool {
	out := make(map[string]bool, len(addrs))
	for _, a := range addrs {
		out[a] = true
	}
	return out
}

// TestStamp_mergeTagsAreStamped is the finding's own example: a subnet whose
// tags come out of a merge().
func TestStamp_mergeTagsAreStamped(t *testing.T) {
	cfg := loadSource(t, `
locals {
  common = {
    team = "platform"
  }
}

resource "aws_subnet" "app" {
  cidr_block = "10.42.1.0/24"

  tags = merge(local.common, { Name = "app" })
}
`)

	res, diags := Stamp(t.Context(), Request{
		Estate:         "stamp-unit",
		Config:         cfg,
		Schemas:        testSchemas(),
		NeedsDiscovery: needsDiscovery("aws_subnet.app"),
	})
	assertNoErrors(t, diags)
	if len(diags) != 0 {
		t.Errorf("stamping a merge() tags argument produced diagnostics: %s", diags.ErrWithWarnings())
	}
	if len(res.Stamped) != 1 || !res.Stamped[0].Merged {
		t.Fatalf("the subnet was not stamped by merging: %+v (skipped: %v)", res.Stamped, res.Skipped)
	}

	// The claim that matters is what the tags evaluate to, because that is
	// what reaches the cloud on the create call.
	assertTags(t, evalTags(t, cfg, "aws_subnet.app", localsMap(map[string]cty.Value{
		"common": cty.MapVal(map[string]cty.Value{"team": cty.StringVal("platform")}),
	})), map[string]string{
		"team":         "platform",
		"Name":         "app",
		"tofu-estate":  "stamp-unit",
		"tofu-address": "aws_subnet.app",
	})
}

// TestStamp_mergeTagsWinOverTheAuthorsDynamicHalf: the injected object is the
// last argument, and merge's last argument wins. That is the property the fix
// rests on - the markers on the applied resource are the ones this run
// stamped, whatever the half it could not read produces.
func TestStamp_mergeTagsWinOverTheAuthorsDynamicHalf(t *testing.T) {
	cfg := loadSource(t, `
variable "tags" {
  type = map(string)
  default = {
    tofu-address = "aws_subnet.somewhere_else"
  }
}

resource "aws_subnet" "app" {
  cidr_block = "10.42.1.0/24"

  tags = merge(var.tags)
}
`)

	_, diags := Stamp(t.Context(), Request{
		Estate:         "stamp-unit",
		Config:         cfg,
		Schemas:        testSchemas(),
		NeedsDiscovery: needsDiscovery("aws_subnet.app"),
	})
	assertNoErrors(t, diags)

	assertTags(t, evalTags(t, cfg, "aws_subnet.app", map[string]cty.Value{
		"var": cty.ObjectVal(map[string]cty.Value{
			"tags": cty.MapVal(map[string]cty.Value{"tofu-address": cty.StringVal("aws_subnet.somewhere_else")}),
		}),
	}), map[string]string{
		"tofu-estate":  "stamp-unit",
		"tofu-address": "aws_subnet.app",
	})
}

// TestStamp_mergeLiteralMarkerIsVerifiedNotDuplicated: a marker written in one
// of merge's own object literals is checked like any other, and a correct one
// is left alone rather than written twice.
func TestStamp_mergeLiteralMarkerIsVerifiedNotDuplicated(t *testing.T) {
	cfg := loadSource(t, `
locals {
  common = { team = "platform" }
}

resource "aws_subnet" "app" {
  cidr_block = "10.42.1.0/24"

  tags = merge(local.common, {
    tofu-estate  = "stamp-unit"
    tofu-address = "aws_subnet.app"
  })
}
`)

	res, diags := Stamp(t.Context(), Request{
		Estate:         "stamp-unit",
		Config:         cfg,
		Schemas:        testSchemas(),
		NeedsDiscovery: needsDiscovery("aws_subnet.app"),
	})
	assertNoErrors(t, diags)
	if len(res.Stamped) != 0 {
		t.Errorf("correct markers inside a merge() were stamped over: %+v", res.Stamped)
	}
	if !hasSkip(res, "aws_subnet.app", SkipAlreadyStamped) {
		t.Errorf("the no-op was not recorded as one: %v", res.Skipped)
	}
}

// TestStamp_mergeLiteralMarkerConflictIsAnError: and an incorrect one is the
// same conflict it would be anywhere else. A merge() is not a way to smuggle
// another estate's name past the check.
func TestStamp_mergeLiteralMarkerConflictIsAnError(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_subnet" "app" {
  cidr_block = "10.42.1.0/24"

  tags = merge({ tofu-estate = "other-estate" })
}
`)

	_, diags := Stamp(t.Context(), Request{
		Estate:         "stamp-unit",
		Config:         cfg,
		Schemas:        testSchemas(),
		NeedsDiscovery: needsDiscovery("aws_subnet.app"),
	})
	if !diags.HasErrors() {
		t.Fatal("a conflicting marker inside a merge() was accepted")
	}
	assertDiagContains(t, diags, "Ownership marker conflict", "other-estate")
}

// TestStamp_evaluatedTagsMarkerConflictIsAnError: the same check where the
// marker is not in the source at all but in what the source evaluates to.
func TestStamp_evaluatedTagsMarkerConflictIsAnError(t *testing.T) {
	cfg := loadSource(t, `
locals {
  tags = {
    tofu-estate = "other-estate"
  }
}

resource "aws_subnet" "app" {
  cidr_block = "10.42.1.0/24"

  tags = local.tags
}
`)

	_, diags := Stamp(t.Context(), Request{
		Estate:         "stamp-unit",
		Config:         cfg,
		Schemas:        testSchemas(),
		NeedsDiscovery: needsDiscovery("aws_subnet.app"),
	})
	if !diags.HasErrors() {
		t.Fatal("a conflicting marker in an evaluated tags argument was accepted")
	}
	assertDiagContains(t, diags, "Ownership marker conflict", "other-estate")
}

// TestStamp_nullTagsAreReplaced: "tags = null" says there are no tags, and
// merging into null is an error, so the marker object replaces it outright.
func TestStamp_nullTagsAreReplaced(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_subnet" "app" {
  cidr_block = "10.42.1.0/24"

  tags = null
}
`)

	_, diags := Stamp(t.Context(), Request{
		Estate:         "stamp-unit",
		Config:         cfg,
		Schemas:        testSchemas(),
		NeedsDiscovery: needsDiscovery("aws_subnet.app"),
	})
	assertNoErrors(t, diags)

	assertTags(t, evalTags(t, cfg, "aws_subnet.app", nil), map[string]string{
		"tofu-estate":  "stamp-unit",
		"tofu-address": "aws_subnet.app",
	})
}

// TestStamp_perInstanceMarkersSurviveAMerge: a for_each block's address
// marker is a template over each.key, and it has to stay one when it is
// injected into a merge() rather than into an object literal. A constant here
// would give every instance one address, which is the collision the whole
// keyed-address design exists to avoid.
func TestStamp_perInstanceMarkersSurviveAMerge(t *testing.T) {
	cfg := loadSource(t, `
variable "tags" {
  type    = map(string)
  default = {}
}

resource "aws_subnet" "this" {
  for_each = { a = "10.42.1.0/24", b = "10.42.2.0/24" }

  cidr_block = each.value
  tags       = var.tags
}
`)

	_, diags := Stamp(t.Context(), Request{
		Estate:         "stamp-unit",
		Config:         cfg,
		Schemas:        testSchemas(),
		NeedsDiscovery: needsDiscovery("aws_subnet.this"),
	})
	assertNoErrors(t, diags)

	for _, key := range []string{"a", "b"} {
		vars := eachData(key)
		vars["var"] = cty.ObjectVal(map[string]cty.Value{"tags": cty.MapValEmpty(cty.String)})
		assertTags(t, evalTags(t, cfg, "aws_subnet.this", vars), map[string]string{
			"tofu-estate":  "stamp-unit",
			"tofu-address": "aws_subnet.this:" + key,
		})
	}
}

// ---------------------------------------------------------------------------
// Unstampable is an error when only the marker could find it
// ---------------------------------------------------------------------------

// TestStamp_jsonSyntaxIsFatalForAMarkerDiscoveredResource: a body this pass
// cannot rewrite at all. For a resource named by its own configuration that is
// a warning and always was; for one the provider names, it is the
// duplicate-creation loop, so the run stops.
func TestStamp_jsonSyntaxIsFatalForAMarkerDiscoveredResource(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.tf.json", `{
  "resource": {
    "aws_subnet": {
      "app": {
        "cidr_block": "10.42.1.0/24"
      }
    }
  }
}`)
	cfg := loadDir(t, dir)

	_, diags := Stamp(t.Context(), Request{
		Estate:         "stamp-unit",
		Config:         cfg,
		Schemas:        testSchemas(),
		NeedsDiscovery: needsDiscovery("aws_subnet.app"),
	})
	if !diags.HasErrors() {
		t.Fatal("a marker-discovered resource that cannot be stamped was allowed through")
	}
	assertDiagContains(t, diags, "Unmarked apply of a marker-only resource", "aws_subnet.app")
	if !strings.Contains(diags.Err().Error(), "never see again") {
		t.Errorf("the error does not say what applying it would do:\n%s", diags.Err())
	}
}

// TestStamp_jsonSyntaxIsAWarningForAClientNamedResource is the contrast, and
// the reason the resolutions have to travel into this package at all: the same
// unstampable body, on a resource a later run can still find by name, stays a
// warning. Unmarked is a real cost there - the estate loses its record of
// owning it - but not an unrecoverable one.
func TestStamp_jsonSyntaxIsAWarningForAClientNamedResource(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.tf.json", `{
  "resource": {
    "aws_s3_bucket": {
      "data": {
        "bucket": "stamp-unit-data"
      }
    }
  }
}`)
	cfg := loadDir(t, dir)

	res, diags := Stamp(t.Context(), Request{
		Estate:  "stamp-unit",
		Config:  cfg,
		Schemas: testSchemas(),
	})
	assertNoErrors(t, diags)
	assertDiagContains(t, diags, "Ownership markers not stamped", "aws_s3_bucket.data")
	if !hasSkip(res, "aws_s3_bucket.data", SkipNotHCL) {
		t.Errorf("the unstampable resource is not in the skip list: %v", res.Skipped)
	}
}

// TestStamp_untaggableMarkerDiscoveredTypeIsFatal: a type with nowhere to
// carry a marker, whose identity nothing but a marker could recover. That
// combination cannot be managed at all, and saying so is better than applying
// it and finding out one run later.
func TestStamp_untaggableMarkerDiscoveredTypeIsFatal(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_route_table_association" "this" {
  subnet_id      = "subnet-1"
  route_table_id = "rtb-1"
}
`)

	_, diags := Stamp(t.Context(), Request{
		Estate:         "stamp-unit",
		Config:         cfg,
		Schemas:        testSchemas(),
		NeedsDiscovery: needsDiscovery("aws_route_table_association.this"),
	})
	if !diags.HasErrors() {
		t.Fatal("an untaggable marker-discovered resource was allowed through")
	}
	assertDiagContains(t, diags, "Unmarked apply of a marker-only resource", "nowhere to carry")
}

// TestStamp_untaggableClientNamedTypeIsStillSilent: the ordinary case the rule
// above must not disturb. A bucket policy carries no tags and never needed to.
func TestStamp_untaggableClientNamedTypeIsStillSilent(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_s3_bucket_policy" "data" {
  bucket = "stamp-unit-data"
  policy = "{}"
}
`)

	res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)
	if len(diags) != 0 {
		t.Errorf("an untaggable client-named resource produced diagnostics: %s", diags.ErrWithWarnings())
	}
	if !hasSkip(res, "aws_s3_bucket_policy.data", SkipUntaggable) {
		t.Errorf("the untaggable resource is not in the skip list: %v", res.Skipped)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

// localsMap puts a locals object of arbitrary values in scope, for fixtures
// whose tags merge a whole map out of one.
func localsMap(pairs map[string]cty.Value) map[string]cty.Value {
	return map[string]cty.Value{"local": cty.ObjectVal(pairs)}
}
