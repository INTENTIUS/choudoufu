// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package markerstrip answers one question about a plan built from a state
// file: would applying it remove this fork's ownership markers from live
// resources that carry them?
//
// GitHub issue #613. After "choudoufu live-import -approve" has stamped an
// estate, the live resources carry tofu-estate and tofu-address and the
// state file - which predates the stamp - does not. A state-backed run
// refreshes, reads tags the configuration does not declare, and proposes
// removing them. Nothing about that diff is wrong: it is the only honest
// thing a state-backed plan can say. What is wrong is applying it, because
// those two tags ARE the estate's ownership record, so the apply
// un-migrates the estate and reads on screen as thirty-eight routine
// attribute changes.
//
// The detection needs no sweep, no record store and no cloud call. Both
// halves of the comparison are already inside the plan: the refreshed prior
// object carries the marker, and the planned new object does not.
//
// # What it deliberately does not report
//
// Only plans.Update. A Delete destroys the resource, which is not a silent
// un-migration and is exactly what stock would do; a replace
// (CreateThenDelete or DeleteThenCreate) destroys the marked object and
// creates an unmarked one, which does lose a marker but does so because the
// configuration asked for a new object, and refusing every replacement of a
// stamped resource would refuse working configurations. A Create has no
// prior to lose anything from.
//
// Only a definite removal. When either side's tag map is unknown - a
// computed tags attribute, an object still being planned - the answer is
// "cannot tell", and this package says nothing rather than guessing. A
// false negative there costs a warning; a false positive would refuse a
// plan that removes nothing.
package markerstrip

import (
	"bytes"
	"sort"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
)

// Removal is one resource instance whose ownership markers the plan removes.
type Removal struct {
	// Addr is the instance the plan would update.
	Addr addrs.AbsResourceInstance

	// Estate is the tofu-estate value the prior object carries and the
	// planned object does not. It is never empty in a returned Removal:
	// a resource with no estate marker is not this package's business.
	Estate string

	// Keys are the marker tag keys the plan removes from this instance,
	// sorted. It always contains markers.TagEstate and may contain
	// tofu-address, its continuation tags and tofu-slot.
	Keys []string
}

// SchemaFor looks up the schema a change's values were encoded against.
// It is a function rather than a *tofu.Schemas so that this package does not
// depend on the whole runtime for one map lookup; callers pass
// (*tofu.Schemas).ResourceTypeConfig's first return.
type SchemaFor func(provider addrs.Provider, mode addrs.ResourceMode, typeName string) *providers.Schema

// Scan reports every change in the plan that removes an ownership marker,
// sorted by address so that two runs over the same plan report the same
// thing in the same order.
//
// A change this package cannot decode is skipped rather than reported. The
// plan renderer decodes the same values immediately afterwards and reports
// the failure in its own voice; a guard that turned an undecodable change
// into a refusal would refuse for a reason that has nothing to do with
// markers.
func Scan(changes []*plans.ResourceInstanceChangeSrc, schemaFor SchemaFor) []Removal {
	var out []Removal
	for _, src := range changes {
		if src == nil || src.Action != plans.Update {
			continue
		}
		if src.Addr.Resource.Resource.Mode != addrs.ManagedResourceMode {
			continue
		}
		if !mightCarryEstate(src.Before) {
			continue
		}
		schema := schemaFor(src.ProviderAddr.Provider, src.Addr.Resource.Resource.Mode, src.Addr.Resource.Resource.Type)
		if schema == nil {
			continue
		}
		change, err := src.Decode(schema)
		if err != nil {
			continue
		}
		before, _ := change.Before.UnmarkDeep()
		after, _ := change.After.UnmarkDeep()

		beforeTags, ok := knownTags(before)
		if !ok {
			continue
		}
		estate := beforeTags[markers.TagEstate]
		if estate == "" {
			continue
		}
		afterTags, ok := knownTags(after)
		if !ok {
			continue
		}
		if afterTags[markers.TagEstate] == estate {
			continue
		}

		var keys []string
		for _, key := range MarkerKeys() {
			if beforeTags[key] == "" {
				continue
			}
			if afterTags[key] == beforeTags[key] {
				continue
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out = append(out, Removal{Addr: src.Addr, Estate: estate, Keys: keys})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr.String() < out[j].Addr.String() })
	return out
}

// Estates is the sorted, deduplicated set of estate names named by the given
// removals. A plan touching two estates is not expected and is not refused
// differently; the caller names all of them so that an operator who does hit
// it is not told about one and surprised by the other.
func Estates(removals []Removal) []string {
	seen := make(map[string]struct{}, len(removals))
	var out []string
	for _, r := range removals {
		if _, ok := seen[r.Estate]; ok {
			continue
		}
		seen[r.Estate] = struct{}{}
		out = append(out, r.Estate)
	}
	sort.Strings(out)
	return out
}

// MarkerKeys is every tag key that carries part of an ownership marker: the
// estate name, the address and its continuation tags, and the count slot.
//
// It is derived from the markers package's own constants rather than typed
// out, so a fifth marker key added there is covered here without an edit.
func MarkerKeys() []string {
	keys := []string{markers.TagEstate, markers.TagSlot}
	for i := 0; i < markers.MaxContinuations; i++ {
		keys = append(keys, markers.AddressTagKey(i))
	}
	sort.Strings(keys)
	return keys
}

// mightCarryEstate is the cheap pre-filter that keeps this scan off the
// critical path of every state-backed plan that has nothing to do with
// markers - which is nearly all of them, and which #611 measured as
// call-identical and wall-clock indistinguishable from stock. Decoding every
// updated resource would double the plan renderer's decode work for no
// result on an estate that was never migrated.
//
// It is sound as a filter, not as an answer. Both encodings a
// [plans.DynamicValue] can hold - msgpack and JSON - write an object's
// attribute names and a string map's keys as their literal UTF-8 bytes, so
// an encoded object whose tags contain "tofu-estate" always contains those
// eleven bytes. The converse does not hold: the bytes can appear in some
// unrelated string value, which costs one decode and then the real check.
func mightCarryEstate(encoded plans.DynamicValue) bool {
	return bytes.Contains(encoded, []byte(markers.TagEstate))
}

// knownTags reads an object's ownership-relevant tags, reporting false when
// the object has no tag surface at all OR when its tag surface is not wholly
// known.
//
// The second half is what [markers.TagsOf] alone cannot say. TagsOf treats an
// unknown tags attribute as "the type is taggable and nothing is set", which
// is the right answer for the question TagsOf asks and the wrong one here: a
// planned object whose tags are unknown would read as an object with no
// marker, and every stamped resource whose provider leaves tags computed
// would be reported as a marker removal it is not.
func knownTags(obj cty.Value) (map[string]string, bool) {
	if obj == cty.NilVal || obj.IsNull() || !obj.IsKnown() {
		return nil, false
	}
	ty := obj.Type()
	if !ty.IsObjectType() {
		return nil, false
	}
	for _, name := range []string{"tags", "tags_all"} {
		if !ty.HasAttribute(name) {
			continue
		}
		if !obj.GetAttr(name).IsWhollyKnown() {
			return nil, false
		}
	}
	return markers.TagsOf(obj)
}
