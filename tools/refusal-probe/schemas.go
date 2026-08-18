// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/getproviders"
	"github.com/intentius/choudoufu/internal/live/check"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/pluginschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file is the acquisition half of -schemas mode. It is deliberately the
// same shape tools/corpus-gen uses, and for the same reasons, because the
// whole value of this mode is that its numbers can be held next to
// live/corpus-refusals.json:
//
//   - the providers to acquire come from [*configs.Config.ProviderRequirements],
//     the recursive resolver a real "tofu init" calls, so an entry gets its
//     OWN providers and a google_* or tfe_* estate is not measured against an
//     AWS-only schema map (issue #211);
//   - acquisition is cached by (provider FQN, version constraint) across the
//     whole run, since 250-odd entries collapse to far fewer distinct
//     requirements;
//   - hashicorp/aws is forced to [pins.AWSProviderVersion], matched
//     structurally against the -provider-source address rather than by a name
//     in control flow;
//   - every other provider's version is read from the checked-in lock file
//     live/corpus-provider-pins.json (issue #222), so this probe and the
//     committed artifact are looking at the same provider releases.
//
// What it deliberately does NOT do is write that lock file. corpus-gen owns
// it. A probe run that meets a (provider, constraint) pair the lock has never
// seen resolves it live, reports it as unlocked, and leaves the file alone -
// several probes can then run concurrently in one tree, which is the property
// this program exists for and which `just corpus` does not have.

// providerNeed is one provider an entry declares or implies, with whatever
// version constraint the requiring configuration itself wrote.
type providerNeed struct {
	Provider   addrs.Provider
	Constraint string
}

func (n providerNeed) key() string { return n.Provider.String() + "@" + n.Constraint }

