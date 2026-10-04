// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
)

// GitHub issue #1656: the cluster client an untag release of a
// manifest-shape orphan goes through is the sweep's client for the SAME
// provider configuration the release runs through, and never another's.

type untagStubSweeper struct{ kubesweep.Sweeper }

type untagStubReleaser struct {
	kubesweep.Sweeper
	kubesweep.LabelReleaser
}

func TestLiveUntagClusterIsTheReleasingConfigurationsClient(t *testing.T) {
	k8s := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes")}
	other := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes"), Alias: "other"}
	aws := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")}

	mine := &untagStubReleaser{}
	sweepers := map[string]kubesweep.Sweeper{
		providerCacheKey(k8s):   mine,
		providerCacheKey(other): &untagStubReleaser{},
	}
	if got := liveUntagCluster(sweepers, k8s); got != kubesweep.LabelReleaser(mine) {
		t.Errorf("released through %v, want the kubernetes configuration's own client", got)
	}
	if got := liveUntagCluster(sweepers, aws); got != nil {
		t.Errorf("a configuration with no client borrowed one: %v", got)
	}
	if got := liveUntagCluster(nil, k8s); got != nil {
		t.Errorf("no sweepers produced a client: %v", got)
	}
	listOnly := map[string]kubesweep.Sweeper{providerCacheKey(k8s): &untagStubSweeper{}}
	if got := liveUntagCluster(listOnly, k8s); got != nil {
		t.Errorf("a list-only sweeper was used as a releaser: %v", got)
	}
	typedNil := map[string]kubesweep.Sweeper{providerCacheKey(k8s): (*kubesweep.Client)(nil)}
	if got := liveUntagCluster(typedNil, k8s); got != nil {
		t.Errorf("a typed-nil client came back as a non-nil releaser")
	}
}
