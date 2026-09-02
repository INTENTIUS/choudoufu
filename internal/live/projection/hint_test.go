// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"reflect"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/states"
)

// TestHint_reducedShapeOnly keeps [Hint] at the reduced shape issue #109
// settled on: which types, and when. The snapshot-era Identifiers field was
// dropped along with the snapshot itself (nothing ever read it), and a Hint
// that grew a field beyond this list would be a reader quietly widening
// back toward a full per-resource record - the thing the hint exists to not
// be.
func TestHint_reducedShapeOnly(t *testing.T) {
	allowed := map[string]bool{
		"Estate":    true,
		"WrittenAt": true,
		"Types":     true,
	}
	rt := reflect.TypeOf(Hint{})
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		if !allowed[name] {
			t.Errorf("Hint carries field %q, which is not one of the reduced hint fields %v; "+
				"a hint may only ever narrow what a caller can learn, never widen it toward a full record", name, allowed)
		}
	}
}

// testHintState is a small, two-type state: enough for the round trip in
// hint_store_test.go to prove the whole type set survives, which a
// single-resource fixture (testProjectionState) cannot distinguish from a
// writer that only recorded "the one resource this state had".
func testHintState() *states.State {
	state := states.NewState()
	state.RootModule().SetResourceInstanceCurrent(
		addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "aws_s3_bucket", Name: "data"}.Instance(addrs.NoKey),
		&states.ResourceInstanceObjectSrc{
			AttrsJSON: []byte(`{"id":"bucket-a","bucket":"bucket-a"}`),
			Status:    states.ObjectReady,
		},
		addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")},
		addrs.NoKey,
	)
	state.RootModule().SetResourceInstanceCurrent(
		addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "aws_sns_topic", Name: "alerts"}.Instance(addrs.NoKey),
		&states.ResourceInstanceObjectSrc{
			AttrsJSON: []byte(`{"id":"arn:aws:sns:us-east-1:000000000000:alerts"}`),
			Status:    states.ObjectReady,
		},
		addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")},
		addrs.NoKey,
	)
	return state
}
