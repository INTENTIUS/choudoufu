// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang/funcs"
	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// estateDir is the P0.1 fixture, which hand-writes its markers on every
// taggable resource. Stamping it has to be a no-op, and that is a test rather
// than an aspiration: see TestStamp_estateFixtureIsUntouched.
func estateDir(t *testing.T) string {
	return flocitest.EstateDir(t)
}

// ---------------------------------------------------------------------------
// The main case: a bare resource gains both markers
// ---------------------------------------------------------------------------

func TestStamp_bareResourceGainsMarkers(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}
`)

	res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)

	if len(res.Stamped) != 1 {
		t.Fatalf("stamped %d resources, want 1: %+v", len(res.Stamped), res.Stamped)
	}
	if got := res.Stamped[0].Addr.String(); got != "aws_vpc.main" {
		t.Errorf("stamped %s, want aws_vpc.main", got)
	}
	if got := strings.Join(res.Stamped[0].Keys, ","); got != "tofu-estate,tofu-address" {
		t.Errorf("stamped keys %q, want tofu-estate,tofu-address", got)
	}
	if res.Stamped[0].PerInstance {
		t.Errorf("a resource with neither count nor for_each was stamped per instance")
	}

	// The claim that matters: evaluating the rewritten body produces the two
	// markers. Everything else in this package is bookkeeping around that.
	tags := evalTags(t, cfg, "aws_vpc.main", nil)
	assertTags(t, tags, map[string]string{
		"tofu-estate":  "stamp-unit",
		"tofu-address": "aws_vpc.main",
	})
}

// TestStamp_existingTagsAreKept: a resource that already sets tags of its own
// keeps them and gains the markers alongside.
func TestStamp_existingTagsAreKept(t *testing.T) {
	cfg := loadSource(t, `
locals {
  team = "platform"
}

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"

  tags = {
    Name  = "primary"
    owner = local.team
  }
}
`)

	_, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)

	assertTags(t, evalTags(t, cfg, "aws_vpc.main", localsData(map[string]string{"team": "platform"})), map[string]string{
		"Name":         "primary",
		"owner":        "platform",
		"tofu-estate":  "stamp-unit",
		"tofu-address": "aws_vpc.main",
	})
}

// TestStamp_correctMarkersAreNoOp: markers already written, and written
// right, are left exactly as they are. The estate fixture is this case, and
// so is any configuration a previous stamped apply produced.
func TestStamp_correctMarkersAreNoOp(t *testing.T) {
	cfg := loadSource(t, `
locals {
  estate = "stamp-unit"
}

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"

  tags = {
    tofu-estate  = local.estate
    tofu-address = "aws_vpc.main"
  }
}
`)

	before := tagsSource(t, cfg, "aws_vpc.main")

	res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)

	if len(res.Stamped) != 0 {
		t.Errorf("a correctly marked resource was stamped anyway: %+v", res.Stamped)
	}
	if !hasSkip(res, "aws_vpc.main", SkipAlreadyStamped) {
		t.Errorf("the no-op was not recorded as such: %v", res.Skipped)
	}
	if after := tagsSource(t, cfg, "aws_vpc.main"); after != before {
		t.Errorf("the tags argument was rewritten:\nbefore %d entries\nafter  %d entries", before, after)
	}
	assertTags(t, evalTags(t, cfg, "aws_vpc.main", localsData(map[string]string{"estate": "stamp-unit"})), map[string]string{
		"tofu-estate":  "stamp-unit",
		"tofu-address": "aws_vpc.main",
	})
}

// TestStamp_partialMarkersAreCompleted: one marker written, the other
// missing. Only the missing one is added.
func TestStamp_partialMarkersAreCompleted(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"

  tags = {
    tofu-estate = "stamp-unit"
  }
}
`)

	res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)

	if len(res.Stamped) != 1 || strings.Join(res.Stamped[0].Keys, ",") != "tofu-address" {
		t.Fatalf("stamped %+v, want tofu-address alone", res.Stamped)
	}
	assertTags(t, evalTags(t, cfg, "aws_vpc.main", nil), map[string]string{
		"tofu-estate":  "stamp-unit",
		"tofu-address": "aws_vpc.main",
	})
}

// ---------------------------------------------------------------------------
// Conflicts
// ---------------------------------------------------------------------------

// TestStamp_wrongEstateIsAnError: a marker claiming another estate is never
// overwritten. That would be a transfer of ownership performed as a side
// effect of running a plan.
func TestStamp_wrongEstateIsAnError(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"

  tags = {
    tofu-estate  = "other-estate"
    tofu-address = "aws_vpc.main"
  }
}
`)

	res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	if !diags.HasErrors() {
		t.Fatalf("a marker naming another estate was accepted: %+v", res)
	}
	assertDiagContains(t, diags, "Ownership marker conflict", "other-estate", "stamp-unit", "aws_vpc.main")

	// Nothing was rewritten: the operator has to read the resource, and it
	// should read the way they wrote it.
	assertTags(t, evalTags(t, cfg, "aws_vpc.main", nil), map[string]string{
		"tofu-estate":  "other-estate",
		"tofu-address": "aws_vpc.main",
	})
}

// TestStamp_wrongAddressIsAnError: a marker naming another address is a
// rename, and renames belong to live-mv.
func TestStamp_wrongAddressIsAnError(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"

  tags = {
    tofu-estate  = "stamp-unit"
    tofu-address = "aws_vpc.old_name"
  }
}
`)

	_, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	if !diags.HasErrors() {
		t.Fatal("a marker naming another address was accepted")
	}
	assertDiagContains(t, diags, "Ownership marker conflict", "aws_vpc.old_name", "live-mv")
}

