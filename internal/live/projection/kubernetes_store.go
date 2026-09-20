// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// kubernetesNamespacePrefix starts the Kubernetes namespace an estate's
// records go in when the record_store block names none.
const kubernetesNamespacePrefix = "tofu-records-"

// KubernetesRecordNamespace is the Kubernetes namespace record_store
// "kubernetes" keeps estate's records in when the block sets no "namespace":
// "tofu-records-" and the estate name.
//
// Derived from the estate rather than fixed, because the namespace IS the read
// isolation (issue #1392, decision 2). RBAC cannot condition on a label and
// admission never sees a get or a list, so one shared namespace would mean
// every estate's records readable by every estate's identity. A default that
// is already one namespace per estate makes the isolating arrangement the one
// an operator gets without asking for it.
//
// It is not created here or anywhere else in this fork. Creating a namespace
// is a cluster-admin act and granting an identity Secrets in it is the other
// half of the same act; a store that made its own namespace on first write
// would be deciding the boundary rather than using it. An absent one is
// refused by name (staterecord.NamespaceMissingError) with the kubectl line.
func KubernetesRecordNamespace(estate string) string {
	return kubernetesNamespacePrefix + estate
}

// kubernetesNamespaceFor is the namespace a record_store block resolves to:
// its own "namespace" argument, or [KubernetesRecordNamespace].
//
// A derived name can fail where a written one cannot: an estate name may be
// 128 characters (#1396) and a namespace is a DNS-1123 label capped at 63, so
// "tofu-records-" plus an estate name past 50 characters is not a namespace
// this cluster will accept. That is refused here, by name, naming the
// argument that settles it - not sent to the API server to come back as a
// generic Invalid.
func kubernetesNamespaceFor(rs *configs.LiveRecordStore, estate string) (string, error) {
	if rs.NamespaceSet {
		return rs.Namespace, nil
	}
	if estate == "" {
		return "", fmt.Errorf("record_store \"kubernetes\": this estate has no name to derive a records namespace from, so the \"namespace\" argument has to name one; a live block with no \"estate\" argument takes its name from the tofu-estate tags, which is not known when the store is opened")
	}
	ns := KubernetesRecordNamespace(estate)
	if errs := validation.IsDNS1123Label(ns); len(errs) > 0 {
		return "", fmt.Errorf(
			"record_store \"kubernetes\": the default records namespace for estate %q is %q, which is not a Kubernetes namespace name (%s); set the \"namespace\" argument to a name this cluster accepts",
			estate, ns, strings.Join(errs, "; "))
	}
	return ns, nil
}

// newKubernetesStore builds the "kubernetes" backend's store.
//
// The connection arguments go through [kubesweep.RestConfig], which is this
// fork's one copy of the kubernetes provider's connection precedence -
// in_cluster_config, then a kubeconfig from config_path/config_paths or the
// KUBE_* and KUBECONFIG environment variables with the context overrides, then
// host, token and the TLS arguments layered on. The sweep already needed it
// (#1114), the stock backend has its own copy bound to the legacy
// helper/schema ResourceData and reachable only through a backend
// configuration, and a third copy here would be one more place for the
// precedence to drift.
func newKubernetesStore(rs *configs.LiveRecordStore, estate string) (staterecord.Store, error) {
	ns, err := kubernetesNamespaceFor(rs, estate)
	if err != nil {
		return nil, err
	}
	cfg, err := kubesweep.RestConfig(kubernetesAttrs(rs))
	if err != nil {
		return nil, fmt.Errorf("record_store \"kubernetes\": %w", err)
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("record_store \"kubernetes\": building a client: %w", err)
	}
	store, err := staterecord.NewKubernetesStore(staterecord.KubernetesConfig{
		Secrets:   clientset.CoreV1().Secrets(ns),
		Namespace: ns,
		// Empty on purpose, the same as the s3 backend's: the namespace a
		// record lives under is carried by the KEY. See backendKeyPrefix.
		KeyPrefix: backendKeyPrefix,
		Estate:    estate,
	})
	if err != nil {
		return nil, fmt.Errorf("record_store \"kubernetes\": %w", err)
	}
	return store, nil
}

// kubernetesAttrs maps the decoded block onto the sweep's connection
// arguments. It is apart from [newKubernetesStore] so the mapping can be
// checked without a cluster.
func kubernetesAttrs(rs *configs.LiveRecordStore) kubesweep.Attrs {
	k := rs.Kubernetes
	attrs := kubesweep.Attrs{
		InCluster:             k.InCluster,
		ConfigPath:            k.ConfigPath,
		ConfigPaths:           k.ConfigPaths,
		ConfigContext:         k.ConfigContext,
		ConfigContextAuthInfo: k.ConfigContextAuthInfo,
		ConfigContextCluster:  k.ConfigContextCluster,
		Host:                  k.Host,
		Token:                 k.Token,
		Insecure:              k.Insecure,
		ClusterCACertificate:  k.ClusterCACertificate,
		ClientCertificate:     k.ClientCertificate,
		ClientKey:             k.ClientKey,
	}
	if k.Exec != nil {
		attrs.Exec = &kubesweep.ExecCredential{
			APIVersion: k.Exec.APIVersion,
			Command:    k.Exec.Command,
			Args:       k.Exec.Args,
			Env:        k.Exec.Env,
		}
	}
	return attrs
}
