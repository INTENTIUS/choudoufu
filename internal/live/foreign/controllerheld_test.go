// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package foreign

import (
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/discovery"
)

// TestControllerHeldOrphanIsNotOfferedARename (GitHub issue #1606): a
// marker rewrite is an offer to this estate, and a controller-held
// resource is never offered. The same orphan without the controller's tags
// is TestRenameCandidate's one-to-one pairing.
func TestControllerHeldOrphanIsNotOfferedARename(t *testing.T) {
	o := orphan("aws_subnet", "subnet-xyz", "private-a", "aws_subnet.this:a")
	o.Tags = map[string]string{
		"crossplane-kind": "subnet.ec2.aws.upbound.io",
		"crossplane-name": "private-a",
	}
	res := classifyFixture(t, discovery.Result{Verdicts: discovery.Verdicts{Orphans: []discovery.OwnedResource{o}}, Report: discovery.Report{Scans: []discovery.TypeScan{scan("aws_subnet", 1)}, Unbound: []addrs.AbsResourceInstance{mustAddr(t, `aws_subnet.this["c"]`)}}})

	if len(res.Renames) != 0 || len(res.Ambiguous) != 0 {
		t.Errorf("a controller-held orphan was offered as a rename:\n%s", res)
	}
}