// TestStamp_conflictStopsEveryRewrite: one conflict anywhere leaves the whole
// configuration as it was, rather than a half-stamped one the operator has to
// reason about.
func TestStamp_conflictStopsEveryRewrite(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}

resource "aws_s3_bucket" "data" {
  bucket = "b"

  tags = {
    tofu-estate = "somebody-else"
  }
}
`)

	res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	if !diags.HasErrors() {
		t.Fatal("the conflicting resource was accepted")
	}
	if len(res.Stamped) != 0 {
		t.Errorf("a stamped list survived a failed pass: %+v", res.Stamped)
	}
	if tags := evalTags(t, cfg, "aws_vpc.main", nil); len(tags) != 0 {
		t.Errorf("the bare resource was rewritten even though the pass failed: %v", tags)
	}
}

// TestStamp_unreadableMarkerIsWarnedNotOverwritten: a marker built from
// something this pass cannot evaluate is left alone, with a warning. Silence
// would be a lie in either direction - claiming it is right, or overwriting
// something that was.
func TestStamp_unreadableMarkerIsWarnedNotOverwritten(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"

  tags = {
    tofu-estate  = "stamp-unit"
    tofu-address = aws_s3_bucket.data.bucket
  }
}

resource "aws_s3_bucket" "data" {
  bucket = "b"

  tags = {
    tofu-estate  = "stamp-unit"
    tofu-address = "aws_s3_bucket.data"
  }
}
`)

	res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)
	if len(diags) == 0 {
		t.Fatal("an unreadable marker produced no diagnostic at all")
	}
	assertDiagContains(t, diags, "Ownership marker could not be checked", "aws_vpc.main")
	if !hasSkip(res, "aws_vpc.main", SkipMarkerUnreadable) {
		t.Errorf("the unreadable marker is not in the skip list: %v", res.Skipped)
	}
	if len(res.Stamped) != 0 {
		t.Errorf("an unreadable marker was stamped over: %+v", res.Stamped)
	}
}

// TestStamp_variableTagsAreMergedInto: a tags argument the pass cannot read
// entry by entry gets the markers merged onto it rather than being skipped.
//
// This asserted the opposite until the audit (finding C2): the skip was a
// warning, the run continued, and a resource whose identity is server-assigned
// was applied with no marker at all - unfindable ever after. What the pass
// cannot read it now merges into, and merge's last argument wins, so the
// markers on the applied resource are the ones this run stamped.
func TestStamp_variableTagsAreMergedInto(t *testing.T) {
	cfg := loadSource(t, `
variable "tags" {
  type    = map(string)
  default = { team = "platform" }
}

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
  tags       = var.tags
}
`)

	res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)
	if len(diags) != 0 {
		t.Errorf("merging into a variable tags argument warned about it: %s", diags.ErrWithWarnings())
	}
	if hasSkip(res, "aws_vpc.main", SkipTagsUnreadable) {
		t.Errorf("the tags argument was skipped instead of merged into: %v", res.Skipped)
	}
	if len(res.Stamped) != 1 || !res.Stamped[0].Merged {
		t.Errorf("the pass does not report a merged stamping: %+v", res.Stamped)
	}

	assertTags(t, evalTags(t, cfg, "aws_vpc.main", map[string]cty.Value{
		"var": cty.ObjectVal(map[string]cty.Value{
			"tags": cty.MapVal(map[string]cty.Value{"team": cty.StringVal("platform")}),
		}),
	}), map[string]string{
		"team":         "platform",
		"tofu-estate":  "stamp-unit",
		"tofu-address": "aws_vpc.main",
	})
}

// ---------------------------------------------------------------------------
// Taggability comes from the schema
// ---------------------------------------------------------------------------

// TestStamp_untaggableTypesAreSkippedSilently: a type whose schema has no
// tags attribute is not a gap in the estate's records. It is skipped, with no
// diagnostic at all.
func TestStamp_untaggableTypesAreSkippedSilently(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_route" "r" {
  route_table_id = "rtb-1"
}

