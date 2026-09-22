// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// GitHub issue #1527. Every EKS root that authenticates with a bearer
// token takes it from data.aws_eks_cluster_auth, whose token attribute
// the AWS provider declares sensitive (schema 6.63.0), so the evaluated
// provider block hands this function a MARKED token. The same is true of
// a client certificate and key supplied through a `sensitive = true`
// variable, which is the documented way to supply them.
//
// kubernetesSweepAttrs used to leave every marked argument unread, which
// on those configurations meant the sweep dialled the cluster with no
// credential at all: corpus-eks-basic's live-plan reported "the cluster
// refused an anonymous request and this provider configuration supplies
// no credential" and all 28 kubernetes_* types read LIST_FAILED, while
// the provider itself - handed the same value over RPC, unmarked by
// internal/plugins/provider.go - read the cluster fine.
//
// The rule these arguments copied belongs to statelessProviders.region,
// and that function's own comment says where it stops: "Refused rather
// than unmarked, unlike the seams that put a value to a provider: this
// answer becomes an operator-facing hint string, and a secret does not
// belong in one." A credential argument is the other kind of seam. It
// goes into a rest.Config and out over TLS, and nothing renders it.
func TestKubernetesSweepAttrsReadsSensitiveCredentials(t *testing.T) {
	sensitive := func(v cty.Value) cty.Value { return v.Mark("sensitive") }
	cfg := func(token, cert, key cty.Value) cty.Value {
		return cty.ObjectVal(map[string]cty.Value{
			"host":               cty.StringVal("https://ABC.gr7.eu-west-1.eks.amazonaws.com"),
			"token":              token,
			"client_certificate": cert,
			"client_key":         key,
			"insecure":           cty.True,
		})
	}

	t.Run("a sensitive bearer token", func(t *testing.T) {
		got := kubernetesSweepAttrs(cfg(
			sensitive(cty.StringVal("k8s-aws-v1.aHR0cHM6")),
			cty.NullVal(cty.String),
			cty.NullVal(cty.String),
		), true)
		if got.Token != "k8s-aws-v1.aHR0cHM6" {
			t.Errorf("Token = %q, want the token: a sensitive token is still the credential the sweep must send", got.Token)
		}
		if got.Host != "https://ABC.gr7.eu-west-1.eks.amazonaws.com" {
			t.Errorf("Host = %q", got.Host)
		}
	})

	t.Run("a sensitive client certificate and key", func(t *testing.T) {
		got := kubernetesSweepAttrs(cfg(
			cty.NullVal(cty.String),
			sensitive(cty.StringVal("-----BEGIN CERTIFICATE-----")),
			sensitive(cty.StringVal("-----BEGIN RSA PRIVATE KEY-----")),
		), true)
		if got.ClientCertificate != "-----BEGIN CERTIFICATE-----" {
			t.Errorf("ClientCertificate = %q", got.ClientCertificate)
		}
		if got.ClientKey != "-----BEGIN RSA PRIVATE KEY-----" {
			t.Errorf("ClientKey = %q", got.ClientKey)
		}
	})
}
