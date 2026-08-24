// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
)

// TestSweepBindType is issue #394's own coverage for [sweepBindType]: the
// three-way answer a whole-estate sweep (the ARN-join tag sweep,
// [fileTaggingCandidate], and the Cloud Control per-type sweep,
// [scanTypeCloudControl]) needs before filing a candidate whose marker
// names a different type than the sweep found it listed as. Both sweep
// paths carry only the joined ARN's or Cloud Control identifier's own
// importID and the object's tags - never [scanType]'s own schema-typed
// resource - so unlike scanType's own [importIdentityFromResource]
// correction, this can only ever carry an identity forward unchanged, never
// recompose a different one.
func TestSweepBindType(t *testing.T) {
	declaredRouteTable := &declared{types: map[string]map[string]*declaredEntry{
		"aws_default_route_table": {
			`module.vpc.aws_default_route_table.default:0`: {},
		},
	}}
	empty := &declared{}

	cases := []struct {
		name                        string
		decl                        *declared
		markerType, typeName        string
		escaped                     string
		wantBindType                string
		wantSkip                    bool
	}{
		{
			name:         "same type - nothing to correct",
			decl:         empty,
			markerType:   "aws_route_table",
			typeName:     "aws_route_table",
			escaped:      `aws_route_table.public[0]`,
			wantBindType: "aws_route_table",
			wantSkip:     false,
		},
		{
			// The exact issue #394 shape: aws_default_route_table is
			// declared and was already visited, correctly, by its own
			// config-driven scanType pass (which alone can read the vpc_id
			// #332's recomposition needs) before this sweep ever ran. This
			// ARN-joined sighting under the generic "aws_route_table" name
			// is the SAME live object found a second time, not a second
			// object, so it is skipped rather than filed a second time
			// under an identity ([sameRatifiedIdentity] is false for this
			// pair) this sweep cannot itself verify.
			name:         "declared default_route_table companion already covered - skip",
			decl:         declaredRouteTable,
			markerType:   "aws_default_route_table",
			typeName:     "aws_route_table",
			escaped:      `module.vpc.aws_default_route_table.default:0`,
			wantBindType: "",
			wantSkip:     true,
		},
		{
			// Same companion pair, but nothing declares it anywhere - a
			// genuine orphan. sameRatifiedIdentity is false for this pair
			// (issue #332: aws_default_route_table imports by the VPC's
			// id, not the route table's own rtb-... id this sweep's
			// candidate carries), and this path never has the listed
			// object's own attributes to recompose one - only
			// scanType's own per-type list call does. Refuse rather than
			// guess.
			name:         "undeclared default_route_table companion, mismatched identity - refuse",
			decl:         empty,
			markerType:   "aws_default_route_table",
			typeName:     "aws_route_table",
			escaped:      `aws_default_route_table.default:0`,
			wantBindType: "aws_route_table",
			wantSkip:     false,
		},
		{
			// aws_default_security_group/aws_security_group DO agree about
			// the identity (both import by the object's own sg-... id, see
			// [TestSameRatifiedIdentity]), so nothing declared anywhere
			// still carries the candidate's own importID forward safely.
			name:         "undeclared default_security_group companion, same identity - carries forward",
			decl:         empty,
			markerType:   "aws_default_security_group",
			typeName:     "aws_security_group",
			escaped:      `aws_default_security_group.default:0`,
			wantBindType: "aws_default_security_group",
			wantSkip:     false,
		},
		{
			// The mutation boundary this fix must not widen: a marker
			// naming a type that is not typeName's #305 or #302 companion
			// at all - not even a default_* prefix pair - must still read
			// as a genuine cross-type mismatch, exactly as before this fix.
			name:         "genuinely unrelated type - still refuse",
			decl:         empty,
			markerType:   "aws_default_security_group",
			typeName:     "aws_route_table",
			escaped:      `aws_default_security_group.default:0`,
			wantBindType: "aws_route_table",
			wantSkip:     false,
		},
		{
			// Same boundary, the IAM side: aws_iam_role/aws_iam_role_policy
			// share nothing [iamServiceLinkedRoleSibling] or
			// [defaultAdopterSiblings] recognizes.
			name:         "unrelated iam pair - still refuse",
			decl:         empty,
			markerType:   "aws_iam_role",
			typeName:     "aws_iam_role_policy",
			escaped:      `aws_iam_role.this`,
			wantBindType: "aws_iam_role_policy",
			wantSkip:     false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotBindType, gotSkip := sweepBindType(c.decl, c.markerType, c.typeName, c.escaped)
			if gotBindType != c.wantBindType || gotSkip != c.wantSkip {
				t.Errorf("sweepBindType(%q, %q, %q) = (%q, %v), want (%q, %v)",
					c.markerType, c.typeName, c.escaped, gotBindType, gotSkip, c.wantBindType, c.wantSkip)
			}
		})
	}
}

