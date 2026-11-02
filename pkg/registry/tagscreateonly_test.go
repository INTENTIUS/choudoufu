// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package registry

import "testing"

// TestTagsCreateOnly pins GitHub issue #2287's create-only rule over the
// embedded artifacts: tag_updatable false, or Tags among the
// create_only_properties, on a taggable declared row.
func TestTagsCreateOnly(t *testing.T) {
	r, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		cfn              string
		createOnly, know bool
	}{
		{"AWS::EC2::KeyPair", true, true},
		{"AWS::AppRunner::Service", true, true},
		{"AWS::S3::Bucket", false, true},
		{"AWS::SQS::Queue", false, true},
		// Not taggable: the registry has nothing to say about its tags.
		{"AWS::ACMPCA::Certificate", false, false},
		{"AWS::Nothing::Here", false, false},
	} {
		got, known := r.TagsCreateOnly(tc.cfn)
		if got != tc.createOnly || known != tc.know {
			t.Errorf("TagsCreateOnly(%s) = %v, %v; want %v, %v", tc.cfn, got, known, tc.createOnly, tc.know)
		}
	}

	n := 0
	for cfn := range r.tagsCreateOnly {
		if c, _ := r.TagsCreateOnly(cfn); c {
			n++
		}
	}
	// The design on #2284 counted 67 at the pinned registry. A regeneration
	// that moves this number is worth reading before it is accepted.
	if n != 67 {
		t.Errorf("%d create-only-tag types in the embedded registry, want 67", n)
	}
}
