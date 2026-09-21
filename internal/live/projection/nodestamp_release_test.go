// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// releasePairs is [NodeResolver.UntagReleases] as comparable values.
func releasePairs(n *NodeResolver) [][2]string {
	var out [][2]string
	for _, r := range n.UntagReleases() {
		out = append(out, [2]string{r.Addr.String(), r.Key})
	}
	return out
}

// TestNodeResolver_UntagReleases_namesOnlyWhatWasReleased is GitHub issue
// #1002's record, by value, over one resolver that meets every way the verb
// can land on an instance: a governed instance with no tags of its own
// (released), its ungoverned sibling (stamped in full), a governed instance
// whose configuration hand-writes the key (left as authored, so nothing was
// released), a governed Kubernetes label and a governed manifest label
// (the writer's other two surfaces, which skip the same write), and a
// governed instance whose tags are not known yet (the writer declined it
// with a warning and released nothing).
func TestNodeResolver_UntagReleases_namesOnlyWhatWasReleased(t *testing.T) {
	pool := func(i int) addrs.AbsResourceInstance {
		return addrs.Resource{Mode: addrs.ManagedResourceMode, Type: markersRecordTestType, Name: "pool"}.
			Instance(addrs.IntKey(i)).Absolute(addrs.RootModuleInstance)
	}
	handWritten := locatedTestAddr(t, markersRecordTestType, "pinned")
	unknownTags := locatedTestAddr(t, markersRecordTestType, "later")
	configMap := configMapAddr(t)
	manifest := manifestAddr(t)

	resolver := &NodeResolver{
		Estate: "test-estate",
		PolicyUntag: map[string]string{
			pool(0).String():     markers.TagEstate,
			handWritten.String(): markers.TagEstate,
			unknownTags.String(): markers.TagEstate,
			configMap.String():   markers.TagEstate,
			manifest.String():    markers.TagEstate,
		},
	}

	if got := releasePairs(resolver); got != nil {
		t.Fatalf("releases before any instance was visited = %v, want none", got)
	}

	noTags := nodeStampTestConfig(cty.NullVal(cty.Map(cty.String)))
	visit := func(addr addrs.AbsResourceInstance, config cty.Value) {
		t.Helper()
		schema := markersRecordTypeSchema()
		switch addr.String() {
		case configMap.String():
			schema = configMapTypeSchema()
		case manifest.String():
			schema = manifestTypeSchema()
		}
		if _, diags := resolver.AdjustConfigValue(context.Background(), addr, config, schema); diags.HasErrors() {
			t.Fatalf("%s: unexpected diagnostics: %s", addr, diags.Err())
		}
	}

	visit(pool(0), noTags)
	visit(pool(1), noTags)
	visit(handWritten, nodeStampTestConfig(cty.MapVal(map[string]cty.Value{markers.TagEstate: cty.StringVal("test-estate")})))
	visit(unknownTags, nodeStampTestConfig(cty.UnknownVal(cty.Map(cty.String))))
	visit(configMap, configMapTestConfig(cty.NullVal(cty.Map(cty.String))))
	visit(manifest, manifestTestConfig(manifestTestManifest(cty.NilVal)))

	want := [][2]string{
		{configMap.String(), markers.TagEstate},
		{manifest.String(), markers.TagEstate},
		{pool(0).String(), markers.TagEstate},
	}
	// UntagReleases sorts by address; sort want the same way rather than
	// hard-coding how three type names happen to collate.
	sortPairs(want)
	if got := releasePairs(resolver); !reflect.DeepEqual(got, want) {
		t.Fatalf("releases = %v, want %v", got, want)
	}

	// An apply walks every instance a second time through the same
	// resolver. The record is of instances, not of visits.
	visit(pool(0), noTags)
	visit(configMap, configMapTestConfig(cty.NullVal(cty.Map(cty.String))))
	if got := releasePairs(resolver); !reflect.DeepEqual(got, want) {
		t.Errorf("releases after a second walk = %v, want %v unchanged", got, want)
	}

	for _, r := range resolver.UntagReleases() {
		if !r.EstateMarker() {
			t.Errorf("%s released %q, which EstateMarker does not call tofu-estate", r.Addr, r.Key)
		}
	}
}

