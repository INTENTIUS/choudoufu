// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/dataread"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// plannedCertificate is what a provider plan of a new certificate returns
// for the part the record reads: domain_validation_options stays unknown
// until ACM issues the certificate.
func plannedCertificate() map[string]cty.Value {
	dvo := cty.Object(map[string]cty.Type{
		"domain_name":           cty.String,
		"resource_record_name":  cty.String,
		"resource_record_type":  cty.String,
		"resource_record_value": cty.String,
	})
	return map[string]cty.Value{
		"aws_acm_certificate.this[0]": cty.ObjectVal(map[string]cty.Value{
			"domain_name":               cty.StringVal("example.com"),
			"domain_validation_options": cty.UnknownVal(cty.Set(dvo)),
		}),
	}
}

// TestPendingDataReadLetsASiblingApplyClassify is GitHub issue #2297's
// identity half. With the certificate's planned values in hand, the
// record's name and type wait on the certificate's apply, and its zone_id
// waits on a data read. live-plan performs the read and classifies the
// record as needing discovery. The offline check performs no read, so it
// resolves with identity.Context.PendingDataReads: the zone_id reference no
// longer counts as a broken argument, the name and type refusals are
// withdrawn, and the read is still reported as the eligible read.
//
// The control resolves the same configuration with PendingDataReads false,
// which is live-plan's setting, and must still refuse name and type.
func TestPendingDataReadLetsASiblingApplyClassify(t *testing.T) {
	load := Load(t.Context(), "testdata/acm-route53-pending-read-2297")
	if load.Config == nil {
		t.Fatalf("fixture did not load: %s", load.Diags.Error())
	}

	_, controlDiags := identity.ResolveWith(t.Context(), load.Config, identity.Context{ManagedResults: plannedCertificate()})
	if !hasSummary(controlDiags, identity.SummaryNonStaticIdentityArgument) {
		t.Fatalf("control: with PendingDataReads false the record's name and type must still refuse; got %v", controlDiags.Err())
	}

	got := Analyze(t.Context(), load.Config, Context{ManagedResults: plannedCertificate()})
	var read bool
	for _, f := range got.Findings {
		if f.ID == identity.SummaryNonStaticIdentityArgument {
			t.Errorf("the record's name and type still refuse offline: %+v", f.Sites)
		}
		if f.EligibleRead() {
			read = true
		}
	}
	if !read {
		t.Errorf("the zone_id read is no longer reported as %q; findings: %v", dataread.SummaryEligibleRead, findingIDs(got))
	}
	var classified bool
	for _, res := range got.Identities {
		if res.Addr.String() == "aws_route53_record.validation[0]" && res.Class == identity.ClassNeedsDiscovery {
			classified = true
		}
	}
	if !classified {
		t.Errorf("aws_route53_record.validation[0] is not classified needs-discovery; identities: %v", classesOf(got))
	}
}