resource "aws_computed_tags" "c" {
  name = "x"
}
`)

	res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)
	if len(diags) != 0 {
		t.Errorf("an untaggable type produced diagnostics: %s", diags.ErrWithWarnings())
	}
	if len(res.Stamped) != 0 {
		t.Fatalf("an untaggable type was stamped: %+v", res.Stamped)
	}
	for _, addr := range []string{"aws_route.r", "aws_computed_tags.c"} {
		if !hasSkip(res, addr, SkipUntaggable) {
			t.Errorf("%s is not recorded as untaggable: %v", addr, res.Skipped)
		}
	}
}

// TestTaggable is the schema rule on its own: a top-level tags attribute of a
// map type that configuration may set.
func TestTaggable(t *testing.T) {
	for name, tc := range map[string]struct {
		attr *configschema.Attribute
		want bool
	}{
		"optional map of string": {&configschema.Attribute{Type: cty.Map(cty.String), Optional: true}, true},
		"required map of string": {&configschema.Attribute{Type: cty.Map(cty.String), Required: true}, true},
		"map of dynamic":         {&configschema.Attribute{Type: cty.Map(cty.DynamicPseudoType), Optional: true}, true},
		"computed only":          {&configschema.Attribute{Type: cty.Map(cty.String), Computed: true}, false},
		"a string":               {&configschema.Attribute{Type: cty.String, Optional: true}, false},
		"a list":                 {&configschema.Attribute{Type: cty.List(cty.String), Optional: true}, false},
		"an object":              {&configschema.Attribute{Type: cty.Object(map[string]cty.Type{"a": cty.String}), Optional: true}, false},
		"absent":                 {nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			block := &configschema.Block{Attributes: map[string]*configschema.Attribute{}}
			if tc.attr != nil {
				block.Attributes["tags"] = tc.attr
			}
			if got := taggable(block); got != tc.want {
				t.Errorf("taggable = %v, want %v", got, tc.want)
			}
		})
	}
}

// The taggability pin: the v0 admission table, split by what the AWS
// provider's schema answers for each type. Whether a type can carry a marker
// is read from the schema at runtime (taggable), never from a list, so the
// admission table itself does not record the answer - and without this pin a
// provider release that adds a tags argument to aws_route, or removes one
// from a type that has it, would change SkipUntaggable behavior with no test
// failing. live/LIMITATIONS.md's "Untaggable types cannot be removed by the
// sweep" entry names this same set, rendered from live/survey-full.json
// (issue #54, tools/survey-gen/untaggable_render.go) rather than the
// curated 68 live/survey.json measures - which is what let
// untaggableAdmittedTypes fold back into one list after the registry-ratified
// batches added untaggable types outside that curated roster. It used to
// carry a second, split-out list (untaggableOutsideCuratedSurvey) for
// exactly those types, worked around because the doc's own derivation
// couldn't see past the curated 68 yet; now that it can, the split is gone.
// TestUntaggableTypesMatchLimitationsDoc keeps this list and the doc the
// same.
var (
	taggableAdmittedTypes = []string{
		"aws_vpc",
		"aws_subnet",
		"aws_security_group",
		"aws_route_table",
		"aws_internet_gateway",
		"aws_eip",
		"aws_s3_bucket",
		"aws_iam_role",
		"aws_cloudwatch_log_group",
		"aws_ssm_parameter",
		"aws_dynamodb_table",
		"aws_ecs_cluster",
		"aws_kms_key",
		"aws_route53_zone",
		"aws_cloudwatch_metric_alarm",
		"aws_lb",
		"aws_lb_target_group",
		"aws_lb_listener",
		"aws_sns_topic",
		"aws_vpc_security_group_ingress_rule",
		"aws_vpc_security_group_egress_rule",
		"aws_launch_template",
		"aws_acm_certificate",
		"aws_sfn_state_machine",
		"aws_ebs_volume",
	}
	untaggableAdmittedTypes = []string{
		"aws_route",
		"aws_route_table_association",
		"aws_s3_bucket_policy",
		"aws_iam_role_policy_attachment",
		"aws_s3_bucket_versioning",
		"aws_s3_bucket_public_access_block",
		"aws_s3_bucket_server_side_encryption_configuration",
		"aws_s3_bucket_lifecycle_configuration",
		"aws_iam_role_policy",
		"aws_kms_alias",
		"aws_route53_record",
		"aws_lb_target_group_attachment",
	}
)

// TestTaggableSetCoversAdmissionTable is the taggability half of lint's
// TestAdmissionTableCoversEstate: that test pins which types are admitted,
// and this one pins which of the admitted types can carry a marker. The two
// pinned lists must cover identity.AdmittedTypes exactly, in both
// directions, so a type added to the admission table has to be classified
// here before this passes.
//
// The schemas are the caricature in testSchemas rather than the real AWS
// provider's, the same trade every unit test in this file makes.
// TestTaggableSetAgainstRealSchemas in stamp_live_test.go is the same pin
// against the schema the real provider serves.
func TestTaggableSetCoversAdmissionTable(t *testing.T) {
	pinned := make(map[string]bool, len(taggableAdmittedTypes)+len(untaggableAdmittedTypes))
	for _, resourceType := range taggableAdmittedTypes {
		pinned[resourceType] = true
	}
	for _, resourceType := range untaggableAdmittedTypes {
		if pinned[resourceType] {
			t.Errorf("%s is pinned as both taggable and untaggable", resourceType)
		}
		pinned[resourceType] = true
	}
	for _, resourceType := range identity.AdmittedTypes() {
		// GitHub issue #73's RECORD_ADMITTED logical types (null_resource,
		// terraform_data, the time_* and random_* rows
		// internal/live/identity/table_recordbacked.go admits) carry no AWS
		// tags argument to have or lack at all - they are not AWS resources,
		// and their identity is the persisted micro-state record itself, not
		// a marker. "Taggable" is not a question this pin needs to answer
		// for them, the same reason tools/survey-gen's own untaggable
		// derivation skips them (see untaggable_render.go).
		if entry, ok := identity.LookupType(resourceType); ok && entry.RecordBacked {
			continue
		}
		if !pinned[resourceType] {
			t.Errorf("%s is in the v0 admission table but its taggability is not pinned here", resourceType)
		}
		delete(pinned, resourceType)
	}
	for resourceType := range pinned {
		t.Errorf("%s has its taggability pinned here but is not in the v0 admission table", resourceType)
	}

	schemas := testSchemas()
	check := func(types []string, want bool) {
		for _, resourceType := range types {
			schema, _ := schemas.ResourceTypeConfig(addrs.NewDefaultProvider("aws"), addrs.ManagedResourceMode, resourceType)
			if schema == nil || schema.Block == nil {
				t.Errorf("the test schemas have no entry for admitted type %s", resourceType)
				continue
			}
			if got := taggable(schema.Block); got != want {
				t.Errorf("taggable(%s) = %v, want %v", resourceType, got, want)
			}
		}
	}
	check(taggableAdmittedTypes, true)
	check(untaggableAdmittedTypes, false)
}

// TestUntaggableTypesMatchLimitationsDoc: live/LIMITATIONS.md tells the
// operator which types the sweep cannot remove because they carry no tags,
// and that list is the untaggable pin above in prose form. Drift between the
// two would mean the doc names a type the code stamps, or stays silent about
// one it skips, so the doc's own entry is held to the pinned set.
func TestUntaggableTypesMatchLimitationsDoc(t *testing.T) {
	doc := filepath.Join(flocitest.RepoRoot(t), "live", "LIMITATIONS.md")
	content, err := os.ReadFile(doc) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		t.Fatalf("reading %s: %v", doc, err)
	}

	const heading = "**Untaggable types carry no ownership marker of their own.**"
	_, entry, found := strings.Cut(string(content), heading)
	if !found {
		t.Fatalf("live/LIMITATIONS.md has no %q entry", heading)
	}
	if end := strings.Index(entry, "\n\n"); end >= 0 {
		entry = entry[:end]
	}

	var docTypes []string
	for _, m := range regexp.MustCompile("`([a-z0-9_]+)`").FindAllStringSubmatch(entry, -1) {
		docTypes = append(docTypes, m[1])
	}
	sort.Strings(docTypes)

	want := append([]string(nil), untaggableAdmittedTypes...)
	sort.Strings(want)

	if strings.Join(docTypes, " ") != strings.Join(want, " ") {
		t.Errorf("the doc entry names %v, want exactly the pinned untaggable set %v", docTypes, want)
	}
}

// TestStamp_tagsAsNestedBlockIsNotStamped: the tags-as-repeated-blocks shape
// is not the tag map the marker spec describes, and is left alone.
func TestStamp_tagsAsNestedBlockIsNotStamped(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_tag_blocks" "asg" {
  name = "app"

  tag {
    key   = "Name"
    value = "app"
  }
}
`)

	res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)
	if len(res.Stamped) != 0 {
		t.Errorf("a type whose tags are blocks was stamped: %+v", res.Stamped)
	}
}

