// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"testing"

	restclient "k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// TestKubernetesControlPlaneCarriesTheBlock is GitHub issue #1524's mapping:
// every argument of the record_store block's control_plane block reaches the
// contract, and an EKS connection with no block is recognised from its exec
// plugin and host.
func TestKubernetesControlPlaneCarriesTheBlock(t *testing.T) {
	rs := &configs.LiveRecordStore{Type: "kubernetes"}
	rs.Kubernetes.ControlPlane = &configs.LiveRecordStoreControlPlane{
		Provider: "aks", Name: "prod", ResourceGroup: "rg", SubscriptionID: "sub",
	}
	got := kubernetesControlPlane(rs, &restclient.Config{Host: "https://prod-dns-1.hcp.westeurope.azmk8s.io:443"})
	if got == nil {
		t.Fatal("the declared control plane was dropped")
	}
	if got.Provider != staterecord.ControlPlaneAKS || got.Name != "prod" || got.ResourceGroup != "rg" || got.SubscriptionID != "sub" {
		t.Errorf("got %+v", got)
	}
}

func TestKubernetesControlPlaneInfersEKS(t *testing.T) {
	rs := &configs.LiveRecordStore{Type: "kubernetes"}
	cfg := &restclient.Config{
		Host: "https://ABC.gr7.us-west-2.eks.amazonaws.com",
		ExecProvider: &clientcmdapi.ExecConfig{
			Command: "aws",
			Args:    []string{"--region", "us-west-2", "eks", "get-token", "--cluster-name", "prod"},
		},
	}
	got := kubernetesControlPlane(rs, cfg)
	if got == nil || got.Provider != staterecord.ControlPlaneEKS || got.Name != "prod" || got.Region != "us-west-2" {
		t.Fatalf("got %+v, want EKS cluster prod in us-west-2", got)
	}
}

// A kind cluster, or anything else that is not a named or recognisable
// managed control plane, keeps the API server Pod reading.
func TestKubernetesControlPlaneNoneForKind(t *testing.T) {
	rs := &configs.LiveRecordStore{Type: "kubernetes"}
	if got := kubernetesControlPlane(rs, &restclient.Config{Host: "https://127.0.0.1:6443"}); got != nil {
		t.Fatalf("a kind connection named a managed control plane: %+v", got)
	}
}
