// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/states"
)

// GitHub issue #1883, measured by reference-k8s-platform-app's
// day2_teardown: after app's `apply -destroy`, 13 record Secrets of app's
// were still in tofu-records-app. Two mechanisms leave them, and these
// tests prove each red.
//
// 1. A tombstone on Kubernetes. [RecordStore.tombstone] keeps a destroyed
//    instance's identity as a "destroyed by us" entry, for AWS's tagging
//    index, which lists a terminated object's tags for a while after it is
//    gone. live/MARKERS.md's ruled table (#1188) says what that entry is on
//    Kubernetes: "writable only behind identity, never read", because the
//    label is a field of the object and nothing lingers once the object is
//    deleted. So every identity-bearing Kubernetes record (a ConfigMap, a
//    Namespace) outlived its object as an envelope nothing reads.
//
// 2. A record whose address the destroy's plan never read. Write-back
//    deletes only the keys the plan read a version for. A record written
//    under an address that left the configuration some other way stays:
//    the old address of a `moved` block, a block removed and destroyed at
//    its orphan address, an object handed to another estate by
//    `live-mv -from-estate` (whose help text says the source's record
//    stays behind). On a whole destroy the estate is going away, so each
//    such key gets the same tombstone-or-delete an address leaving the
//    final state gets. A kind=object envelope is left alone: that one IS a
//    record-backed instance, and #1355's guard names it as a survivor.

const k8sProviderString = `provider["registry.opentofu.org/hashicorp/kubernetes"]`

func TestTombstoneOnKubernetesActsLikeDelete(t *testing.T) {
	ctx := context.Background()
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("tombstone-estate"))
	addr := locatedTestAddr(t, "kubernetes_config_map", "app")

	version, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
		env.Identity = &identityPayload{ImportID: "shop/app-config"}
		env.Provider = k8sProviderString
	})
	if err != nil {
		t.Fatalf("seeding the current identity: %s", err)
	}
	if err := store.tombstone(ctx, addr, version, nil); err != nil {
		t.Fatalf("tombstone: %s", err)
	}
	if tombstones, _, keyExists, err := store.GetTombstones(ctx, addr); err != nil || keyExists {
		t.Errorf("a destroyed Kubernetes object's record outlived it (keyExists=%v, tombstones %#v, err %v); on this substrate a tombstone is never read, so the destroy must delete the key", keyExists, tombstones, err)
	}
}

func TestWholeDestroyClearsRecordsThePlanNeverRead(t *testing.T) {
	ctx := context.Background()
	staterecord.ResetRunCacheForTest(t)
	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	const prefix = "tofu-records/app"
	store := NewRecordEnvelopeStore(raw, prefix)

	// What live-mv -from-estate leaves in the source estate: a residue
	// record for an address the configuration no longer declares.
	handoff := locatedTestAddr(t, "kubernetes_manifest", "handoff")
	if _, err := store.mergeEnvelope(ctx, handoff, "", func(env *recordEnvelope) {
		env.Residue = &residueFields{Attributes: map[string]residueAttrValue{
			"wait": {Type: []byte(`"string"`), Value: []byte(`"x"`)},
		}}
		env.Provider = k8sProviderString
	}); err != nil {
		t.Fatalf("seeding the moved-away record: %s", err)
	}
	// An AWS identity under an address the plan never read: it must keep
	// its tombstone, which AWS's lingering tags still need.
	old := locatedTestAddr(t, "aws_instance", "old")
	if _, err := store.mergeEnvelope(ctx, old, "", func(env *recordEnvelope) {
		env.Identity = &identityPayload{ImportID: "i-0old"}
		env.Provider = `provider["registry.opentofu.org/hashicorp/aws"]`
	}); err != nil {
		t.Fatalf("seeding the unread AWS identity: %s", err)
	}

	run := func(whole bool) {
		t.Helper()
		staterecord.ResetRunCacheForTest(t)
		diags := WriteBack(ctx, WriteBackRequest{
			Store:        store,
			FinalState:   states.NewState(),
			Schemas:      nullSchemas(),
			WholeDestroy: whole,
		})
		if diags.HasErrors() {
			t.Fatalf("WriteBack: %s", diags.Err())
		}
	}

	// A destroy that is not whole (-target) leaves what it did not plan.
	run(false)
	if _, _, keyExists, _ := store.GetTombstones(ctx, handoff); !keyExists {
		t.Fatal("premise: a targeted destroy deleted a record it never planned")
	}

	run(true)
	if _, _, keyExists, err := store.GetTombstones(ctx, handoff); err != nil || keyExists {
		t.Errorf("the whole destroy left the moved-away record for %s (keyExists=%v err=%v)", handoff, keyExists, err)
	}
	tombstones, _, keyExists, err := store.GetTombstones(ctx, old)
	if err != nil || !keyExists || len(tombstones) != 1 {
		t.Errorf("the unread AWS identity for %s was not reduced to its tombstone: keyExists=%v tombstones=%#v err=%v", old, keyExists, tombstones, err)
	}
	if _, _, _, found, _ := store.GetIdentity(ctx, old); found {
		t.Errorf("the unread AWS identity for %s is still a current identity after the whole destroy", old)
	}
}

