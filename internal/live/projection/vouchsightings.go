// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"github.com/intentius/choudoufu/internal/addrs"
)

// VouchSightings is the existence-and-identity evidence one run's listing
// passes produced for the state cache's record-envelope arm: for each
// provider configuration a pass listed through, for each resource type it
// was asked to vouch, the set of live import identities that listing
// returned WITHOUT a visible ownership marker.
//
// The provider partition is issue #745, and it is what makes the arm's
// stated premise true rather than nearly true. A sighting is evidence that
// an object of that identity exists in the account and region the pass
// listed, and nowhere else. Without the partition, a multi-region estate
// that mirrors one client-chosen name into two regions - a log group, a
// bucket-style name, anything whose import identity is the name the
// configuration chose - lets region B's object vouch existence for region
// A's instance: A's object can have been deleted out of band, B's listing
// still names the identity, the record still attests it, and the plan
// reports a dead instance unchanged until the next full read. Every other
// vouch in this fork is pass-bound already ([Ownership.Verified] is
// address-keyed and produced by the pass that swept for the marker), so
// this is the sightings joining the rule the rest of the evidence follows.
//
// The keys are [addrs.AbsProviderConfig.String] values, which is the same
// spelling internal/live/discovery's own scoping compares
// (providerscope.ResolveResource of the resource block) and the same one
// [readPrep.providerAddr] carries, so a lookup is an exact string match
// between two renderings of the same resolution.
//
// A nil or empty map vouches for nothing, and so does a lookup under a
// provider configuration no pass stamped: every leg of the arm fails
// toward reading.
type VouchSightings map[string]map[string]map[string]bool

// Sighted reports whether the pass that listed through providerAddr saw the
// given import identity of the given type, unmarked, in this run.
func (v VouchSightings) Sighted(providerAddr addrs.AbsProviderConfig, typeName, importID string) bool {
	if len(v) == 0 || importID == "" {
		return false
	}
	return v[providerAddr.String()][typeName][importID]
}

// Add records one pass's sighting, returning the (possibly newly allocated)
// map so that a nil receiver is usable: v = v.Add(...).
func (v VouchSightings) Add(providerAddr addrs.AbsProviderConfig, typeName, importID string) VouchSightings {
	if typeName == "" || importID == "" {
		return v
	}
	if v == nil {
		v = VouchSightings{}
	}
	key := providerAddr.String()
	if v[key] == nil {
		v[key] = map[string]map[string]bool{}
	}
	if v[key][typeName] == nil {
		v[key][typeName] = map[string]bool{}
	}
	v[key][typeName][importID] = true
	return v
}

// Union folds other into v, keeping every pass's own partition: two passes
// that sighted the same identity keep two entries, one per provider
// configuration, because they are two facts about two accounts. This is
// what internal/live/discovery's multi-provider Merge does with each pass's
// sightings, and the reason it is a union rather than a flattening.
func (v VouchSightings) Union(other VouchSightings) VouchSightings {
	for providerKey, byType := range other {
		for typeName, ids := range byType {
			for id := range ids {
				if v == nil {
					v = VouchSightings{}
				}
				if v[providerKey] == nil {
					v[providerKey] = map[string]map[string]bool{}
				}
				if v[providerKey][typeName] == nil {
					v[providerKey][typeName] = map[string]bool{}
				}
				v[providerKey][typeName][id] = true
			}
		}
	}
	return v
}
