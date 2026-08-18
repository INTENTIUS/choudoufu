// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"testing"
)

// TestOmitIfAbsentRoute53Record proves [Component.OmitIfAbsent]'s
// set_identifier segment (#286) against the provider's own two documented
// import forms for aws_route53_record (aws 6.59.0's Import section):
//
//	Z4KAPRWWNC7JR_dev.example.com_NS
//	Z4KAPRWWNC7JR_dev.example.com_NS_dev
//
// Before this, the ratified row named only zone_id, name and type, so two
// weighted (or latency, or failover) records sharing a zone, name and type
// but differing only in set_identifier rendered the identical ImportID and
// collided under the duplicate-identity guard. Both directions of getting
// an optional component wrong are wrong markers - a spurious trailing
// underscore on a record with no set identifier, or a dropped one on a
// record that has it - so both shapes are asserted here, string for
// string, against the documented examples.
func TestOmitIfAbsentRoute53Record(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "route53-record-set-identifier"), nil)
	result, diags := Resolve(context.Background(), cfg)
	if len(diags) > 0 {
		for _, d := range diags {
			t.Logf("diagnostic: %s: %s", d.Description().Summary, d.Description().Detail)
		}
		t.Fatalf("unexpected diagnostics (%d)", len(diags))
	}

	unweighted := resolutionAt(t, result, "aws_route53_record.unweighted")
	if unweighted.Class != ClassConcrete {
		t.Fatalf("unweighted resolved %s, want concrete", unweighted.Class)
	}
	if want := "Z4KAPRWWNC7JR_dev.example.com_NS"; unweighted.ImportID != want {
		t.Errorf("unweighted rendered %q, want %q (the provider's own no-set-identifier import example)", unweighted.ImportID, want)
	}
	if got, ok := unweighted.IdentityValues["set_identifier"]; ok {
		t.Errorf("unweighted carries IdentityValues[set_identifier] = %q; an omitted set_identifier must supply no identity value at all, not an empty one", got)
	}

	weighted := resolutionAt(t, result, "aws_route53_record.weighted")
	if weighted.Class != ClassConcrete {
		t.Fatalf("weighted resolved %s, want concrete", weighted.Class)
	}
	if want := "Z4KAPRWWNC7JR_dev.example.com_NS_dev"; weighted.ImportID != want {
		t.Errorf("weighted rendered %q, want %q (the provider's own set-identifier import example)", weighted.ImportID, want)
	}
	if want := "dev"; weighted.IdentityValues["set_identifier"] != want {
		t.Errorf("weighted's set_identifier identity value = %q, want %q (unprefixed - the underscore belongs to the ImportID string only)", weighted.IdentityValues["set_identifier"], want)
	}

	if unweighted.ImportID == weighted.ImportID {
		t.Fatalf("both instances rendered the same ImportID (%q); that is the exact collision this field exists to prevent", unweighted.ImportID)
	}
}

// TestOmitIfAbsentRoute53ZoneAssociation proves [Component.OmitIfAbsent]'s
// vpc_region segment (#286) against the provider's own two documented
// import forms for aws_route53_zone_association (aws 6.59.0's Import
// section):
//
//	Z123456ABCDEFG:vpc-12345678
//	Z123456ABCDEFG:vpc-12345678:us-east-2
//
// Before this, the ratified row named only zone_id and vpc_id, so a
// cross-region association collided with a same-region association of the
// identical VPC id in the identical zone.
func TestOmitIfAbsentRoute53ZoneAssociation(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "route53-zone-association-vpc-region"), nil)
	result, diags := Resolve(context.Background(), cfg)
	if len(diags) > 0 {
		for _, d := range diags {
			t.Logf("diagnostic: %s: %s", d.Description().Summary, d.Description().Detail)
		}
		t.Fatalf("unexpected diagnostics (%d)", len(diags))
	}

	sameRegion := resolutionAt(t, result, "aws_route53_zone_association.same_region")
	if sameRegion.Class != ClassConcrete {
		t.Fatalf("same_region resolved %s, want concrete", sameRegion.Class)
	}
	if want := "Z123456ABCDEFG:vpc-12345678"; sameRegion.ImportID != want {
		t.Errorf("same_region rendered %q, want %q (the provider's own same-region import example)", sameRegion.ImportID, want)
	}
	if got, ok := sameRegion.IdentityValues["vpc_region"]; ok {
		t.Errorf("same_region carries IdentityValues[vpc_region] = %q; an omitted vpc_region must supply no identity value at all, not an empty one", got)
	}

	crossRegion := resolutionAt(t, result, "aws_route53_zone_association.cross_region")
	if crossRegion.Class != ClassConcrete {
		t.Fatalf("cross_region resolved %s, want concrete", crossRegion.Class)
	}
	if want := "Z123456ABCDEFG:vpc-12345678:us-east-2"; crossRegion.ImportID != want {
		t.Errorf("cross_region rendered %q, want %q (the provider's own cross-region import example)", crossRegion.ImportID, want)
	}
	if want := "us-east-2"; crossRegion.IdentityValues["vpc_region"] != want {
		t.Errorf("cross_region's vpc_region identity value = %q, want %q", crossRegion.IdentityValues["vpc_region"], want)
	}

	if sameRegion.ImportID == crossRegion.ImportID {
		t.Fatalf("both instances rendered the same ImportID (%q); that is the exact collision this field exists to prevent", sameRegion.ImportID)
	}
}

