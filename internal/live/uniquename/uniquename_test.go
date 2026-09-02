// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package uniquename

import "testing"

// TestAssertedOnRealDescriptions runs the predicate over descriptions copied
// verbatim out of the CloudFormation registry schemas and the hashicorp/aws
// 6.59.0 documentation. Every negative case here is a real text that a naive
// search for the word "unique" reads as a guarantee.
func TestAssertedOnRealDescriptions(t *testing.T) {
	cases := []struct {
		name string
		desc string
		want bool
		why  string
	}{
		// Positive: the shapes the mechanism exists for.
		{
			name: "cfn cache policy",
			desc: "A unique name to identify the cache policy.",
			want: true,
			why:  "AWS::CloudFront::CachePolicy, definitions.CachePolicyConfig.Name",
		},
		{
			name: "provider cache policy",
			desc: "* `name` - (Required) Unique name used to identify the cache policy.",
			want: true,
			why:  "hashicorp/aws cloudfront_cache_policy.html.markdown:49",
		},
		{
			name: "cfn origin request policy",
			desc: "A unique name to identify the origin request policy.",
			want: true,
		},
		{
			name: "cfn response headers policy, claim on the second line",
			desc: "A name to identify the response headers policy.\n The name must be unique for response headers policies in this AWS-account.",
			want: true,
			why:  "the first sentence makes no claim; the second does, and a line break separates them",
		},
		{
			name: "cfn cidr collection",
			desc: "A unique name for the CIDR collection.",
			want: true,
		},
		{
			name: "scoped to customer and region, which a listing covers",
			desc: "The unique name that is associated with the InfluxDB cluster. Cluster names must be unique per customer and per region.",
			want: true,
		},

		// The permanent negative case (#272).
		{
			name: "cfn origin access control",
			desc: "A name to identify the origin access control.",
			want: false,
			why:  "AWS::CloudFront::OriginAccessControl says nothing about uniqueness",
		},
		{
			name: "provider origin access control",
			desc: "* `name` - (Required) A name that identifies the Origin Access Control.",
			want: false,
			why:  "hashicorp/aws cloudfront_origin_access_control.html.markdown:33",
		},

		// Negation.
		{
			name: "gamelift alias denies uniqueness",
			desc: "A descriptive label that is associated with an alias. Alias names do not need to be unique.",
			want: false,
		},
		{
			name: "ivs playback key pair denies uniqueness",
			desc: "An arbitrary string (a nickname) assigned to a playback key pair that helps the customer identify that resource. The value does not need to be unique.",
			want: false,
		},

		// Platform-generated. The first case is the one that matters: it
		// contains the exact words "unique name", it appears verbatim at the
		// end of 54 argument bullets in hashicorp/aws 6.59.0, and it is the
		// only thing standing between this predicate and aws_iam_role,
		// aws_cloudwatch_log_group and 52 others. Deleting generatedRe leaves
		// every other case in this table green.
		{
			name: "terraform assigns a random, unique name when omitted",
			desc: "* `name` - (Optional, Forces new resource) The name of the log group. If omitted, Terraform will assign a random, unique name.",
			want: false,
			why:  "the unique name is one Terraform mints, not one the configuration states",
		},
		{
			name: "name_prefix sibling of the same shape",
			desc: "* `name` - (Optional) The name of the policy. If omitted, Terraform will assign a random, unique name.",
			want: false,
		},
		{
			name: "apigateway api key, unique physical ID",
			desc: "A name for the API key. If you don't specify a name, CFN generates a unique physical ID and uses that ID for the API key name.",
			want: false,
			why:  "the unique value is one the client did not write",
		},
		{
			name: "s3 access point, unique generated ID",
			desc: "The name you want to assign to this Access Point. If you don't specify a name, AWS CloudFormation generates a unique ID and uses that ID for the access point name.",
			want: false,
		},

		// Narrower scope than a listing.
		{
			name: "route53 key signing key, scoped to the hosted zone",
			desc: "An alphanumeric string used to identify a key signing key (KSK). Name must be unique for each key signing key in the same hosted zone.",
			want: false,
			why:  "two hosted zones may each hold a KSK of this name, and a listing sees both",
		},
		{
			name: "bedrockagentcore policy, scoped to the policy engine",
			desc: "The customer-assigned immutable name for the policy. Must be unique within the policy engine.",
			want: false,
		},
		{
			name: "mediaconnect flow output, scoped to the flow",
			desc: "The name of the output. This value must be unique within the current flow.",
			want: false,
		},
		{
			name: "cases template, scoped per domain",
			desc: "A name for the template. It must be unique per domain.",
			want: false,
		},
		{
			name: "sagemaker model package nested spec, scoped to a list",
			desc: "A unique name to identify the additional inference specification. The name must be unique within the list of your additional inference specifications for a particular model package.",
			want: true,
			why: "the FIRST sentence states the claim flatly and clears every guard, so the " +
				"predicate says yes; what keeps this type out of the roster is the crossing, " +
				"not this predicate - see live/unique_name_evidence_test.go",
		},

		// Silence.
		{name: "empty", desc: "", want: false},
		{name: "no claim at all", desc: "The name of the thing.", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Asserted(tc.desc); got != tc.want {
				t.Errorf("Asserted(%q) = %v, want %v (%s)", tc.desc, got, tc.want, tc.why)
			}
		})
	}
}

// TestNegationGuardIsUnreachedAtThePin records something the table above
// cannot show: negateRe never fires, because assertRe refuses every denial
// one line earlier. A guard that can see nothing is a guard whose doc comment
// is making a claim about protection it is not providing, and this repository
// has shipped one of those before.
//
// The guard is kept rather than deleted, and this test is why: the likely
// next change here is widening assertRe to reach a phrasing it misses today,
// and the first widening that also reaches a denial should turn this test red
// - at which point negateRe starts earning its place and the constant below
// becomes true.
func TestNegationGuardIsUnreachedAtThePin(t *testing.T) {
	denials := []string{
		"A descriptive label that is associated with an alias. Alias names do not need to be unique.",
		"An arbitrary string assigned to a playback key pair. The value does not need to be unique.",
		"The name of the room. The value does not need to be unique.",
	}
	for _, d := range denials {
		reached := false
		for _, s := range sentences(d) {
			if assertRe.MatchString(s) && negateRe.MatchString(s) {
				reached = true
			}
		}
		if reached {
			t.Errorf("negateRe now fires on %q; review this: assertRe has widened to match a denial, "+
				"so negateRe is load-bearing and this test should be replaced by one asserting it catches the case", d)
		}
		if Asserted(d) {
			t.Errorf("Asserted(%q) = true; a denial was read as a guarantee", d)
		}
	}
}

// TestNarrowerThanListing pins the scope guard on its own, so a change that
// widens listingScopes shows up here rather than only as a roster drift.
func TestNarrowerThanListing(t *testing.T) {
	narrow := []string{
		"must be unique within the domain",
		"must be unique in the same hosted zone",
		"must be unique per domain",
		"must be unique within each flow",
	}
	wide := []string{
		"must be unique",
		"must be unique within your AWS account",
		"must be unique per customer and per region",
		"must be unique within each Region",
	}
	for _, s := range narrow {
		if !narrowerThanListing(s) {
			t.Errorf("narrowerThanListing(%q) = false, want true", s)
		}
	}
	for _, s := range wide {
		if narrowerThanListing(s) {
			t.Errorf("narrowerThanListing(%q) = true, want false", s)
		}
	}
}