// ---------------------------------------------------------------------------
// count and for_each
// ---------------------------------------------------------------------------

// TestStamp_countInstancesGetEscapedAddresses: every instance of a count
// resource gets its own escaped address, which is exactly what discovery
// normalizes an observed marker to.
func TestStamp_countInstancesGetEscapedAddresses(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_eip" "pool" {
  count = 3
}
`)

	res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)
	if len(res.Stamped) != 1 || !res.Stamped[0].PerInstance {
		t.Fatalf("stamped %+v, want one per-instance entry", res.Stamped)
	}

	for i, want := range map[int]string{0: "aws_eip.pool:0", 1: "aws_eip.pool:1", 2: "aws_eip.pool:2"} {
		tags := evalTags(t, cfg, "aws_eip.pool", countData(i))
		assertTags(t, tags, map[string]string{
			"tofu-estate":  "stamp-unit",
			"tofu-address": want,
		})
	}

	// No slot table in the request, so no slot marker: a run that did not
	// read the live estate has nothing to say about which member is which.
	if tags := evalTags(t, cfg, "aws_eip.pool", countData(0)); tags["tofu-slot"] != "" {
		t.Errorf("a slot marker was stamped with no slot table: %v", tags)
	}
}

// TestStamp_forEachInstancesGetEscapedAddresses: the same for for_each, where
// the escaping rule drops the quotes around the key.
func TestStamp_forEachInstancesGetEscapedAddresses(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_subnet" "this" {
  for_each   = { a = "10.42.1.0/24", b = "10.42.2.0/24" }
  cidr_block = each.value
}
`)

	_, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)

	for _, key := range []string{"a", "b"} {
		assertTags(t, evalTags(t, cfg, "aws_subnet.this", eachData(key)), map[string]string{
			"tofu-estate":  "stamp-unit",
			"tofu-address": "aws_subnet.this:" + key,
		})
	}
}

