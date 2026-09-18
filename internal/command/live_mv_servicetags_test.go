// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"testing"
)

// TestLiveMvSweepClientsBuildsBothLegs is the command half of GitHub issue
// #1274, and it is separate from internal/live/mv's own guard on purpose:
// that one proves the engine can read an IAM marker through the service's
// tag API when it is handed a reader, and this one proves live-mv hands it
// one. The defect was entirely in the second question - the engine leg had
// existed since #1125, wired into live-plan and into nothing else, and
// live-mv refused a rename of the very resource live-plan had just renamed.
//
// Nothing here calls AWS. [newServiceTagsReader] resolves its credentials
// lazily and [servicetags.IAM.Route] is a map lookup, so the whole test is
// a construction and two lookups.
func TestLiveMvSweepClientsBuildsBothLegs(t *testing.T) {
	// The endpoint override is what [cloudControlTarget] reads, and it is
	// the ordinary condition of every emulator run - including the
	// corpus-iam-policy stage #1274 was found on. The gate variable is
	// cleared because this package's TestMain pins it off for every test
	// (command_test.go), which is right for the suite and is the one thing
	// that would make this test pass for the wrong reason.
	t.Setenv(cloudControlEnvVar, "")
	t.Setenv("AWS_ENDPOINT_URL", "http://127.0.0.1:1")

	tagging, serviceTags := liveMvSweepClients("us-east-1")
	if tagging == nil {
		t.Error("live-mv built no Tagging client with an endpoint override set; issue #266's fallback is gone")
	}
	if serviceTags == nil {
		t.Fatal("live-mv built no service tag reader with an endpoint override set: this is GitHub issue #1274 exactly - the leg live-plan has had since #1125, absent from the command that needed it to rename an aws_iam_policy")
	}
	// The type the defect was found on has to be one the reader will
	// actually answer for. Asserted through the reader's own routing table
	// rather than by naming what that table should contain, so this stays
	// true of whatever internal/live/servicetags decides it covers.
	if !serviceTags.Route("aws_iam_policy") {
		t.Error("the reader live-mv built has no route for aws_iam_policy, so the sweep still cannot read that type's marker")
	}
	if serviceTags.Route("aws_vpc") {
		t.Error("the reader routes aws_vpc; the per-service leg is for types no other route can answer for, and an ec2 sweep must not pay a call per object")
	}
}

// TestLiveMvSweepClientsAreBothAbsentWhenTheGateIsOff is the gate's off
// state, and the two legs have to leave together: the escape hatch for a
// target whose tag index is blind is a run with no sweep clients at all,
// and a service reader that outlived it would be a per-object IAM call the
// operator thought they had turned off.
//
// The off state is [cloudControlEnvVar], not an absent endpoint. An empty
// AWS_ENDPOINT_URL is an ordinary real-AWS run, which both clients are for.
func TestLiveMvSweepClientsAreBothAbsentWhenTheGateIsOff(t *testing.T) {
	t.Setenv(cloudControlEnvVar, "off")

	tagging, serviceTags := liveMvSweepClients("us-east-1")
	if tagging != nil {
		t.Error("a Tagging client was built with the Cloud Control gate off")
	}
	if serviceTags != nil {
		t.Error("a service tag reader was built with the Cloud Control gate off")
	}
}