// The two records day2_teardown still left after the change above, both
// from the shared kind legs' create_before_destroy replaces (day2_replace's
// kubernetes_config_map.hashed, day2_crash's crash_rename_read): a replace
// keeps its address and changes its object, and [supersedeIdentity] (and,
// for a destroyed deposed object, [tombstoneDestroyedDeposed]) wrote a
// tombstone entry straight through [addTombstoneEntry], which did not ask
// the family. At teardown [RecordStore.tombstone] cleared the identity, but
// the earlier entry kept the envelope non-empty and the key alive.
func TestKubernetesReplaceLeavesNoTombstoneToOutliveTheDestroy(t *testing.T) {
	ctx := context.Background()
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("tombstone-estate"))
	addr := locatedTestAddr(t, "kubernetes_config_map", "hashed")

	// The replace: cfg-a superseded by cfg-b at the same address.
	version, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
		env.Identity = &identityPayload{ImportID: "shop/cfg-a"}
		env.Provider = k8sProviderString
		supersedeIdentity(env, &identityPayload{ImportID: "shop/cfg-b"})
		env.Identity = &identityPayload{ImportID: "shop/cfg-b"}
	})
	if err != nil {
		t.Fatalf("seeding the replaced identity: %s", err)
	}
	if tombstones, _, _, _ := store.GetTombstones(ctx, addr); len(tombstones) != 0 {
		t.Errorf("a Kubernetes replace wrote tombstone entries %#v; on this substrate nothing reads them", tombstones)
	}

	// The teardown.
	if err := store.tombstone(ctx, addr, version, nil); err != nil {
		t.Fatalf("tombstone: %s", err)
	}
	if tombstones, _, keyExists, err := store.GetTombstones(ctx, addr); err != nil || keyExists {
		t.Errorf("the destroyed replace's record outlived the destroy (keyExists=%v, tombstones %#v, err %v)", keyExists, tombstones, err)
	}
}

// An envelope an earlier build already wrote with a Kubernetes tombstone in
// it is cleared by the destroy too, so the leftover does not need a manual
// cleanup on estates that ran before this fix.
func TestKubernetesDestroyDropsAnEarlierBuildsTombstone(t *testing.T) {
	ctx := context.Background()
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("tombstone-estate"))
	addr := locatedTestAddr(t, "kubernetes_config_map", "crash_rename_read")

	version, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
		env.Identity = &identityPayload{ImportID: "shop/crash-read-b"}
		env.Provider = k8sProviderString
		env.Tombstone = map[string]*tombstoneFields{
			"shop/crash-read-a": {Identity: &identityPayload{ImportID: "shop/crash-read-a"}, Provider: k8sProviderString},
		}
	})
	if err != nil {
		t.Fatalf("seeding: %s", err)
	}
	if err := store.tombstone(ctx, addr, version, nil); err != nil {
		t.Fatalf("tombstone: %s", err)
	}
	if tombstones, _, keyExists, err := store.GetTombstones(ctx, addr); err != nil || keyExists {
		t.Errorf("the destroy kept an earlier build's Kubernetes tombstone (keyExists=%v, tombstones %#v, err %v)", keyExists, tombstones, err)
	}
}