// TestStamp_handWrittenInstanceAddressIsRecognized: the shape the estate
// fixture writes by hand is recognized as already correct, structurally,
// rather than being warned about as unreadable.
func TestStamp_handWrittenInstanceAddressIsRecognized(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_eip" "pool" {
  count = 3

  tags = {
    tofu-estate  = "stamp-unit"
    tofu-address = "aws_eip.pool:${count.index}"
  }
}

resource "aws_subnet" "this" {
  for_each = { a = "10.42.1.0/24" }

  tags = {
    tofu-estate  = "stamp-unit"
    tofu-address = "aws_subnet.this:${each.key}"
  }
}
`)

	res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)
	if len(diags) != 0 {
		t.Errorf("the hand-written instance markers produced diagnostics: %s", diags.ErrWithWarnings())
	}
	if len(res.Stamped) != 0 {
		t.Errorf("hand-written instance markers were stamped over: %+v", res.Stamped)
	}
}

// TestStamp_constantAddressOnACountResourceIsAnError: one address shared by
// three instances is a collision waiting to happen, and MARKERS.md calls it
// one. It is named rather than silently replaced.
func TestStamp_constantAddressOnACountResourceIsAnError(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_eip" "pool" {
  count = 3

  tags = {
    tofu-estate  = "stamp-unit"
    tofu-address = "aws_eip.pool"
  }
}
`)

	_, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	if !diags.HasErrors() {
		t.Fatal("a constant address on a count resource was accepted")
	}
	// The remedy has to name the interpolation, not addressExpr's display
	// string. This used to assert "aws_eip.pool:count.index", which is what
	// the message told the user to write - and writing it literally sets the
	// same constant on all three instances, reproducing this very error.
	// See #101.
	assertDiagContains(t, diags, "Ownership marker conflict", "count", "${count.index}")

	for _, d := range diags {
		if strings.Contains(d.Description().Detail, `Write it as "aws_eip.pool:count.index"`) {
			t.Errorf("the remedy quotes addressExpr's display string, which has no interpolation in it: %s", d.Description().Detail)
		}
	}
}

// ---------------------------------------------------------------------------
// Inputs that are not a configuration to stamp
// ---------------------------------------------------------------------------

func TestStamp_refusesBadInput(t *testing.T) {
	cfg := loadSource(t, `resource "aws_vpc" "main" {}`)

	for name, tc := range map[string]struct {
		req  Request
		want string
	}{
		"no estate name": {
			Request{Estate: "", Config: cfg, Schemas: testSchemas()},
			"No estate name to stamp with",
		},
		"an estate name outside the grammar": {
			Request{Estate: "Not_An_Estate", Config: cfg, Schemas: testSchemas()},
			"No estate name to stamp with",
		},
		"no configuration": {
			Request{Estate: "stamp-unit", Schemas: testSchemas()},
			"No configuration to stamp",
		},
		"no schemas": {
			Request{Estate: "stamp-unit", Config: cfg},
			"No provider schemas for marker stamping",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, diags := Stamp(t.Context(), tc.req)
			if !diags.HasErrors() {
				t.Fatalf("no error for %s", name)
			}
			assertDiagContains(t, diags, tc.want)
		})
	}

	// Whatever it refused, it must not have written anything.
	if tags := evalTags(t, cfg, "aws_vpc.main", nil); len(tags) != 0 {
		t.Errorf("a refused pass rewrote the configuration anyway: %v", tags)
	}
}

// ---------------------------------------------------------------------------
// The estate fixture
// ---------------------------------------------------------------------------