// TestNodeResolver_UntagReleases_otherMarkerKeys: a policy whose tag_key is
// one of the other two markers. tofu-address is always written, so skipping
// it is always a release. tofu-slot is written only where the sweep
// assigned a slot, so an instance with none had no write to withhold. A
// tag_key that is no marker at all releases nothing.
func TestNodeResolver_UntagReleases_otherMarkerKeys(t *testing.T) {
	address := locatedTestAddr(t, markersRecordTestType, "address")
	slotted := locatedTestAddr(t, markersRecordTestType, "slotted")
	unslotted := locatedTestAddr(t, markersRecordTestType, "unslotted")
	foreign := locatedTestAddr(t, markersRecordTestType, "foreign")

	resolver := &NodeResolver{
		Estate: "test-estate",
		Slots:  map[string]string{markers.EscapeAddress(slotted.String()): "slot-7"},
		PolicyUntag: map[string]string{
			address.String():   markers.TagAddress,
			slotted.String():   markers.TagSlot,
			unslotted.String(): markers.TagSlot,
			foreign.String():   "team",
		},
	}
	for _, addr := range []addrs.AbsResourceInstance{address, slotted, unslotted, foreign} {
		got, diags := resolver.AdjustConfigValue(context.Background(), addr, nodeStampTestConfig(cty.NullVal(cty.Map(cty.String))), markersRecordTypeSchema())
		if diags.HasErrors() {
			t.Fatalf("%s: unexpected diagnostics: %s", addr, diags.Err())
		}
		if addr.String() == slotted.String() {
			if _, ok := requireTags(t, got)[markers.TagSlot]; ok {
				t.Fatalf("the slot the record says was released is still written: %v", requireTags(t, got))
			}
		}
	}

	want := [][2]string{
		{address.String(), markers.TagAddress},
		{slotted.String(), markers.TagSlot},
	}
	sortPairs(want)
	if got := releasePairs(resolver); !reflect.DeepEqual(got, want) {
		t.Fatalf("releases = %v, want %v", got, want)
	}
	for _, r := range resolver.UntagReleases() {
		if r.EstateMarker() {
			t.Errorf("%s released %q and is reported as leaving management", r.Addr, r.Key)
		}
	}
}

// TestNodeResolver_UntagReleases_concurrentWalk: the plan walk calls
// AdjustConfigValue from many goroutines at once. Run under -race (CI does)
// this fails on an unguarded collector; without -race it still checks no
// release was lost.
func TestNodeResolver_UntagReleases_concurrentWalk(t *testing.T) {
	const instances = 64
	resolver := &NodeResolver{Estate: "test-estate", PolicyUntag: map[string]string{}}
	all := make([]addrs.AbsResourceInstance, instances)
	for i := range all {
		all[i] = addrs.Resource{Mode: addrs.ManagedResourceMode, Type: markersRecordTestType, Name: "pool"}.
			Instance(addrs.IntKey(i)).Absolute(addrs.RootModuleInstance)
		if i%2 == 0 {
			resolver.PolicyUntag[all[i].String()] = markers.TagEstate
		}
	}

	var wg sync.WaitGroup
	for _, addr := range all {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = resolver.AdjustConfigValue(context.Background(), addr, nodeStampTestConfig(cty.NullVal(cty.Map(cty.String))), markersRecordTypeSchema())
			_ = resolver.UntagReleases()
		}()
	}
	wg.Wait()

	if got := len(resolver.UntagReleases()); got != instances/2 {
		t.Fatalf("%d releases recorded, want %d", got, instances/2)
	}
}

func sortPairs(p [][2]string) {
	for i := 1; i < len(p); i++ {
		for j := i; j > 0 && p[j][0] < p[j-1][0]; j-- {
			p[j], p[j-1] = p[j-1], p[j]
		}
	}
}