// providerNeeds resolves the providers one loaded configuration declares or
// implies. Built-in providers are excluded: they need no fetch and every type
// they carry is in the generated admission table already.
//
// Diagnostics are dropped rather than raised: whatever the resolver did
// populate is still worth acquiring, and a configuration whose own
// required_providers is broken already has load-level findings saying so.
func providerNeeds(cfg *configs.Config) []providerNeed {
	if cfg == nil {
		return nil
	}
	reqs, _, _ := cfg.ProviderRequirements()

	out := make([]providerNeed, 0, len(reqs))
	for provider, constraints := range reqs {
		if provider.IsZero() || provider.IsBuiltIn() {
			continue
		}
		out = append(out, providerNeed{
			Provider:   provider,
			Constraint: getproviders.VersionConstraintsString(constraints),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key() < out[j].key() })
	return out
}

// acquirer fetches provider schemas, memoized by (provider, constraint).
type acquirer struct {
	initBin string

	// pinned and pinnedVersion name the one provider held at an exact
	// version whatever a requiring configuration's own constraint says.
	pinned        addrs.Provider
	pinnedVersion string

	// locks is live/corpus-provider-pins.json, read once and never written.
	locks providerLocks

	log io.Writer

	cache map[string]acquired
}

// acquired is one (provider, constraint) pair's outcome.
type acquired struct {
	Provider   string
	Constraint string
	Version    string
	Types      int
	Pinned     bool
	Locked     bool
	Available  bool
	Error      string

	schemas map[string]providers.Schema

	// listTypes is the set of type names this (provider, version) pair
	// serves a LIST resource for - the GetProviderSchema response's
	// ListResourceTypes section, which "providers schema -json" never
	// carries (see internal/live/pluginschema's package doc) and which
	// [acquired.schemas] therefore cannot answer for. It exists for issue
	// #269's version-skew
	// comparison: a type can have a perfectly good managed-resource schema
	// at a release that offers no way to LIST it, which is exactly the
	// "Unlistable marker-discovered type" refusal internal/live/discovery
	// raises and this probe otherwise never observes.
	listTypes map[string]bool
}

func newAcquirer(initBin string, pinned addrs.Provider, pinnedVersion string, locks providerLocks, log io.Writer) *acquirer {
	if locks == nil {
		locks = providerLocks{}
	}
	return &acquirer{
		initBin:       initBin,
		pinned:        pinned,
		pinnedVersion: pinnedVersion,
		locks:         locks,
		log:           log,
		cache:         map[string]acquired{},
	}
}

// schemasFor merges every available provider's resource-type schemas for one
// entry, and returns a status row per provider alongside.
//
// The row per provider is the point, not decoration. A caller handed the
// merged map alone cannot tell "this entry's providers all loaded" from "one
// of three loaded", and that difference is exactly what made a partial
// acquisition fabricate hard refusals once: a gate reading len(schemas) > 0
// went true on random_id's schema while the AWS schema the entry actually
// needed had failed. 35 of the 250 entries in the committed corpus show a
// missing provider, so this is the common case and not an edge.
func (a *acquirer) schemasFor(needs []providerNeed) (map[string]providers.Schema, []entryProvider) {
	if a.initBin == "" || len(needs) == 0 {
		return nil, nil
	}

	merged := map[string]providers.Schema{}
	rows := make([]entryProvider, 0, len(needs))
	for _, need := range needs {
		res := a.acquire(need)
		rows = append(rows, entryProvider{
			Provider: res.Provider,
			Version:  res.Version,
			Present:  res.Available,
			Error:    res.Error,
		})
		if res.Available {
			for typeName, schema := range res.schemas {
				merged[typeName] = schema
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Provider < rows[j].Provider })
	if len(merged) == 0 {
		merged = nil
	}
	return merged, rows
}

// acquire runs, or replays from cache, one provider's schema read, forcing
// [acquirer.pinned] to [acquirer.pinnedVersion] when need names it.
func (a *acquirer) acquire(need providerNeed) acquired {
	return a.resolve(need, true)
}

// acquireOwn resolves need at the constraint the REQUIRING configuration
// itself wrote, even when need.Provider is the one provider this run
// otherwise pins for every entry.
//
// It exists for issue #269's version-skew signal alone. -schemas mode holds
// hashicorp/aws at one exact release so every entry compares against every
// other on equal footing, and that consistency is exactly what makes the
// probe blind to an entry whose OWN "~> 5.64"-shaped constraint resolves to
// a real release with weaker list-resource coverage than the pinned one -
// a live-plan-time refusal ("Unlistable marker-discovered type",
// internal/live/discovery/refusals.go:165) the pinned acquisition never
// reproduces. See [versionSkewFor].
//
// Cached under an "own:"-prefixed key, distinct from [acquirer.acquire]'s
// slot for the identical provider: acquire's pinned branch collapses every
// constraint to the empty string before computing its key, while floating
// at the entry's own constraint IS the point here, so the real constraint
// has to survive into the key.
func (a *acquirer) acquireOwn(need providerNeed) acquired {
	return a.resolve(need, false)
}

// resolve is [acquirer.acquire] and [acquirer.acquireOwn]'s shared body.
// forcePin true is acquire's ordinary behaviour: a need naming
// [acquirer.pinned] is pinned to [acquirer.pinnedVersion] regardless of its
// own constraint. forcePin false always resolves at need's own constraint
// (or the lock file's answer for it, exactly as any other, non-pinned
// provider does), whichever provider it names.
func (a *acquirer) resolve(need providerNeed, forcePin bool) acquired {
	pinned := forcePin && a.pinnedVersion != "" && need.Provider == a.pinned

	cacheNeed := need
	if pinned {
		// One cache slot for the pinned provider however the requiring
		// entry spelled its constraint: two entries with different ">= x.y"
		// floors get the identical release, so they must not fragment into
		// two installs.
		cacheNeed.Constraint = ""
	}

	key := cacheNeed.key()
	if !forcePin {
		key = "own:" + key
	}
	if res, ok := a.cache[key]; ok {
		return res
	}

	res := acquired{
		Provider:   need.Provider.ForDisplay(),
		Constraint: cacheNeed.Constraint,
		Pinned:     pinned,
	}

	req := pluginschema.Request{
		InitBin:    a.initBin,
		Source:     need.Provider.ForDisplay(),
		Constraint: need.Constraint,
		Provider:   need.Provider,
		Log:        a.log,
	}
	switch {
	case pinned:
		req.Version = a.pinnedVersion
		req.Constraint = ""
	default:
		if lock, ok := a.locks[lockKey(res.Provider, res.Constraint)]; ok {
			res.Locked = true
			if lock.Available && lock.Version != "" {
				req.Version = lock.Version
				req.Constraint = ""
			}
		}
	}

	workdir, err := os.MkdirTemp("", "refusal-probe-schemas")
	if err != nil {
		res.Error = err.Error()
		a.cache[key] = res
		return res
	}
	defer os.RemoveAll(workdir)
	req.WorkDir = workdir

	// The full GetProviderSchema response, not just [pluginschema.ResourceTypes]:
	// this run needs the ListResourceTypes section too, to answer "can THIS
	// release list the types the entry declares" (issue #269). See this
	// file's package doc for why that section is unreachable any other way.
	full, err := pluginschema.Acquire(context.Background(), req)
	if err != nil {
		res.Error = err.Error()
		a.cache[key] = res
		return res
	}

	res.Available = true
	res.schemas = full.ResourceTypes
	res.Types = len(full.ResourceTypes)
	if len(full.ListResourceTypes) > 0 {
		res.listTypes = make(map[string]bool, len(full.ListResourceTypes))
		for t := range full.ListResourceTypes {
			res.listTypes[t] = true
		}
	}
	res.Version = req.Version
	if res.Version == "" {
		if v, ok := pluginschema.InstalledVersion(workdir, need.Provider); ok {
			res.Version = v
		}
	}

	a.cache[key] = res
	return res
}

// results is every (provider, constraint) pair this run tried, sorted
// available-first so an unavailable one is never buried.
func (a *acquirer) results() []providerResult {
	out := make([]providerResult, 0, len(a.cache))
	for _, res := range a.cache {
		out = append(out, providerResult{
			Provider:   res.Provider,
			Constraint: res.Constraint,
			Version:    res.Version,
			Types:      res.Types,
			Pinned:     res.Pinned,
			Locked:     res.Locked,
			Available:  res.Available,
			Error:      res.Error,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		x, y := out[i], out[j]
		if x.Available != y.Available {
			return x.Available
		}
		if x.Provider != y.Provider {
			return x.Provider < y.Provider
		}
		return x.Constraint < y.Constraint
	})
	return out
}

// providerLock is one row of live/corpus-provider-pins.json. Only the fields
// this program reads are declared; the file is corpus-gen's and is never
// written here.
type providerLock struct {
	Provider   string `json:"provider"`
	Constraint string `json:"constraint,omitempty"`
	Available  bool   `json:"available"`
	Version    string `json:"version,omitempty"`
	Error      string `json:"error,omitempty"`
}

type providerLocks map[string]providerLock

func lockKey(provider, constraint string) string { return provider + "@" + constraint }

// loadProviderLocks reads the lock file, or returns an empty table when there
// is none. A missing file is not an error here: every requirement then floats
// to whatever its constraint resolves to today, and every row says so through
// providerResult.Locked, which is the honest reading rather than a refusal to
// run.
func loadProviderLocks(path string) (providerLocks, error) {
	data, err := os.ReadFile(path) //nolint:gosec // checked-in artifact under live/, path from a flag
	if errors.Is(err, os.ErrNotExist) {
		return providerLocks{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var locks providerLocks
	if err := json.Unmarshal(data, &locks); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	if locks == nil {
		locks = providerLocks{}
	}
	return locks, nil
}

// versionSkew is issue #269's signal: what this entry's OWN provider
// version constraint would resolve to, set against the version -schemas
// mode otherwise pins for every entry, and where the two diverge in
// list-resource coverage for the types THIS entry actually needs listed.
//
// It is deliberately a field beside the pinned measurement, not a
// replacement for it: [runDiff] and every cross-entry ranking in this
// program keep comparing entries at the one pinned release, because that is
// the only way two entries - or two runs of the same entry - compare on
// equal footing. This is the separate, honestly-labeled place where "but
// THIS entry's own constraint would not actually work" gets recorded.
type versionSkew struct {
	// Provider and Constraint are the entry's own required_providers
	// requirement for [acquirer.pinned] - "hashicorp/aws" and "~> 5.64" for
	// the estate that motivated this - copied from [providerNeed] rather
	// than reconstructed, the same discipline [providerPin] documents.
	Provider   string `json:"provider"`
	Constraint string `json:"constraint,omitempty"`

	// PinnedVersion is the release every entry in this run was actually
	// measured against - [acquirer.pinnedVersion] - and OwnVersion is what
	// Constraint resolves to when nothing forces it elsewhere. OwnError is
	// set instead of OwnVersion when that resolution itself failed (an
	// unpublished range, no build for this platform); a failure here is
	// informational, not a Diverges reason on its own, since it says
	// nothing about list-resource coverage one way or the other.
	PinnedVersion string `json:"pinned_version"`
	OwnVersion    string `json:"own_version,omitempty"`
	OwnError      string `json:"own_error,omitempty"`

	// NeedsDiscovery is every type this entry declares that
	// [identity.ClassNeedsDiscovery] resolved - the types marker discovery
	// has to list to find, because their identity is server-assigned and
	// nothing in the configuration can substitute for a live lookup. It is
	// the set [internal/live/discovery.scanType] would attempt to list on a
	// real live-plan.
	NeedsDiscovery []string `json:"needs_discovery"`

	// MissingUnderOwn is the subset of NeedsDiscovery that OwnVersion
	// cannot list but PinnedVersion can - a real regression this entry's
	// own constraint would hit and the pinned measurement papers over, not
	// merely a type neither release can list (which is a fact about the
	// type, not about this entry's version constraint, and is visible
	// elsewhere without this field).
	MissingUnderOwn []string `json:"missing_under_own,omitempty"`

	// Diverges is true exactly when MissingUnderOwn is non-empty. Reported
	// as its own field rather than left for a caller to compute from the
	// slice's length, because a JSON consumer summing "did any entry
	// diverge" should not have to know that convention.
	Diverges bool `json:"diverges"`
}

// versionSkewFor computes [versionSkew] for one entry, or returns nil when
// there is nothing to compute: no declared type needs marker discovery at
// all, or this entry names no requirement for [acquirer.pinned].
//
// needs is the entry's own [providerNeeds]; rep is that entry's already-
// computed [check.Report], read here only for rep.Identities, the resolved
// identity for every instance the entry declares (see [check.Report.Identities]'s
// own doc for why that field exists to be read a second time like this).
func (a *acquirer) versionSkewFor(needs []providerNeed, rep check.Report) *versionSkew {
	if a.pinnedVersion == "" {
		return nil
	}

	var pinnedNeed providerNeed
	found := false
	for _, n := range needs {
		if n.Provider == a.pinned {
			pinnedNeed = n
			found = true
			break
		}
	}
	if !found {
		return nil
	}

	discoverySet := map[string]bool{}
	for _, res := range rep.Identities {
		if res.Class != identity.ClassNeedsDiscovery {
			continue
		}
		discoverySet[res.Addr.Resource.Resource.Type] = true
	}
	if len(discoverySet) == 0 {
		return nil
	}
	needsDiscovery := make([]string, 0, len(discoverySet))
	for t := range discoverySet {
		needsDiscovery = append(needsDiscovery, t)
	}
	sort.Strings(needsDiscovery)

	pinnedAcq := a.acquire(pinnedNeed)
	ownAcq := a.acquireOwn(pinnedNeed)

	skew := &versionSkew{
		Provider:       pinnedNeed.Provider.ForDisplay(),
		Constraint:     pinnedNeed.Constraint,
		PinnedVersion:  pinnedAcq.Version,
		OwnVersion:     ownAcq.Version,
		OwnError:       ownAcq.Error,
		NeedsDiscovery: needsDiscovery,
	}

	if ownAcq.Available && pinnedAcq.Available {
		for _, t := range needsDiscovery {
			if pinnedAcq.listTypes[t] && !ownAcq.listTypes[t] {
				skew.MissingUnderOwn = append(skew.MissingUnderOwn, t)
			}
		}
	}
	skew.Diverges = len(skew.MissingUnderOwn) > 0

	return skew
}