// TestOmitIfAbsentTargetGroupAttachment proves [Component.OmitIfAbsent]'s
// availability_zone and quic_server_id segments (#286) for
// aws_lb_target_group_attachment and its documented alias
// aws_alb_target_group_attachment. The provider (aws 6.59.0) documents the
// comma-joined form as built from "target_group_arn, target_id, and
// optionally port and availability_zone separated by commas", with a
// literal example that stops at port:
//
//	arn:aws:elasticloadbalancing:us-west-2:123456789012:targetgroup/my-tg/abc123,i-0123456789abcdef0,8080
//
// base below reproduces that string exactly. Neither a combined example
// carrying availability_zone nor one carrying quic_server_id appears on
// the page - the Import section's prose states the comma rule for
// availability_zone without a worked string, and quic_server_id appears
// only in the Identity Schema and in the unrelated "Target using QUIC"
// configuration example (which supplies quic_server_id = "0x1a2b3c4d5e6f7a8b"
// but no import ID). with_az and with_quic apply the documented comma rule
// to those two documented-but-not-jointly-exampled optional arguments, and
// are asserted as constructed strings rather than quoted ones - see the
// testdata file's own comment.
//
// A target registered under two availability zones - or re-registered with
// a different QUIC server id, which the provider's own prose requires to
// be unique per listener - previously rendered the identical ImportID and
// collided under the duplicate-identity guard.
func TestOmitIfAbsentTargetGroupAttachment(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "target-group-attachment-optional"), nil)
	result, diags := Resolve(context.Background(), cfg)
	if len(diags) > 0 {
		for _, d := range diags {
			t.Logf("diagnostic: %s: %s", d.Description().Summary, d.Description().Detail)
		}
		t.Fatalf("unexpected diagnostics (%d)", len(diags))
	}

	base := resolutionAt(t, result, "aws_lb_target_group_attachment.base")
	if base.Class != ClassConcrete {
		t.Fatalf("base resolved %s, want concrete", base.Class)
	}
	if want := "arn:aws:elasticloadbalancing:us-west-2:123456789012:targetgroup/my-tg/abc123,i-0123456789abcdef0,8080"; base.ImportID != want {
		t.Errorf("base rendered %q, want %q (the provider's own documented import example)", base.ImportID, want)
	}
	if _, ok := base.IdentityValues["availability_zone"]; ok {
		t.Errorf("base carries IdentityValues[availability_zone]; an omitted availability_zone must supply no identity value at all")
	}
	if _, ok := base.IdentityValues["quic_server_id"]; ok {
		t.Errorf("base carries IdentityValues[quic_server_id]; an omitted quic_server_id must supply no identity value at all")
	}

	withAZ := resolutionAt(t, result, "aws_lb_target_group_attachment.with_az")
	if withAZ.Class != ClassConcrete {
		t.Fatalf("with_az resolved %s, want concrete", withAZ.Class)
	}
	if want := "arn:aws:elasticloadbalancing:us-west-2:123456789012:targetgroup/my-tg/abc123,i-0123456789abcdef0,8080,us-west-2a"; withAZ.ImportID != want {
		t.Errorf("with_az rendered %q, want %q (base example plus the documented ',availability_zone' comma rule)", withAZ.ImportID, want)
	}
	if want := "us-west-2a"; withAZ.IdentityValues["availability_zone"] != want {
		t.Errorf("with_az's availability_zone identity value = %q, want %q", withAZ.IdentityValues["availability_zone"], want)
	}

	withQuic := resolutionAt(t, result, "aws_lb_target_group_attachment.with_quic")
	if withQuic.Class != ClassConcrete {
		t.Fatalf("with_quic resolved %s, want concrete", withQuic.Class)
	}
	if want := "arn:aws:elasticloadbalancing:us-west-2:123456789012:targetgroup/my-tg/abc123,i-0123456789abcdef0,8080,0x1a2b3c4d5e6f7a8b"; withQuic.ImportID != want {
		t.Errorf("with_quic rendered %q, want %q (base example plus the documented ',quic_server_id' comma rule, using the page's own QUIC example value)", withQuic.ImportID, want)
	}
	if want := "0x1a2b3c4d5e6f7a8b"; withQuic.IdentityValues["quic_server_id"] != want {
		t.Errorf("with_quic's quic_server_id identity value = %q, want %q", withQuic.IdentityValues["quic_server_id"], want)
	}

	if base.ImportID == withAZ.ImportID || base.ImportID == withQuic.ImportID || withAZ.ImportID == withQuic.ImportID {
		t.Fatalf("two of base (%q), with_az (%q) and with_quic (%q) rendered the same ImportID; that is the exact collision this field exists to prevent", base.ImportID, withAZ.ImportID, withQuic.ImportID)
	}

	// aws_alb_target_group_attachment is the documented alias - its
	// ratified row mirrors aws_lb_target_group_attachment's Components
	// verbatim, so the base shape must render identically.
	aliasBase := resolutionAt(t, result, "aws_alb_target_group_attachment.base")
	if aliasBase.Class != ClassConcrete {
		t.Fatalf("aliasBase resolved %s, want concrete", aliasBase.Class)
	}
	if want := "arn:aws:elasticloadbalancing:us-west-2:123456789012:targetgroup/my-tg/abc123,i-0123456789abcdef0,8080"; aliasBase.ImportID != want {
		t.Errorf("aliasBase rendered %q, want %q", aliasBase.ImportID, want)
	}
}
