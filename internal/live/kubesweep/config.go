// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubesweep

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

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
	// Exec is the provider block's exec block, the way every EKS root
	// authenticates (GitHub issue #1114): host and cluster_ca_certificate
	// name the cluster and a credential plugin - `aws eks get-token`, or
	// aws-iam-authenticator - produces the bearer token for it. Nil when
	// the block declares none, which leaves whatever the kubeconfig or
	// the explicit token supplied.
	//
	// A kubeconfig whose user is an exec plugin needs nothing here: it
	// arrives through clientcmd, which sets restclient.Config.ExecProvider
	// itself, and client-go's rest package links the exec credential
	// provider unconditionally. This field is for the provider block's
	// own exec block, which reaches no kubeconfig at all.
	Exec *ExecCredential
}

// ExecCredential is the hashicorp/kubernetes provider block's exec block:
// a credential plugin speaking the client.authentication.k8s.io
// ExecCredential protocol, run on every request whose credential has
// expired. The credential it produces is the caller's own, which is what
// keeps the admission policy's "the fence binds the credential" property
// true of the sweep as well as of the provider.
type ExecCredential struct {
	// APIVersion is the exec block's api_version:
	// client.authentication.k8s.io/v1beta1 for an EKS root written any
	// time in the last five years, /v1 for a newer one.
	APIVersion string
	// Command is the plugin's executable, resolved on PATH by client-go.
	Command string
	// Args are the plugin's arguments, in order.
	Args []string
	// Env are environment variables set for the plugin on top of this
	// process's own, in no particular order.
	Env map[string]string
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
	if a.Exec != nil && a.Exec.Command != "" {
		cfg.ExecProvider = execProvider(a.Exec)
	}
	return cfg
}

// execProvider is the provider block's exec block as client-go's own
// ExecConfig.
//
// InteractiveMode has no counterpart in the provider block, and client-go
// rejects an ExecConfig that leaves it empty at the first request
// ("unknown interactiveMode"), so one has to be chosen here. IfAvailable
// is client-go's own default for a v1beta1 kubeconfig exec user
// (clientcmd/api/v1's SetDefaults_ExecConfig), and is the permissive
// direction: a plugin that never reads standard input - `aws eks
// get-token` is one - behaves identically either way, and one that does
// ask gets the terminal rather than failing. Never would refuse a
// configuration that works for the provider itself.
func execProvider(e *ExecCredential) *clientcmdapi.ExecConfig {
	out := &clientcmdapi.ExecConfig{
		APIVersion:      e.APIVersion,
		Command:         e.Command,
		Args:            append([]string(nil), e.Args...),
		InteractiveMode: clientcmdapi.IfAvailableExecInteractiveMode,
	}
	names := make([]string, 0, len(e.Env))
	for k := range e.Env {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		out.Env = append(out.Env, clientcmdapi.ExecEnvVar{Name: k, Value: e.Env[k]})
	}
	return out
}
