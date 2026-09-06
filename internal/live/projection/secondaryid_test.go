// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #879's write half. A type can be identified two ways at once
// and aws_ecs_task_definition is: its wire identity SCHEMA is family +
// revision (what this package records), while its documented import string
// is the whole task-definition ARN (what internal/live/discovery composes
// for every live object it finds by its marker alone). Recording only the
// first left a replace's tombstone unmatchable against the destroyed
// object's own lingering tag, and corpus-ecs-fargate's day2_replace refused
// on every plan after the replace.
//
// The schema below is the shape the AWS provider serves for that type,
// carried in this test rather than fetched, exactly as
// internal/live/identity's TestLocatedIdentityPlanNumberComponent carries
// it for #671.
func taskDefinitionSchema() providers.Schema {
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":       {Type: cty.String, Computed: true},
				"arn":      {Type: cty.String, Computed: true},
				"family":   {Type: cty.String, Required: true},
				"revision": {Type: cty.Number, Computed: true},
			},
		},
		IdentitySchema: &configschema.Object{
			Nesting: configschema.NestingSingle,
			Attributes: map[string]*configschema.Attribute{
				"family":   {Type: cty.String, Required: true},
				"revision": {Type: cty.Number, Required: true},
			},
		},
	}
}

// TestLocatedRecordFromRecordsBothNamesOfOneObject: the record an apply
// writes for a composite-identity type that also imports by its whole ARN
// must carry both names, asserted by value. The components are what a
// record-first import reads; the second name is the only thing a
// marker-found sighting can ever be compared against.
func TestLocatedRecordFromRecordsBothNamesOfOneObject(t *testing.T) {
	const arn = "arn:aws:ecs:eu-west-1:000000000000:task-definition/ex-fargate-standalone-v2:1"
	obj := cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal(arn),
		"arn":      cty.StringVal(arn),
		"family":   cty.StringVal("ex-fargate-standalone-v2"),
		"revision": cty.NumberIntVal(1),
	})

	rec, ok := LocatedRecordFrom("aws_ecs_task_definition", taskDefinitionSchema(), obj)
	if !ok {
		t.Fatal("the applied object was not recordable at all")
	}
	if rec.Components["family"] != "ex-fargate-standalone-v2" || rec.Components["revision"] != "1" {
		t.Errorf("components = %v, want family=ex-fargate-standalone-v2 revision=1", rec.Components)
	}
	// The identity object stays the recorded identity, and the ARN is NOT
	// promoted into ImportID: locatedfallback.go binds real instances from
	// that field, and #746's composite population must keep taking its
	// components branch.
	if rec.ImportID != "" {
		t.Errorf("ImportID = %q, want empty - a composite identity is still recorded as an object", rec.ImportID)
	}
	if rec.SecondaryID != arn {
		t.Errorf("SecondaryID = %q, want the whole task-definition ARN %q - without it a replace's tombstone cannot be compared to the destroyed object's own lingering tag (#879)", rec.SecondaryID, arn)
	}
}

// TestLocatedRecordFromSecondNameSurvivesTheStore is the same claim through
// the real local store, since a value that is written and not read back is
// not evidence. It also pins the tombstone leg, which is the one #879's
// refusal actually turns on.
func TestLocatedRecordFromSecondNameSurvivesTheStore(t *testing.T) {
	ctx := t.Context()
	addr := mustAddr(t, locatedTestType+`.standalone`)
	store := newTestLocatedStore(localHintStore(t), "test-estate-879").rs

	const liveARN = "arn:aws:ecs:eu-west-1:000000000000:task-definition/ex-fargate-standalone-v2:1"
	const deadARN = "arn:aws:ecs:eu-west-1:000000000000:task-definition/ex-fargate-standalone:1"

	if _, err := SeedLocatedForInstance(ctx, store, addr, locatedTestProvider, LocatedRecord{
		Components:  map[string]string{"family": "ex-fargate-standalone-v2", "revision": "1"},
		SecondaryID: liveARN,
	}); err != nil {
		t.Fatalf("seeding the identity: %s", err)
	}
	if err := SeedTombstoneForInstance(ctx, store, addr, TombstoneRecord{
		Components:  map[string]string{"family": "ex-fargate-standalone", "revision": "1"},
		SecondaryID: deadARN,
	}); err != nil {
		t.Fatalf("seeding the tombstone: %s", err)
	}

	rec, _, _, found, err := store.GetIdentity(ctx, addr)
	if err != nil || !found {
		t.Fatalf("reading the identity back: found=%v err=%v", found, err)
	}
	if rec.SecondaryID != liveARN {
		t.Errorf("the stored identity reads back SecondaryID %q, want %q", rec.SecondaryID, liveARN)
	}

	tombstones, _, _, err := store.GetTombstones(ctx, addr)
	if err != nil {
		t.Fatalf("reading the tombstones back: %s", err)
	}
	if len(tombstones) != 1 {
		t.Fatalf("want exactly one tombstone, got %d", len(tombstones))
	}
	for _, tr := range tombstones {
		if tr.SecondaryID != deadARN {
			t.Errorf("the stored tombstone reads back SecondaryID %q, want %q - the destroyed object's lingering tag is identified by that string and nothing else", tr.SecondaryID, deadARN)
		}
	}
}