// TestStamp_estateFixtureIsUntouched is the no-op case at fixture scale: the
// P0.1 estate hand-writes both markers on every taggable resource, so a
// stamping pass over it adds nothing, warns about nothing, and leaves a plan
// with no marker changes in it.
//
// The schemas here are the caricature below rather than the real AWS
// provider's, so the set of "taggable" types is the set this test declares.
// The integration test runs the same assertion against the real schemas.
func TestStamp_estateFixtureIsUntouched(t *testing.T) {
	cfg := loadDir(t, estateDir(t))

	res, diags := Stamp(t.Context(), Request{Estate: "stateless-e2e", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)
	if len(diags) != 0 {
		t.Errorf("stamping the estate fixture produced diagnostics: %s", diags.ErrWithWarnings())
	}
	if len(res.Stamped) != 0 {
		t.Errorf("stamping the estate fixture changed it: %+v", res.Stamped)
	}

	// And the no-op is because the markers are right, not because nothing was
	// looked at.
	var alreadyStamped int
	for _, s := range res.Skipped {
		if s.Reason == SkipAlreadyStamped {
			alreadyStamped++
		}
	}
	if alreadyStamped == 0 {
		t.Errorf("no resource in the estate fixture was recognized as already stamped: %v", res.Skipped)
	}
}

// TestStamp_estateFixtureUnderAnotherEstateConflicts: the same fixture, asked
// for by the wrong estate name, is a conflict on every marked resource rather
// than a mass rewrite.
func TestStamp_estateFixtureUnderAnotherEstateConflicts(t *testing.T) {
	cfg := loadDir(t, estateDir(t))

	_, diags := Stamp(t.Context(), Request{Estate: "somebody-else", Config: cfg, Schemas: testSchemas()})
	if !diags.HasErrors() {
		t.Fatal("stamping another estate's name over the fixture was accepted")
	}
	assertDiagContains(t, diags, "Ownership marker conflict", "stateless-e2e")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// testSchemaSource is a Schemas over a plain map of resource type to block.
type testSchemaSource map[string]*configschema.Block

func (s testSchemaSource) ResourceTypeConfig(_ addrs.Provider, mode addrs.ResourceMode, typeName string) (*providers.Schema, uint64) {
	if mode != addrs.ManagedResourceMode {
		return nil, 0
	}
	block, ok := s[typeName]
	if !ok {
		return nil, 0
	}
	return &providers.Schema{Block: block}, 0
}

// testSchemas is a caricature of the AWS provider: the types the fixtures in
// this file and in live/e2e/estate use, tagged or not as the real
// provider has them.
// taggedSchema and untaggedSchema build one caricature resource schema, with
// and without the tags argument. They were closures inside testSchemas until
// the per-cohort split (contributing/LIVE-TABLES.md) gave the cohort
// fragments their own files, which need to call them too; hoisting them here
// is the only reason their names changed.
func taggedSchema(names ...string) *configschema.Block {
	attrs := map[string]*configschema.Attribute{
		"tags": {Type: cty.Map(cty.String), Optional: true},
	}
	for _, n := range names {
		attrs[n] = &configschema.Attribute{Type: cty.String, Optional: true, Computed: true}
	}
	return &configschema.Block{Attributes: attrs}
}

func untaggedSchema(names ...string) *configschema.Block {
	attrs := map[string]*configschema.Attribute{}
	for _, n := range names {
		attrs[n] = &configschema.Attribute{Type: cty.String, Optional: true, Computed: true}
	}
	return &configschema.Block{Attributes: attrs}
}

// schemaFragments are the per-cohort contributions to [testSchemas], each
// appended by one stamp_cohort_*_test.go file's init.
var schemaFragments []func(testSchemaSource)

// registerCohortStamp folds one cohort's slice of all three pinned
// collections in. Package-level var initializers all run before any init
// func, so the core literals below are complete before the first fragment
// appends to them, and the order the fragments arrive in does not matter:
// every one of the three is consumed as a set (TestTaggableSetCoversAdmissionTable
// builds a map, TestUntaggableTypesMatchLimitationsDoc sorts before
// comparing).
func registerCohortStamp(taggable, untaggable []string, schemas func(testSchemaSource)) {
	taggableAdmittedTypes = append(taggableAdmittedTypes, taggable...)
	untaggableAdmittedTypes = append(untaggableAdmittedTypes, untaggable...)
	if schemas != nil {
		schemaFragments = append(schemaFragments, schemas)
	}
}

// mergeCohortSchemas adds one cohort's caricature schemas to the map being
// built, refusing a type two cohorts both describe rather than letting the
// later fragment silently win it - the add-only rule
// tools/mapping-gen/overlay.d applies to its own fragments.
func mergeCohortSchemas(dst, src testSchemaSource) {
	for k, v := range src {
		if _, dup := dst[k]; dup {
			panic("stamp: type " + k + " has a test schema in more than one cohort fragment")
		}
		dst[k] = v
	}
}

func testSchemas() Schemas {
	s := testSchemaSource{
		// Taggable, as the real AWS provider has them.
		"aws_vpc":                  taggedSchema("id", "cidr_block"),
		"aws_subnet":               taggedSchema("id", "vpc_id", "cidr_block", "availability_zone"),
		"aws_security_group":       taggedSchema("id", "name", "description", "vpc_id"),
		"aws_route_table":          taggedSchema("id", "vpc_id"),
		"aws_internet_gateway":     taggedSchema("id", "vpc_id"),
		"aws_eip":                  taggedSchema("id", "domain"),
		"aws_s3_bucket":            taggedSchema("id", "bucket"),
		"aws_iam_role":             taggedSchema("id", "name", "assume_role_policy"),
		"aws_cloudwatch_log_group": taggedSchema("id", "name", "retention_in_days"),
		"aws_ssm_parameter":        taggedSchema("id", "name", "type", "value"),
		"aws_dynamodb_table":       taggedSchema("id", "name", "billing_mode", "hash_key"),
		"aws_ecs_cluster":          taggedSchema("id", "name", "arn"),
		"aws_kms_key":              taggedSchema("id", "key_id", "description"),
		"aws_route53_zone":         taggedSchema("id", "zone_id", "name"),
		"aws_cloudwatch_metric_alarm": taggedSchema("id", "alarm_name", "comparison_operator", "evaluation_periods",
			"metric_name", "namespace", "period", "statistic", "threshold"),

		// Untaggable, likewise.
		"aws_route":                                          untaggedSchema("route_table_id", "destination_cidr_block", "gateway_id"),
		"aws_route_table_association":                        untaggedSchema("subnet_id", "route_table_id"),
		"aws_s3_bucket_policy":                               untaggedSchema("bucket", "policy"),
		"aws_iam_role_policy_attachment":                     untaggedSchema("role", "policy_arn"),
		"aws_s3_bucket_versioning":                           untaggedSchema("id", "bucket"),
		"aws_s3_bucket_public_access_block":                  untaggedSchema("id", "bucket", "block_public_acls", "block_public_policy"),
		"aws_s3_bucket_server_side_encryption_configuration": untaggedSchema("id", "bucket"),
		"aws_s3_bucket_lifecycle_configuration":              untaggedSchema("id", "bucket"),
		"aws_iam_role_policy":                                untaggedSchema("id", "role", "name", "policy"),
		"aws_kms_alias":                                      untaggedSchema("id", "name", "target_key_id"),
		"aws_route53_record":                                 untaggedSchema("id", "zone_id", "name", "type", "ttl"),
		"aws_lb":                                             taggedSchema("id", "arn", "name", "internal"),
		"aws_lb_target_group":                                taggedSchema("id", "arn", "name", "port", "protocol", "vpc_id"),
		"aws_lb_listener":                                    taggedSchema("id", "arn", "load_balancer_arn", "port", "protocol"),
		"aws_sns_topic":                                      taggedSchema("id", "arn", "name"),
		"aws_vpc_security_group_ingress_rule":                taggedSchema("id", "arn", "security_group_rule_id", "security_group_id", "cidr_ipv4", "from_port", "to_port", "ip_protocol"),
		"aws_vpc_security_group_egress_rule":                 taggedSchema("id", "arn", "security_group_rule_id", "security_group_id", "cidr_ipv4", "ip_protocol"),
		"aws_launch_template":                                taggedSchema("id", "arn", "name", "image_id", "instance_type"),
		"aws_acm_certificate":                                taggedSchema("id", "arn", "domain_name", "validation_method"),
		"aws_sfn_state_machine":                              taggedSchema("id", "arn", "name", "role_arn", "definition"),
		"aws_ebs_volume":                                     taggedSchema("id", "arn", "availability_zone", "size"),

		"aws_lb_target_group_attachment": untaggedSchema("id", "target_group_arn", "target_id", "port"),

		// Two shapes that are not the marker tag map: a computed-only tags
		// attribute, and tags carried as repeated blocks.
		"aws_computed_tags": {Attributes: map[string]*configschema.Attribute{
			"name": {Type: cty.String, Optional: true},
			"tags": {Type: cty.Map(cty.String), Computed: true},
		}},
		"aws_tag_blocks": {
			Attributes: map[string]*configschema.Attribute{
				"name": {Type: cty.String, Optional: true},
			},
			BlockTypes: map[string]*configschema.NestedBlock{
				"tag": {
					Nesting: configschema.NestingList,
					Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
						"key":   {Type: cty.String, Required: true},
						"value": {Type: cty.String, Required: true},
					}},
				},
			},
		},
	}
	for _, f := range schemaFragments {
		f(s)
	}
	return s
}

