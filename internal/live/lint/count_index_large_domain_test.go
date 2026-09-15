// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import "testing"

// TestCountIndexDomainDecidesAboveTheOldBound is issue #1076's guard.
//
// countIndexDomainMax used to be 256, and the reason its own comment gave
// was cost: "the comparison is pairwise, so the work grows as the square of
// the count". That is a property of the implementation, not of the
// question. Checking whether a list of values contains a duplicate does not
// need every pair compared; it needs each value looked up once. With the
// comparison linear the bound has no reason to sit where a real estate
// trips over it.
//
// A real one did. tools/terralith-gen declares count = 2 * SCALE, so every
// size from scale 129 up carried a count over 256, and choudoufu refused to
// plan an estate it plans perfectly well at scale 128 - which is exactly
// where the 10,000-resource scale run died.
//
// Both directions, because raising a bound is only safe if the thing it
// was gating still gives the right answer on both:
//
//   - the injective shape at count 300 must be ADMITTED, where before the
//     domain declined to look and the syntactic rule refused format();
//   - the colliding shape at the same count must still be REFUSED, and it
//     is the half that would go quietly wrong if a larger domain were made
//     to work by weakening what it checks.
func TestCountIndexDomainDecidesAboveTheOldBound(t *testing.T) {
	const oldBound = 256
	if countIndexDomainMax <= oldBound {
		t.Fatalf("countIndexDomainMax = %d, want more than the old %d - this guard exists because a real estate's count = 2 * SCALE crossed it (issue #1076)", countIndexDomainMax, oldBound)
	}

	cfg := loadConfigDir(t, "testdata/count-index-large-domain")
	refused := refusedResources(t, cfg)

	if refused["aws_route53_record.wide_distinct"] {
		t.Errorf("aws_route53_record.wide_distinct was refused at count 300; format is injective on 0..299, so the domain must decide it rather than decline and let the syntactic rule refuse every function call")
	}
	if !refused["aws_route53_record.wide_collides"] {
		t.Errorf("aws_route53_record.wide_collides was ADMITTED at count 300; count.index %% 3 collides on 300 indices, and admitting it would mean two configuration addresses claiming one cloud object - the worst outcome this tool has")
	}
}