// TestTaggingSweepDefaultRouteTableCompanionRoutesNative is issue #394's own
// reproduction, shrunk to a fixture: an estate-wide sweep with TaggingSweep
// set finds a route table carrying an aws_default_route_table marker (a
// #305 adopter whose identity - the VPC's own id, issue #332 - can only be
// recomposed from the LISTED object's own vpc_id attribute, which only a
// native per-type list call ever carries), and neither side of the pair is
// declared in this estate's configuration at all. Before this fix,
// aws_route_table went through the estate-wide tag sweep's ARN join, whose
// candidate never carries vpc_id, and the shared object was reported
// Malformed. [partitionSweepTypes] (issue #394) now routes aws_route_table
// through the native per-type sweep instead - the SAME [scanType] machinery
// [TestDiscoverDefaultRouteTableAliasIsNotMalformed] already proves correct
// for the plain (non-TaggingSweep) case - even though TaggingSweep is set,
// and this test proves that routing happens: a tag-sweep candidate for the
// very same live object is present too (a real GetResources call would
// return it right alongside), and it must be silently ignored rather than
// filed a second time under the wrong identity.
func TestTaggingSweepDefaultRouteTableCompanionRoutesNative(t *testing.T) {
	cloud := newFakeCloud()
	cloud.withAttr("aws_route_table", "vpc_id")
	// The object is returned by aws_route_table's OWN native list call -
	// exactly how the real bug reproduces, and exactly what
	// [TestDiscoverDefaultRouteTableAliasIsNotMalformed] already proves
	// [scanType] binds correctly - carrying a marker for the sibling type
	// aws_default_route_table.
	cloud.ownWithAttrs("aws_route_table", "rtb-shared-1", `aws_default_route_table.default`,
		map[string]string{"vpc_id": "vpc-shared-1"})

	// The same live object, ALSO surfaced by the estate-wide tag sweep's
	// GetResources call (a real API would return every tagged resource,
	// including ones a native per-type call also covers) and joined
	// generically to "aws_route_table" by ARN shape alone. Before this fix
	// this was the only sighting reaching discovery at all, since
	// aws_route_table was never otherwise declared.
	arn := "arn:aws:ec2:us-east-1:123456789012:route-table/rtb-shared-1"
	srv := &taggingServer{
		arns: []string{arn},
		tags: map[string]map[string]string{
			arn: {
				TagEstate:  estateName,
				TagAddress: `aws_default_route_table.default`,
			},
		},
	}
	server := srv.start(t)
	defer server.Close()

	// Neither side of the pair is declared, matching
	// [TestTaggingSweepDefaultSecurityGroupCompanionOrphanCarriesIDForward]'s
	// own orphan shape: [sweepTypes] excludes a type only because the
	// configuration declares it, and this proves the native-partition
	// question on its own terms, independent of the separate #293
	// marker-index fallback a DECLARED aws_default_route_table with no
	// list route of its own would additionally exercise.
	cfg := loadConfig(t, "testdata/default-adopter-sweep-orphan")
	res, diags := Discover(context.Background(), Request{
		Estate:       estateName,
		Config:       cfg,
		Resolutions:  resolveOrFail(t, cfg).All(),
		Provider:     cloud,
		Sweep:        true,
		Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: server.URL}),
		TaggingSweep: true,
		Roster:       taggingRoster(t, "aws_route_table", "AWS::EC2::RouteTable", true),
	})
	if diags.HasErrors() {
		t.Fatalf("the default_route_table/route_table companion pair was reported as an error:\n%s\n%s", res, renderDiags(diags))
	}
	if len(res.ProblemsOfKind(ProblemMalformedMarker)) != 0 {
		t.Fatalf("the tag sweep's own sighting of the natively-scanned object was reported malformed:\n%s", res)
	}
	if len(res.ProblemsOfKind(ProblemCollision)) != 0 {
		t.Fatalf("the tag sweep's redundant sighting was reported as a genuine collision rather than routed away:\n%s", res)
	}

	scan, ok := res.ScanFor("aws_route_table")
	if !ok || scan.Source != SourceProvider {
		t.Errorf("aws_route_table's scan = %+v, want it swept the native way (Source=provider), not via the tag sweep's ARN join", scan)
	}

	var found bool
	for _, o := range res.Orphans {
		if o.TypeName != "aws_default_route_table" {
			continue
		}
		found = true
		if o.ImportID != "vpc-shared-1" {
			t.Errorf("orphan's ImportID is %q, want vpc-shared-1 (the VPC id, issue #332) - not the route table's own rtb-... id", o.ImportID)
		}
	}
	if !found {
		t.Fatalf("the aliased object did not appear as an orphan at all:\n%s", res)
	}
}