// loadSource writes one configuration file to a scratch directory and loads
// it the way the command does, static evaluator and all.
func loadSource(t *testing.T, src string) *configs.Config {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	return loadDir(t, dir)
}

func loadDir(t *testing.T, dir string) *configs.Config {
	t.Helper()

	parser := configs.NewParser(nil)
	call := configs.NewStaticModuleCall(
		addrs.RootModule,
		hcl.Range{},
		func(v *configs.Variable) (cty.Value, hcl.Diagnostics) { return v.Default, nil },
		dir,
		"default",
	)
	mod, diags := parser.LoadConfigDir(dir, call)
	if diags.HasErrors() {
		t.Fatalf("loading %s: %s", dir, diags.Error())
	}
	cfg, cfgDiags := configs.BuildConfig(t.Context(), mod, configs.ModuleWalkerFunc(
		func(_ context.Context, req *configs.ModuleRequest) (*configs.Module, *version.Version, hcl.Diagnostics) {
			t.Fatalf("fixture %s unexpectedly calls module %q", dir, req.Name)
			return nil, nil, nil
		},
	))
	if cfgDiags.HasErrors() {
		t.Fatalf("building config for %s: %s", dir, cfgDiags.Error())
	}
	return cfg
}

// evalTags decodes a resource body's tags argument the way the plan engine
// would, with the given instance-key variables in scope, and returns the tags
// as plain strings.
//
// This is the assertion that matters throughout this file: not "the AST has
// the right shape" but "evaluating the configuration produces the right
// tags", which is what the plan will do with it.
func evalTags(t *testing.T, cfg *configs.Config, addr string, vars map[string]cty.Value) map[string]string {
	t.Helper()

	rc, ok := cfg.Module.ManagedResources[addr]
	if !ok {
		t.Fatalf("no resource %s in the configuration", addr)
	}
	body, ok := rc.Config.(*hclsyntax.Body)
	if !ok {
		t.Fatalf("%s is not in HCL native syntax", addr)
	}
	attr, ok := body.Attributes["tags"]
	if !ok {
		return nil
	}

	val, diags := attr.Expr.Value(&hcl.EvalContext{Variables: vars, Functions: tagFunctions()})
	if diags.HasErrors() {
		t.Fatalf("evaluating %s's tags: %s", addr, diags.Error())
	}
	if val.IsNull() {
		return nil
	}

	out := make(map[string]string)
	for it := val.ElementIterator(); it.Next(); {
		k, v := it.Element()
		if v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
			t.Fatalf("%s's tag %s did not evaluate to a known string", addr, k.AsString())
		}
		out[k.AsString()] = v.AsString()
	}
	return out
}

