// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package managedk8s asks a managed Kubernetes control plane's provider -
// EKS, GKE, AKS - what the cluster itself cannot say: whether its API server
// encrypts Secrets before they reach etcd (GitHub issue #1524).
//
// record_store "kubernetes" checks encryption at rest on first contact
// (internal/live/staterecord/kubernetescontract.go). On kind and kubeadm the
// answer is a flag on the API server's static Pod. On a managed control plane
// there is no such Pod at any permission level, and the setting lives on the
// provider's description of the cluster instead. [Reader] reads that
// description with the ambient cloud credentials, the same chains every other
// client in this fork uses, and hands back a neutral
// [staterecord.ControlPlaneEncryption]; what it means for the contract is
// staterecord's to decide.
//
// Nothing here writes. Each provider is asked one read-only question:
// eks:DescribeCluster, container.clusters.get, and
// Microsoft.ContainerService/managedClusters/read.
package managedk8s

import (
	"context"
	"fmt"
	"net/http"

	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// Reader is the real [staterecord.ControlPlaneReader]. The zero value asks
// the providers' public endpoints with the ambient credentials; the fields
// exist so a test can point each provider at an httptest server.
type Reader struct {
	// HTTPClient is used for every request. Nil takes http.DefaultClient.
	HTTPClient *http.Client

	// EKSEndpoint, GKEEndpoint and AKSEndpoint replace each provider's
	// public API base URL. Empty takes the real one (for EKS, also
	// AWS_ENDPOINT_URL_EKS and AWS_ENDPOINT_URL, the SDK's own overrides).
	EKSEndpoint string
	GKEEndpoint string
	AKSEndpoint string

	// eksCredentials, gkeNoAuth and aksToken replace the credential chains
	// in tests. Nil or false takes the real chain.
	eksCredentials func(ctx context.Context, region string) (eksAuth, error)
	gkeNoAuth      bool
	aksToken       func(ctx context.Context) (string, error)
}

var _ staterecord.ControlPlaneReader = (*Reader)(nil)

// SecretsEncryption implements [staterecord.ControlPlaneReader].
func (r *Reader) SecretsEncryption(ctx context.Context, cp staterecord.ManagedControlPlane) (staterecord.ControlPlaneEncryption, error) {
	switch cp.Provider {
	case staterecord.ControlPlaneEKS:
		return r.eks(ctx, cp)
	case staterecord.ControlPlaneGKE:
		return r.gke(ctx, cp)
	case staterecord.ControlPlaneAKS:
		return r.aks(ctx, cp)
	}
	return staterecord.ControlPlaneEncryption{}, fmt.Errorf("managedk8s: no reader for control plane provider %q", cp.Provider)
}

func (r *Reader) httpClient() *http.Client {
	if r.HTTPClient != nil {
		return r.HTTPClient
	}
	return http.DefaultClient
}

// appendNonEmpty appends each of vs that is not empty.
func appendNonEmpty(dst []string, vs ...string) []string {
	for _, v := range vs {
		if v != "" {
			dst = append(dst, v)
		}
	}
	return dst
}