// TestTaggingSweepGenuinelyUnrelatedTypeStillMalformed is the fix's mutation
// control at the real tag-sweep path: a live object's marker names a type
// genuinely unrelated to what the ARN join resolved it as - not any
// admitted companion pair - so [sweepBindType] must still refuse it exactly
// as it did before this fix. aws_security_group is used rather than
// aws_route_table deliberately: its own companion pair
// ([sameRatifiedIdentity] true) is one [partitionSweepTypes] leaves in the
// tag-sweep universe, so this proves the boundary at the actual
// [fileTaggingCandidate] call site a bug loosening [sweepBindType] would
// widen, not merely at the unit level [TestSweepBindType] already covers.
func TestTaggingSweepGenuinelyUnrelatedTypeStillMalformed(t *testing.T) {
	cloud := newFakeCloud()

	// The live object IS a security group (what the ARN join, and the real
	// AWS resource, say), but its marker names an unrelated
	// aws_default_route_table address - not aws_security_group's #305
	// sibling aws_default_security_group, and not an IAM service-linked-role
	// pair either.
	arn := "arn:aws:ec2:us-east-1:123456789012:security-group/sg-confused-1"
	srv := &taggingServer{
		arns: []string{arn},
		tags: map[string]map[string]string{
			arn: {
				TagEstate:  estateName,
				TagAddress: `aws_default_route_table.default`,
			},
		},
	}
	server := srv.start(t)
	defer server.Close()

	cfg := loadConfig(t, "testdata/default-adopter-sweep-orphan")
	res, diags := Discover(context.Background(), Request{
		Estate:       estateName,
		Config:       cfg,
		Resolutions:  resolveOrFail(t, cfg).All(),
		Provider:     cloud,
		Sweep:        true,
		Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: server.URL}),
		TaggingSweep: true,
		Roster:       taggingRoster(t, "aws_security_group", "AWS::EC2::SecurityGroup", true),
	})
	if !diags.HasErrors() {
		t.Fatalf("a genuinely cross-type marker arriving via the tag sweep produced no error:\n%s", res)
	}
	problems := res.ProblemsOfKind(ProblemMalformedMarker)
	if len(problems) != 1 {
		t.Fatalf("want exactly one malformed-marker problem, got %d:\n%s", len(problems), res)
	}
	for _, want := range []string{"aws_default_route_table", "aws_security_group"} {
		if !strings.Contains(problems[0].Detail, want) {
			t.Errorf("the malformed-marker detail does not name %q: %q", want, problems[0].Detail)
		}
	}
	if len(res.Orphans) != 0 {
		t.Errorf("a type-confused sweep marker was also classified as an orphan:\n%s", res)
	}
}

// TestTaggingSweepDefaultSecurityGroupCompanionOrphanCarriesIDForward covers
// [sweepBindType]'s other safe branch through the real tag-sweep path: a
// companion pair whose ratified rows DO agree about the import identity
// ([sameRatifiedIdentity] true, aws_default_security_group/
// aws_security_group) and that is declared NOWHERE in this estate's
// configuration - a genuine orphan, not a second sighting of a declared
// object. The tag sweep's own importID (already the object's own sg-... id
// under either name) carries forward unchanged, and the object is reported
// as an orphan under its marker's own type, not malformed.
func TestTaggingSweepDefaultSecurityGroupCompanionOrphanCarriesIDForward(t *testing.T) {
	cloud := newFakeCloud()

	arn := "arn:aws:ec2:us-east-1:123456789012:security-group/sg-shared-1"
	srv := &taggingServer{
		arns: []string{arn},
		tags: map[string]map[string]string{
			arn: {
				TagEstate:  estateName,
				TagAddress: `aws_default_security_group.default`,
			},
		},
	}
	server := srv.start(t)
	defer server.Close()

	cfg := loadConfig(t, "testdata/default-adopter-sweep-orphan")
	res, diags := Discover(context.Background(), Request{
		Estate:       estateName,
		Config:       cfg,
		Resolutions:  resolveOrFail(t, cfg).All(),
		Provider:     cloud,
		Sweep:        true,
		Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: server.URL}),
		TaggingSweep: true,
		Roster:       taggingRoster(t, "aws_security_group", "AWS::EC2::SecurityGroup", true),
	})
	if diags.HasErrors() {
		t.Fatalf("an undeclared default_security_group/security_group companion pair was reported as an error via the tag sweep:\n%s\n%s", res, renderDiags(diags))
	}
	if len(res.ProblemsOfKind(ProblemMalformedMarker)) != 0 {
		t.Fatalf("the companion pair was reported malformed:\n%s", res)
	}
	var found bool
	for _, o := range res.Orphans {
		if o.Normalized != `aws_default_security_group.default` {
			continue
		}
		found = true
		if o.TypeName != "aws_default_security_group" {
			t.Errorf("orphan's TypeName is %q, want aws_default_security_group", o.TypeName)
		}
		if o.ImportID != "sg-shared-1" {
			t.Errorf("orphan's ImportID is %q, want sg-shared-1 unchanged", o.ImportID)
		}
	}
	if !found {
		t.Fatalf("the aliased object did not appear as an orphan at all:\n%s", res)
	}
}
