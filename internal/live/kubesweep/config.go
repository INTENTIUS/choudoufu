// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubesweep

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mitchellh/go-homedir"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// Attrs are the hashicorp/kubernetes provider block's connection arguments
// this sweep understands, read off the evaluated provider configuration
// by the caller. They follow the provider's own precedence
// (internal/backend/remote-state/kubernetes has the same loader for the
// state backend): in_cluster_config first; otherwise a kubeconfig named by
// config_path, config_paths, KUBE_CONFIG_PATH, KUBE_CONFIG_PATHS or
// KUBECONFIG, with config_context and its two overrides; and host, token
// and the TLS arguments layered on top of whatever the kubeconfig gave.
type Attrs struct {
	InCluster             bool
	ConfigPath            string
	ConfigPaths           []string
	ConfigContext         string
	ConfigContextAuthInfo string
	ConfigContextCluster  string
	Host                  string
	Token                 string
	Insecure              bool
	ClusterCACertificate  string
	ClientCertificate     string
	ClientKey             string
}

// RestConfig builds the client configuration the sweep connects with.
func RestConfig(a Attrs) (*restclient.Config, error) {
	if a.InCluster {
		cfg, err := restclient.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("in_cluster_config: %w", err)
		}
		return overlay(cfg, a), nil
	}

	var paths []string
	switch {
	case a.ConfigPath != "":
		paths = []string{a.ConfigPath}
	case len(a.ConfigPaths) > 0:
		paths = a.ConfigPaths
	case os.Getenv("KUBE_CONFIG_PATH") != "":
		paths = []string{os.Getenv("KUBE_CONFIG_PATH")}
	case os.Getenv("KUBE_CONFIG_PATHS") != "":
		paths = filepath.SplitList(os.Getenv("KUBE_CONFIG_PATHS"))
	case os.Getenv("KUBECONFIG") != "":
		paths = filepath.SplitList(os.Getenv("KUBECONFIG"))
	}
	loader := &clientcmd.ClientConfigLoadingRules{}
	var expanded []string
	for _, p := range paths {
		e, err := homedir.Expand(p)
		if err != nil {
			return nil, fmt.Errorf("kubeconfig path %q: %w", p, err)
		}
		expanded = append(expanded, e)
	}
	switch len(expanded) {
	case 0:
		if a.Host == "" {
			return nil, fmt.Errorf("no kubeconfig: set config_path, config_paths, KUBE_CONFIG_PATH or KUBECONFIG, or host and token")
		}
	case 1:
		loader.ExplicitPath = expanded[0]
	default:
		loader.Precedence = expanded
	}

	overrides := &clientcmd.ConfigOverrides{}
	if a.ConfigContext != "" {
		overrides.CurrentContext = a.ConfigContext
	}
	if a.ConfigContextAuthInfo != "" || a.ConfigContextCluster != "" {
		overrides.Context = clientcmdapi.Context{AuthInfo: a.ConfigContextAuthInfo, Cluster: a.ConfigContextCluster}
	}

	var cfg *restclient.Config
	if len(expanded) > 0 {
		c, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loader, overrides).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("kubeconfig: %w", err)
		}
		cfg = c
	} else {
		cfg = &restclient.Config{}
	}
	return overlay(cfg, a), nil
}

// overlay applies the explicit connection arguments over whatever the
// kubeconfig or the in-cluster environment supplied.
func overlay(cfg *restclient.Config, a Attrs) *restclient.Config {
	if a.Host != "" {
		cfg.Host = a.Host
	}
	if a.Token != "" {
		cfg.BearerToken = a.Token
	}
	if a.Insecure {
		cfg.TLSClientConfig.Insecure = true
		cfg.TLSClientConfig.CAData = nil
		cfg.TLSClientConfig.CAFile = ""
	}
	if a.ClusterCACertificate != "" {
		cfg.TLSClientConfig.CAData = []byte(a.ClusterCACertificate)
	}
	if a.ClientCertificate != "" {
		cfg.TLSClientConfig.CertData = []byte(a.ClientCertificate)
	}
	if a.ClientKey != "" {
		cfg.TLSClientConfig.KeyData = []byte(a.ClientKey)
	}
	return cfg
}
