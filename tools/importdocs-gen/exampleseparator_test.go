// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

// TestSeparatorFromProse covers the stated forms, and the misfire that
// made the context requirement necessary.
func TestSeparatorFromProse(t *testing.T) {
	for _, tc := range []struct {
		name    string
		section string
		want    string
		ok      bool
	}{
		{
			name:    "separated by a named character",
			section: "import X using the alias ID and the agent ID separated by a comma. For example:",
			want:    ",", ok: true,
		},
		{
			name:    "the transit gateway phrasing, which is not `separated by`",
			section: "import X using the EC2 Transit Gateway Route Table, an underscore, and the destination.",
			want:    "_", ok: true,
		},
		{
			name:    "an angle-bracketed format token",
			section: "import IPAMs using the `<cidr>_<ipam-pool-id>`. For example:",
			want:    "_", ok: true,
		},
		{
			// The loose version of this rule matched the bare word "pipes"
			// here and gave aws_pipes_pipe the separator "|". The page is
			// naming the service, not a join character.
			name:    "a service noun that happens to name a character",
			section: "import pipes using the `name`. For example:",
			ok:      false,
		},
		{
			name:    "a format token whose joins disagree",
			section: "import X using `<a>_<b>/<c>`.",
			ok:      false,
		},
		{
			name:    "no statement at all",
			section: "import X using the `name`.",
			ok:      false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := separatorFromProse(tc.section)
			if ok != tc.ok || (tc.ok && got != tc.want) {
				t.Errorf("separatorFromProse() = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestSeparatorFromExample covers the fallback and, more importantly, what
// it refuses.
func TestSeparatorFromExample(t *testing.T) {
	for _, tc := range []struct {
		name    string
		example string
		want    string
		ok      bool
	}{
		{
			// splitSegments declined this because segmentRe wants a leading
			// letter and these segments are hex and an account number. The
			// character was never ambiguous.
			name: "hex and numeric segments", example: "00b00fd5aecc0ab60a708659477e9617:123456789012",
			want: ":", ok: true,
		},
		{
			// The doc explains this shape in prose: "an empty base_path or,
			// in other words, a root path". A rule demanding non-empty
			// segments refuses the one example the page goes out of its way
			// to explain.
			name: "a documented trailing separator", example: "example.com/",
			want: "/", ok: true,
		},
		{
			name: "three segments", example: "us-east-1_vG78M4goG,example-group,example-user",
			want: ",", ok: true,
		},
		{
			// ':' and '/' both occur, so this example does not decide. An
			// ARN must never be split.
			name: "an ARN", example: "arn:aws:sagemaker:us-west-2:123456789012:user-profile/domain-id/profile-name",
			ok: false,
		},
		{
			// '|' is the real separator and ':' belongs to a URL scheme.
			// Nothing in the string says which, so this declines.
			name: "a URL inside a segment", example: "us-west-2_abc123|https://example.com",
			ok: false,
		},
		{
			// Underscore is never selected: a Cognito pool ID is spelled
			// us-west-2_abc123 and is ONE value, and nothing here separates
			// that from two IDs joined by one.
			name: "underscore alone", example: "tgw-rtb-12345678_tgw-attach-87654321",
			ok: false,
		},
		{name: "no candidate at all", example: "my-pipe", ok: false},
		{name: "empty", example: "", ok: false},
		{name: "a leading separator is not a join", example: "/only-one", ok: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := separatorFromExample(tc.example)
			if ok != tc.ok || (tc.ok && got != tc.want) {
				t.Errorf("separatorFromExample(%q) = (%q, %v), want (%q, %v)", tc.example, got, ok, tc.want, tc.ok)
			}
		})
	}
}
