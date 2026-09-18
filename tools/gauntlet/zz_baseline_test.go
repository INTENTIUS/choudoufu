package main

import "testing"

func TestBaselineNonLadderRefusal(t *testing.T) {
	rec := refusal136()
	rec.Estate = "reference-ec2-vpc"
	rec.Scale = 0
	rec.Refusal.Reason = "the account's Amazon Linux AMI for this region resolves to nothing"
	rec.Refusal.Needed, rec.Refusal.Limit, rec.Refusal.Unit = nil, nil, ""
	plan := planLiveCertScaleRow("reference-ec2-vpc", true, rec)
	if plan.Err != nil {
		t.Fatalf("reference-ec2-vpc has no ladder and its refusal fails the run:\n  %v", plan.Err)
	}
	if !plan.Write {
		t.Fatalf("the refusal was not written anywhere: note=%q", plan.Note)
	}
}
