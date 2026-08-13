// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"sort"
	"testing"

	residue "github.com/intentius/choudoufu/live"
)

// TestAdmittedTypesTagVerbCoverage is issue #52's drift guard for the
// botocore side: every admittedTypesV0 entry that maps to a CFN type at all
// must resolve to a row in live/tag-verbs.json - a known verb, or an
// honestly-named "unknown" (ambiguous, none, or not composable) - rather
// than silently falling off the artifact's edge because live/mapping.json
// gained a new mapped service since tools/tagverbs-gen last ran.
//
// A type with no CFN mapping at all (the six composite/property-child
// admitted types - aws_iam_role_policy_attachment,
// aws_lb_target_group_attachment, the four S3 bucket children) is out of
// this test's scope: [residue.TagVerbForType] reports ok=false for them
// because there is no CFN service to look a verb up for in the first place,
// which is not the drift this test guards against.
func TestAdmittedTypesTagVerbCoverage(t *testing.T) {
	types := make([]string, 0, len(admittedTypesV0))
	for tfType := range admittedTypesV0 {
		types = append(types, tfType)
	}
	sort.Strings(types)

	checked := 0
	for _, tfType := range types {
		tv, mapped := residue.TagVerbForType(tfType)
		if !mapped {
			continue
		}
		checked++
		if !tv.Known {
			t.Errorf("%s maps to CFN service %s, which live/tag-verbs.json carries no row for at all; regenerate via `go run ./tools/tagverbs-gen`", tfType, tv.Service)
		}
	}
	if checked == 0 {
		t.Fatal("no admitted type resolved to a CFN service at all; this test is not exercising anything")
	}
}

// TestAdmittedTypesTagVerbCoverage_KnownServicesReport pins today's actual
// split for the currently admitted set, so a change to either
// admittedTypesV0 or live/tag-verbs.json's classification shows up as a
// failing assertion here rather than only as a silent count drift: which
// services resolve a composable verb, which are known-but-not-composable,
// and which are ambiguous or none.
func TestAdmittedTypesTagVerbCoverage_KnownServicesReport(t *testing.T) {
	wantComposable := map[string]bool{
		"aws_vpc": true, "aws_subnet": true, "aws_security_group": true,
		"aws_route_table": true, "aws_internet_gateway": true, "aws_eip": true,
		"aws_vpc_security_group_ingress_rule": true, "aws_vpc_security_group_egress_rule": true,
		"aws_launch_template": true, "aws_ebs_volume": true, "aws_route": true,
		"aws_route_table_association": true,
	}
	for tfType := range wantComposable {
		tv, mapped := residue.TagVerbForType(tfType)
		if !mapped || !tv.Known || !tv.Composable {
			t.Errorf("%s: TagVerbForType = %+v (mapped=%v), want a known composable EC2 verb", tfType, tv, mapped)
		}
	}

	// A representative type from each non-composable outcome the artifact
	// records honestly rather than a guess: ambiguous services (IAM, ACM),
	// a known-but-two-part-identity service (Route53), and a service with
	// no tagging write at all is not exercised here since none of the
	// admitted types map to S3's own untaggable bucket-child shapes in a
	// way this artifact would call "known but S3-none" - aws_s3_bucket
	// itself does map to S3, whose row is "none".
	wantNotComposable := []string{"aws_iam_role", "aws_acm_certificate", "aws_route53_zone", "aws_s3_bucket"}
	for _, tfType := range wantNotComposable {
		tv, mapped := residue.TagVerbForType(tfType)
		if !mapped || !tv.Known {
			t.Errorf("%s: TagVerbForType = %+v (mapped=%v), want Known", tfType, tv, mapped)
			continue
		}
		if tv.Composable {
			t.Errorf("%s: TagVerbForType = %+v, want not composable", tfType, tv)
		}
	}
}
