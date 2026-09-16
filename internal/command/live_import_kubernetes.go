// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/liveimport"
)

// This file is live-import's half of GitHub issue #1109: the cluster
// client a manifest-shape resource's one-label merge patch goes through,
// supplied to internal/live/liveimport through its [liveimport.Clusters]
// seam.
//
// It is the same client [statelessProviders.kubernetesClient] builds for
// the estate sweep (#1065) and the server-side dry run (#1081 item 3),
// from the same provider block's own connection arguments, so a migration
// writes under exactly the credential a plan reads under and the
// admission policy of live/kubernetes/estate-boundary.yaml judges the
// write the way it judges kubectl's.

var _ liveimport.Clusters = (*statelessProviders)(nil)

// labelPatcher is the cached client per provider configuration, and the
// error that stood in the way of building one. Cached because a
// migration asks per resource INSTANCE and building a client costs two
// discovery round trips; the error is cached for the same reason, so a
// cluster that cannot be reached is not re-dialled once per custom
// resource in the state file.
type labelPatcher struct {
	client *kubesweep.Client
	err    error
}

// LabelPatcher implements [liveimport.Clusters].
//
// The nil-interface trap is the reason this returns the concrete client
// through an explicit branch rather than assigning it straight into the
// interface: a (*kubesweep.Client)(nil) in a kubesweep.LabelPatcher is not
// a nil interface, and the caller's "no client, say why" path would never
// run.
func (p *statelessProviders) LabelPatcher(ctx context.Context, addr addrs.AbsProviderConfig) (kubesweep.LabelPatcher, error) {
	key := providerCacheKey(addr)

	p.mu.Lock()
	got, cached := p.kubePatchers[key]
	p.mu.Unlock()
	if !cached {
		client, _, _, schemaDiags, err := p.kubernetesClient(ctx, addr)
		switch {
		case schemaDiags.HasErrors():
			got = labelPatcher{err: schemaDiags.Err()}
		case err != nil:
			got = labelPatcher{err: err}
		default:
			got = labelPatcher{client: client}
		}
		p.mu.Lock()
		if p.kubePatchers == nil {
			p.kubePatchers = map[string]labelPatcher{}
		}
		p.kubePatchers[key] = got
		p.mu.Unlock()
	}

	if got.client == nil {
		return nil, got.err
	}
	return got.client, nil
}
