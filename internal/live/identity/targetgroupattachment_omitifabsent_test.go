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

// TestTargetGroupAttachmentPortOmitIfAbsent is issue #399's implementation
// unit: the maintainer's 2026-08-24 ruling, verified against botocore's
// elbv2 2015-12-01 model (TargetDescription.Port and
// CreateTargetGroupInput.Port are both documented as not applying to a
// Lambda-type target; RegisterTargets' same-target-different-port
// allowance is scoped to EC2 instances and IP addresses only), makes
// aws_lb_target_group_attachment's port component [Component.OmitIfAbsent]
// - the same mechanism availability_zone and quic_server_id on this row
// already use, never a type-specific branch.
//
// testdata/target-group-attachment-lambda-port mirrors
// terraform-aws-modules/terraform-aws-alb's own
// local.lambda_target_groups shape (corpus-alb-complete's real estate,
// gauntlet issue #397/#399): port is WRITTEN but conditionally null for a
// lambda target, not merely absent from the block, so this exercises the
// "component present, evaluates to a clean null" redirect
// (resolve.go:1611's onlyNullIdentityArgument check) and not only the
// syntactic-absence branch omit-if-absent_test.go already covers for
// aws_lambda_permission's qualifier.
func TestTargetGroupAttachmentPortOmitIfAbsent(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "target-group-attachment-lambda-port"), nil)
	result, diags := Resolve(context.Background(), cfg)
	if len(diags) > 0 {
		for _, d := range diags {
			t.Logf("diagnostic: %s: %s", d.Description().Summary, d.Description().Detail)
		}
		t.Fatalf("unexpected diagnostics (%d)", len(diags))
	}

	// The defect this unit fixes, closed: a lambda-target attachment whose
	// port evaluates to null renders identity from target_group_arn and
	// target_id alone, exactly the two-field form
	// terraform-aws-modules/terraform-aws-alb's own real lambda attachments
	// resolve to against real AWS (no port in real life either -
	// botocore's own model, quoted above).
	lambda := resolutionAt(t, result, "aws_lb_target_group_attachment.lambda")
	if lambda.Class != ClassConcrete {
		t.Fatalf("lambda resolved %s, want concrete - a null port must omit, not refuse", lambda.Class)
	}
	wantLambdaID := "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/lambda-tg/abc123,arn:aws:lambda:us-east-1:123456789012:function:my-function"
	if lambda.ImportID != wantLambdaID {
		t.Errorf("lambda rendered %q, want %q - the two-field form with no port segment and no dangling separator", lambda.ImportID, wantLambdaID)
	}
	if got, ok := lambda.IdentityValues["port"]; ok {
		t.Errorf("lambda carries IdentityValues[port] = %q; an omitted port must supply no identity value at all, not an empty one", got)
	}

	// The mutation boundary this fix must not cost: an ordinary
	// instance-target attachment's port IS present and non-null, and must
	// keep rendering the three-field form byte-identical to before this
	// ruling - dropping the disambiguator here would silently collide two
	// attachments of the same target at different ports, the exact
	// wrong-marker shape HANDOFF's safety rule forbids.
	instance := resolutionAt(t, result, "aws_lb_target_group_attachment.instance")
	if instance.Class != ClassConcrete {
		t.Fatalf("instance resolved %s, want concrete", instance.Class)
	}
	wantInstanceID := "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/inst-tg/def456,i-0123456789abcdef0,80"
	if instance.ImportID != wantInstanceID {
		t.Errorf("instance rendered %q, want %q - byte-identical to the pre-ruling three-field form, port included", instance.ImportID, wantInstanceID)
	}
	if got := instance.IdentityValues["port"]; got != "80" {
		t.Errorf("instance's port identity value = %q, want %q - a present port must still ride along", got, "80")
	}
}

// TestTargetGroupAttachmentPortOmitIfAbsentMutationCheck proves the test
// above is load-bearing by reverting exactly the flag #399 ratified and
// confirming the lambda instance refuses again with "Null identity
// argument" - the identical wrong refusal corpus-alb-complete's real
// estate raised on both of its lambda-target attachments before this row
// change (gauntlet issue #397's own recorded detail). DefaultTable is
// restored via t.Cleanup so no other test in this package observes the
// mutation.
func TestTargetGroupAttachmentPortOmitIfAbsentMutationCheck(t *testing.T) {
	original, ok := DefaultTable["aws_lb_target_group_attachment"]
	if !ok {
		t.Fatal("aws_lb_target_group_attachment is not in DefaultTable")
	}
	reverted := original
	reverted.Components = append([]Component(nil), original.Components...)
	portIdx := -1
	for i, c := range reverted.Components {
		if len(c.Attrs) == 1 && c.Attrs[0] == "port" {
			portIdx = i
		}
	}
	if portIdx == -1 {
		t.Fatal("no component named exactly \"port\" found on aws_lb_target_group_attachment - has the row's shape changed?")
	}
	if !reverted.Components[portIdx].OmitIfAbsent {
		t.Fatal("port is not OmitIfAbsent on the current row; this mutation test's premise (revert it and watch the refusal come back) no longer holds")
	}
	reverted.Components[portIdx].OmitIfAbsent = false
	DefaultTable["aws_lb_target_group_attachment"] = reverted
	t.Cleanup(func() { DefaultTable["aws_lb_target_group_attachment"] = original })

	cfg := loadConfig(t, filepath.Join("testdata", "target-group-attachment-lambda-port"), nil)
	result, diags := Resolve(context.Background(), cfg)

	for _, r := range result.All() {
		if r.Addr.String() == "aws_lb_target_group_attachment.lambda" {
			t.Fatalf("lambda resolved to %+v with port reverted to non-OmitIfAbsent; it must refuse, not fabricate a port-less identity from a different code path", r)
		}
	}
	found := false
	for _, d := range diags {
		desc := d.Description()
		if desc.Summary == "Null identity argument" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a \"Null identity argument\" diagnostic once port is reverted to non-OmitIfAbsent; got: %v", diags)
	}
}
