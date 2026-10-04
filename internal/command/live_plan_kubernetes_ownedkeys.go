// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"
	"sync"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/projection"
)

// The command layer's half of GitHub issue #1211: the one cluster read
// that tells a live plan which metadata.labels and
// metadata.annotations keys its own field manager owns on a live
// kubernetes_manifest object.
//
// This is the SAFETY RAIL, never the source. What licenses a removal is
// the estate's record of what the configuration last declared; this only
// ever declines one of those candidates, because managedFields answers
// "who wrote this field" and the question is "did this configuration
// declare it" - see internal/live/kubesweep/managedkeys.go for the
// measurement that separated the two. The projection consults it only
// when a candidate exists, so a converged estate makes no call here at
// all.
//
// It lives here for the same reason the server-side dry run does: the
// cluster client is the marker sweep's, built from the provider block at
// the plan's first cluster contact and kept on projectionProviders, and
// internal/live/projection has no Kubernetes client and should not grow
// one. The projection declares the question
// ([projection.ManifestOwnedKeysFunc]); this answers it.

// liveManifestOwnedKeys builds the hook, or returns nil when there
// is nothing to build it from.
//
// Nil is not a silent degradation: the projection warns whenever it has
// a removal candidate and no hook to confirm it with, and a
// non-Kubernetes estate never reaches the hook at all because the
// projection binds it only for a manifest-shaped type
// ([projection.Options.ManifestOwnedKeys]).
//
// The sweepers are looked up per call rather than captured, because the
// marker sweep builds a cluster client at its FIRST contact with a
// cluster and this hook is built before the sweep has necessarily made
// one.
func liveManifestOwnedKeys(config *configs.Config, provs *projectionProviders) projection.ManifestOwnedKeysFunc {
	if config == nil || provs == nil {
		return nil
	}
	managers := &manifestFieldManagers{config: config, byAddr: map[string]manifestFieldManagerResult{}}
	return func(ctx context.Context, req projection.ManifestOwnedKeysRequest) (projection.ManifestOwnedKeys, error) {
		var out projection.ManifestOwnedKeys

		sweeper := provs.kubernetesSweepers()[providerCacheKey(req.Provider)]
		if sweeper == nil {
			return out, fmt.Errorf("the marker sweep built no cluster client for provider %s, so metadata.managedFields could not be read", req.Provider)
		}
		// Sweeper is the listing interface; reading one object back is
		// LabelPatcher's. *kubesweep.Client is both, and a test double
		// that is only one of them says so here rather than by
		// answering the wrong question.
		reader, ok := sweeper.(kubesweep.LabelPatcher)
		if !ok {
			return out, fmt.Errorf("the cluster client for provider %s cannot read a single object back, only list", req.Provider)
		}

		manager, err := managers.forAddr(ctx, req.Addr)
		if err != nil {
			return out, err
		}

		ref := kubesweep.ObjectRef{
			APIVersion: req.APIVersion,
			Kind:       req.Kind,
			Namespace:  req.Namespace,
			Name:       req.Name,
		}
		obj, found, err := reader.ReadObject(ctx, ref)
		if err != nil {
			return out, fmt.Errorf("reading %s back for its metadata.managedFields: %w", ref, err)
		}
		if !found {
			// The provider read this object moments ago, so an absence
			// here is a deletion that happened in between - a real
			// answer about the cluster, and not one this hook may paper
			// over by reporting that we own nothing.
			return out, fmt.Errorf("%s no longer exists; it was deleted between the provider's read and this one", ref)
		}
		out.Keys, out.ManagedFieldsPresent = kubesweep.ManagedMetadataKeys(obj, manager, markers.ManifestComputedMetadataAttrs)
		return out, nil
	}
}

// manifestFieldManagers resolves, and remembers, the server-side-apply
// field manager each manifest-shaped resource block declares.
//
// Remembered because the hook runs once per instance and the answer is
// per BLOCK: an expanded block asks the same question of the same
// expression for every one of its instances. Guarded by a mutex because
// the projection's read pass calls the hook from several goroutines at
// once.
type manifestFieldManagers struct {
	config *configs.Config

	mu     sync.Mutex
	byAddr map[string]manifestFieldManagerResult
}

type manifestFieldManagerResult struct {
	name string
	err  error
}

func (m *manifestFieldManagers) forAddr(ctx context.Context, addr addrs.AbsResourceInstance) (string, error) {
	key := addr.ContainingResource().String()
	m.mu.Lock()
	defer m.mu.Unlock()
	if got, ok := m.byAddr[key]; ok {
		return got.name, got.err
	}
	name, err := manifestFieldManagerFor(ctx, m.config, addr)
	m.byAddr[key] = manifestFieldManagerResult{name: name, err: err}
	return name, err
}

// manifestFieldManagerFor is [identity.ManifestFieldManager], shared with
// live-mv's manifest rename (#1720) so the two can never disagree on which
// manager a block writes under.
//
// Honouring the override is not a nicety. managedFields is keyed by
// manager NAME, so asking about "Terraform" on an object a block wrote
// under "my-pipeline" finds no entry, and the caller would then be told
// this estate owns no keys at all - the wrong answer, arrived at
// confidently. An unresolvable name is an error for the same reason, and
// the caller turns it into the warning that names the key that will not
// be removed.
func manifestFieldManagerFor(ctx context.Context, config *configs.Config, addr addrs.AbsResourceInstance) (string, error) {
	return identity.ManifestFieldManager(ctx, config, addr)
}
