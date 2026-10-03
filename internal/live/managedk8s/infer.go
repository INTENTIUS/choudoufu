// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package managedk8s

import (
	"net/url"
	"path/filepath"
	"strings"

	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// ControlPlaneFor settles which managed control plane, if any, a record_store
// "kubernetes" connection reaches.
//
// declared is the record_store block's control_plane block, already in
// staterecord's vocabulary, or nil. It always wins.
//
// With none declared, an EKS cluster is recognised from the connection
// itself, because every EKS kubeconfig already names its cluster: the exec
// credential plugin is `aws eks get-token --cluster-name <name>` or
// `aws-iam-authenticator token -i <name>`, and the API server host is
// `<id>.<suffix>.<region>.eks.amazonaws.com`. Both have to be present - the
// cluster name from the plugin, the EKS host from the connection - and the
// provider's own answer is still believed only when its endpoint is that
// host (internal/live/staterecord's endpoint check), so a wrong guess comes
// out NOT CHECKED and never a pass.
//
// GKE and AKS are not inferred. gke-gcloud-auth-plugin and kubelogin carry
// no cluster coordinates, and the cluster name a kubeconfig context carries
// is a gcloud and az CLI convention an operator can rename. Those need the
// block.
//
// host is rest.Config's Host; execCommand and execArgs are its ExecProvider's
// Command and Args, empty where there is none.
func ControlPlaneFor(declared *staterecord.ManagedControlPlane, host, execCommand string, execArgs []string) *staterecord.ManagedControlPlane {
	if declared != nil {
		cp := *declared
		if cp.Source == "" {
			cp.Source = "named by the record_store block's control_plane block"
		}
		if cp.Provider == staterecord.ControlPlaneEKS && cp.Region == "" {
			cp.Region = eksRegionFromHost(host)
		}
		return &cp
	}
	region := eksRegionFromHost(host)
	if region == "" {
		return nil
	}
	name, argRegion := eksClusterFromExec(execCommand, execArgs)
	if name == "" {
		return nil
	}
	if argRegion != "" {
		region = argRegion
	}
	return &staterecord.ManagedControlPlane{
		Provider: staterecord.ControlPlaneEKS,
		Name:     name,
		Region:   region,
		Source:   "inferred from the exec credential plugin's cluster name and the API server's EKS host",
	}
}

// eksRegionFromHost is the region in an EKS API server host
// (`<id>.<suffix>.<region>.eks.amazonaws.com`), or "" when host is not one.
func eksRegionFromHost(host string) string {
	h := host
	if !strings.Contains(h, "://") {
		h = "https://" + h
	}
	u, err := url.Parse(h)
	if err != nil {
		return ""
	}
	name := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	for _, suffix := range []string{".eks.amazonaws.com", ".eks.amazonaws.com.cn"} {
		if rest, ok := strings.CutSuffix(name, suffix); ok {
			labels := strings.Split(rest, ".")
			if len(labels) >= 2 {
				return labels[len(labels)-1]
			}
		}
	}
	return ""
}

// eksClusterFromExec reads the cluster name, and the region when given, out
// of the two exec plugins every EKS kubeconfig uses.
//
//	aws [--region R] eks get-token --cluster-name NAME [--region R] ...
//	aws-iam-authenticator token -i NAME
func eksClusterFromExec(command string, args []string) (name, region string) {
	base := filepath.Base(command)
	switch {
	case base == "aws" || base == "aws.exe" || base == "aws.cmd":
		if !containsSeq(args, "eks", "get-token") {
			return "", ""
		}
		return flagValue(args, "--cluster-name"), flagValue(args, "--region")
	case strings.HasPrefix(base, "aws-iam-authenticator"):
		n := flagValue(args, "-i")
		if n == "" {
			n = flagValue(args, "--cluster-id")
		}
		return n, flagValue(args, "--region")
	}
	return "", ""
}

// flagValue is the value of flag in args, in either spelling
// ("--flag value" or "--flag=value"), or "".
func flagValue(args []string, flag string) string {
	for i, a := range args {
		if v, ok := strings.CutPrefix(a, flag+"="); ok {
			return v
		}
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// containsSeq reports whether args holds a, immediately followed by b.
func containsSeq(args []string, a, b string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == a && args[i+1] == b {
			return true
		}
	}
	return false
}
