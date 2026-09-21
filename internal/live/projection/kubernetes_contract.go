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

// ClusterTarget is which namespace, on which cluster, a report was answered
// against.
//
// GitHub issue #1448: `live-cluster` printed a verdict with nothing in it
// that named the cluster, so a report against whatever the ambient
// kubeconfig points at read exactly like a report against the cluster the
// estate keeps its records in. Server is what settles that, and it comes
// from the same resolved connection the calls went through rather than from
// re-reading the configuration.
// It carries only what the resolved connection knows. Whether that
// connection came from a record_store block, and which context the block
// named, are the caller's own arguments, so they stay the caller's to
// report: one fact, one place it is known from.
type ClusterTarget struct {
	// Namespace is the records namespace the findings are about.
	Namespace string
	// Server is the API server URL the calls went to.
	Server string
}

// VerifyCluster reads the cluster contract for a records namespace, with no
// store opened and no sentinel written: it is what `choudoufu live-cluster`
// runs (GitHub issue #1393), the way [VerifyBucket] is what `choudoufu
// live-bucket` runs. The client is built exactly the way [NewRecordStore]
// builds the store's own, through [kubesweep.RestConfig].
//
// rs is the estate's record_store "kubernetes" block, and its connection is
// what this reaches the cluster with. A nil rs is a caller with no such
// block in reach, and then the connection is the ambient one: KUBE_CONFIG_PATH
// or KUBECONFIG and the current context. The returned [ClusterTarget] says
// which of the two happened, because the two can be different clusters and a
// report that did not say so was a report about an unnamed one (#1448).
//
// namespace may be "", in which case it is derived from estate the way the
// store derives it. A namespace given here overrides that, and nothing else:
// the connection stays rs's.
//
// requiredVerbs is what the caller says its runs need on Secrets. Nil takes
// [staterecord.KubernetesRecordVerbs]; a CI plan job passes
// [staterecord.KubernetesPlanVerbs].
func VerifyCluster(ctx context.Context, rs *configs.LiveRecordStore, estate, namespace string, requiredVerbs []string) (findings []staterecord.Finding, target ClusterTarget, err error) {
	if rs == nil {
		rs = &configs.LiveRecordStore{Type: "kubernetes"}
	}
	target.Namespace = namespace
	if target.Namespace == "" {
		target.Namespace, err = kubernetesNamespaceFor(rs, estate)
		if err != nil {
			return nil, target, err
		}
	}
	cfg, err := kubesweep.RestConfig(kubernetesAttrs(rs))
	if err != nil {
		return nil, target, fmt.Errorf("reaching the cluster: %w", err)
	}
	target.Server = cfg.Host
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, target, fmt.Errorf("building a client: %w", err)
	}
	findings, err = staterecord.CheckClusterContract(ctx, clientset, staterecord.ClusterContractOptions{
		Namespace:     target.Namespace,
		Estate:        estate,
		RequiredVerbs: requiredVerbs,
		// Nothing has used this namespace, so its existence is the report's
		// to establish, and an absent one is reported in the store's own
		// words.
		NamespaceKnownToExist: false,
	})
	return findings, target, err
}
