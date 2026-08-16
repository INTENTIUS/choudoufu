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

// acquire runs, or replays from cache, one provider's schema read.
func (a *acquirer) acquire(need providerNeed) acquired {
	pinned := a.pinnedVersion != "" && need.Provider == a.pinned
	if pinned {
		// One cache slot for the pinned provider however the requiring
		// entry spelled its constraint: two entries with different ">= x.y"
		// floors get the identical release, so they must not fragment into
		// two installs.
		need.Constraint = ""
	}

	key := need.key()
	if res, ok := a.cache[key]; ok {
		return res
	}

	res := acquired{
		Provider:   need.Provider.ForDisplay(),
		Constraint: need.Constraint,
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

	schemas, err := pluginschema.ResourceTypes(context.Background(), req)
	if err != nil {
		res.Error = err.Error()
		a.cache[key] = res
		return res
	}

	res.Available = true
	res.schemas = schemas
	res.Types = len(schemas)
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