// tagFunctions is the language functions a stamped tags argument can call.
// Only one is ever needed - the lookup that reads a slot out of the table
// this package writes - and it is the language's own implementation rather
// than a stand-in, so a test evaluating a stamped tag is evaluating what the
// plan will.
func tagFunctions() map[string]function.Function {
	return map[string]function.Function{
		"lookup": funcs.LookupFunc,
		// merge is the other one a stamped tags argument can call: the pass
		// injects markers into an expression it cannot read entry by entry by
		// merging its own object onto it. Same reasoning as lookup - the
		// language's own implementation, so evaluating a stamped tag here is
		// evaluating what the plan will.
		"merge": stdlib.MergeFunc,
		// substr is issue #71's addition: a per-instance address long enough
		// to need continuation tags is split with n independent substr()
		// calls (stamp.templateChunkMarkers). Same reasoning as the two
		// above.
		"substr": stdlib.SubstrFunc,
	}
}

// localsData puts a locals object in scope, for the fixtures whose tags refer
// to one.
func localsData(pairs map[string]string) map[string]cty.Value {
	vals := make(map[string]cty.Value, len(pairs))
	for k, v := range pairs {
		vals[k] = cty.StringVal(v)
	}
	return map[string]cty.Value{"local": cty.ObjectVal(vals)}
}

// countData and eachData are the instance-key variables the evaluator puts in
// scope for a count or for_each instance.
func countData(i int) map[string]cty.Value {
	return map[string]cty.Value{
		"count": cty.ObjectVal(map[string]cty.Value{"index": cty.NumberIntVal(int64(i))}),
	}
}

func eachData(key string) map[string]cty.Value {
	return map[string]cty.Value{
		"each": cty.ObjectVal(map[string]cty.Value{
			"key":   cty.StringVal(key),
			"value": cty.StringVal(key),
		}),
	}
}

// tagsSource counts the entries in a resource's tags object literal, as a
// cheap "was this rewritten" probe.
func tagsSource(t *testing.T, cfg *configs.Config, addr string) int {
	t.Helper()

	rc := cfg.Module.ManagedResources[addr]
	body := rc.Config.(*hclsyntax.Body)
	attr, ok := body.Attributes["tags"]
	if !ok {
		return 0
	}
	obj, ok := attr.Expr.(*hclsyntax.ObjectConsExpr)
	if !ok {
		return -1
	}
	return len(obj.Items)
}

func hasSkip(res *Result, addr string, reason SkipReason) bool {
	for _, s := range res.Skipped {
		if s.Addr.String() == addr && s.Reason == reason {
			return true
		}
	}
	return false
}

func assertTags(t *testing.T, got map[string]string, want map[string]string) {
	t.Helper()

	if len(got) != len(want) {
		t.Errorf("tags %v, want %v", got, want)
		return
	}
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if got[k] != want[k] {
			t.Errorf("tag %s = %q, want %q (all tags: %v)", k, got[k], want[k], got)
		}
	}
}

func assertNoErrors(t *testing.T, diags tfdiags.Diagnostics) {
	t.Helper()
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}
}

func assertDiagContains(t *testing.T, diags tfdiags.Diagnostics, wants ...string) {
	t.Helper()

	var text strings.Builder
	for _, d := range diags {
		desc := d.Description()
		text.WriteString(desc.Summary)
		text.WriteString("\n")
		text.WriteString(desc.Detail)
		text.WriteString("\n")
	}
	for _, want := range wants {
		if !strings.Contains(text.String(), want) {
			t.Errorf("no diagnostic mentioning %q:\n%s", want, text.String())
		}
	}
}
