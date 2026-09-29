// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"sort"
	"sync"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// UntagRelease is one instance GitHub issue #67's declared_tagged = "untag"
// verb actually released a marker key from: the node writer reached the
// instance, would have written Key, and left it out because
// [NodeResolver.PolicyUntag] named it. It is the node path's equivalent of
// the retired internal/live/stamp's Untagged record (GitHub issue #1002),
// per instance rather than per resource block for the reason PolicyUntag's
// own doc comment gives.
type UntagRelease struct {
	Addr addrs.AbsResourceInstance

	// Key is the marker key that was left out of what the writer wrote.
	Key string
}

// EstateMarker reports whether the released key is tofu-estate itself, the
// case a plan has to call out: the instance leaves management, because the
// next run's marker discovery can no longer find it by the marker that
// named it.
func (u UntagRelease) EstateMarker() bool { return u.Key == markers.TagEstate }

// untagReleases is the collector behind [NodeResolver.UntagReleases]. It is
// the only state a NodeResolver mutates after its fields are populated, and
// it is written from the plan walk's own goroutines (one
// [NodeResolver.AdjustConfigValue] call per instance, concurrently), so
// unlike [Result]'s cacheHits - counted on one sequential goroutine - it
// carries a mutex.
type untagReleases struct {
	mu    sync.Mutex
	byKey map[string]UntagRelease
}

// noteUntagRelease records that the writer left key out for addr at policy's
// request. written is the map the instance's OWN configuration declared,
// before this pass's entries are added: a key already present there is
// hand-written, the writer leaves it exactly as authored, and so nothing was
// released - the same line the retired stamp drew with SkipUntagHandWritten,
// and the reason this is a record of what happened rather than a copy of
// PolicyUntag.
//
// An apply walks the same instance a second time, so the record is keyed by
// instance address and key rather than appended.
func (n *NodeResolver) noteUntagRelease(addr addrs.AbsResourceInstance, key string, written map[string]cty.Value) {
	if _, handWritten := written[key]; handWritten {
		return
	}
	n.releases.mu.Lock()
	defer n.releases.mu.Unlock()
	if n.releases.byKey == nil {
		n.releases.byKey = map[string]UntagRelease{}
	}
	n.releases.byKey[addr.String()+"\x00"+key] = UntagRelease{Addr: addr, Key: key}
}

// UntagReleases reports every instance this resolver released a marker key
// from so far, sorted by instance address and then key. It is evidence
// about the walk rather than an input to it, surfaced the way
// [Result.CacheHits] and [Result.BoundIdentity] surface theirs - an
// unexported field behind an accessor - but it lives here and not on
// [Result] because a Result is complete before the walk that does the
// releasing has started. Read it once the plan is in hand: before the walk
// it is empty for every estate, which says nothing about the policy.
//
// It lists releases, not governed instances. [Result.Policy] already names
// every instance the verb governs; an instance appears there and not here
// when its configuration hand-writes the key, when a -target left it out of
// the walk, or when the writer declined to touch its tags at all (an
// unknown tags value, say) and said so in a warning of its own.
func (n *NodeResolver) UntagReleases() []UntagRelease {
	n.releases.mu.Lock()
	defer n.releases.mu.Unlock()
	if len(n.releases.byKey) == 0 {
		return nil
	}
	out := make([]UntagRelease, 0, len(n.releases.byKey))
	for _, r := range n.releases.byKey {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].Addr.String(), out[j].Addr.String(); a != b {
			return a < b
		}
		return out[i].Key < out[j].Key
	})
	return out
}
