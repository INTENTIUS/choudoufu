// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// What the cluster store needs that the shared open path does not give it:
// the report `choudoufu live-cluster` runs against a namespace named
// directly, with no store opened. GitHub issue #1393. Opening a store,
// asserting its contract on first contact and the sentinel handshake are
// store.go's, written once for every store (#1442).

// VerifyCluster reads the cluster contract for a records namespace named
// directly, with no store opened and no sentinel written: it is what
// `choudoufu live-cluster` runs (GitHub issue #1393), the way
// [VerifyBucket] is what `choudoufu live-bucket` runs. The client is built
// exactly the way [NewRecordStore] builds the store's own, through
// [kubesweep.RestConfig].
//
// namespace may be "", in which case it is derived from estate the way the
// store derives it.
//
// requiredVerbs is what the caller says its runs need on Secrets. Nil takes
// [staterecord.KubernetesRecordVerbs]; a CI plan job passes
// [staterecord.KubernetesPlanVerbs].
func VerifyCluster(ctx context.Context, rs *configs.LiveRecordStore, estate, namespace string, requiredVerbs []string) (findings []staterecord.Finding, ns string, err error) {
	if rs == nil {
		rs = &configs.LiveRecordStore{Type: "kubernetes"}
	}
	ns = namespace
	if ns == "" {
		ns, err = kubernetesNamespaceFor(rs, estate)
		if err != nil {
			return nil, "", err
		}
	}
	cfg, err := kubesweep.RestConfig(kubernetesAttrs(rs))
	if err != nil {
		return nil, ns, fmt.Errorf("reaching the cluster: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, ns, fmt.Errorf("building a client: %w", err)
	}
	findings, err = staterecord.CheckClusterContract(ctx, clientset, staterecord.ClusterContractOptions{
		Namespace:     ns,
		Estate:        estate,
		RequiredVerbs: requiredVerbs,
		// Nothing has used this namespace, so its existence is the report's
		// to establish, and an absent one is reported in the store's own
		// words.
		NamespaceKnownToExist: false,
	})
	return findings, ns, err
}
