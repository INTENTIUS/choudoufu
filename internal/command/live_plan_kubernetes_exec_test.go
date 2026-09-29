// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"reflect"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/kubesweep"
)

// GitHub issue #1114: the provider block's exec block is how every EKS
// root authenticates, and nothing read it before. These drive
// kubernetesSweepAttrs over the evaluated provider configuration the way
// statelessProviders.kubernetesClient hands it one.

// execBlockType is the exec block as the hashicorp/kubernetes provider's
// schema implies it: a nested block of at most one, so a list of one
// object with the block's four arguments.
func execBlockType() cty.Type {
	return cty.List(cty.Object(map[string]cty.Type{
		"api_version": cty.String,
		"command":     cty.String,
		"args":        cty.List(cty.String),
		"env":         cty.Map(cty.String),
	}))
}

// eksProviderConfig is the shape of a real EKS root's provider block:
// the endpoint and CA off data.aws_eks_cluster and a credential plugin.
func eksProviderConfig(exec cty.Value) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"host":                   cty.StringVal("https://ABC.gr7.eu-west-1.eks.amazonaws.com"),
		"cluster_ca_certificate": cty.StringVal("-----BEGIN CERTIFICATE-----"),
		"token":                  cty.NullVal(cty.String),
		"config_path":            cty.NullVal(cty.String),
		"insecure":               cty.NullVal(cty.Bool),
		"exec":                   exec,
	})
}

func execBlock(apiVersion, command string, args cty.Value, env cty.Value) cty.Value {
	return cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
		"api_version": cty.StringVal(apiVersion),
		"command":     cty.StringVal(command),
		"args":        args,
		"env":         env,
	})})
}

func TestKubernetesSweepAttrsReadsTheExecBlock(t *testing.T) {
	val := eksProviderConfig(execBlock(
		"client.authentication.k8s.io/v1beta1",
		"aws",
		cty.ListVal([]cty.Value{
			cty.StringVal("eks"), cty.StringVal("get-token"),
			cty.StringVal("--cluster-name"), cty.StringVal("prod"),
		}),
		cty.MapVal(map[string]cty.Value{"AWS_PROFILE": cty.StringVal("ops")}),
	))

	got := kubernetesSweepAttrs(val, true)
	if got.Host != "https://ABC.gr7.eu-west-1.eks.amazonaws.com" {
		t.Errorf("Host = %q", got.Host)
	}
	want := &kubesweep.ExecCredential{
		APIVersion: "client.authentication.k8s.io/v1beta1",
		Command:    "aws",
		Args:       []string{"eks", "get-token", "--cluster-name", "prod"},
		Env:        map[string]string{"AWS_PROFILE": "ops"},
	}
	if !reflect.DeepEqual(got.Exec, want) {
		t.Errorf("Exec = %+v, want %+v", got.Exec, want)
	}
}

func TestKubernetesSweepAttrsExecBlockAbsent(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  cty.Value
	}{
		{
			name: "no exec attribute at all",
			val: cty.ObjectVal(map[string]cty.Value{
				"host": cty.StringVal("https://h"),
			}),
		},
		{name: "the block is not declared", val: eksProviderConfig(cty.ListValEmpty(execBlockType().ElementType()))},
		{name: "a null block list", val: eksProviderConfig(cty.NullVal(execBlockType()))},
		{name: "an unknown block list", val: eksProviderConfig(cty.UnknownVal(execBlockType()))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := kubernetesSweepAttrs(tc.val, true).Exec; got != nil {
				t.Errorf("Exec = %+v, want nil", got)
			}
		})
	}
}

// A marked argument is left unread, the rule every other argument here
// follows. The block is dropped whole rather than in part: running a
// credential plugin with an argument missing from the middle of its
// argv would ask it for a different credential, and running one whose
// command came from somewhere else is worse still.
func TestKubernetesSweepAttrsDropsAMarkedExecBlock(t *testing.T) {
	sensitive := func(v cty.Value) cty.Value { return v.Mark("sensitive") }
	full := cty.ListVal([]cty.Value{
		cty.StringVal("eks"), cty.StringVal("get-token"),
	})
	env := cty.MapVal(map[string]cty.Value{"AWS_PROFILE": cty.StringVal("ops")})

	for _, tc := range []struct {
		name string
		val  cty.Value
	}{
		{
			name: "the command is sensitive",
			val: eksProviderConfig(cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
				"api_version": cty.StringVal("client.authentication.k8s.io/v1beta1"),
				"command":     sensitive(cty.StringVal("aws")),
				"args":        full,
				"env":         env,
			})})),
		},
		{
			name: "one argument is sensitive",
			val: eksProviderConfig(cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
				"api_version": cty.StringVal("client.authentication.k8s.io/v1beta1"),
				"command":     cty.StringVal("aws"),
				"args": cty.ListVal([]cty.Value{
					cty.StringVal("eks"), sensitive(cty.StringVal("get-token")),
				}),
				"env": env,
			})})),
		},
		{
			name: "the whole argument list is sensitive",
			val: eksProviderConfig(cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
				"api_version": cty.StringVal("client.authentication.k8s.io/v1beta1"),
				"command":     cty.StringVal("aws"),
				"args":        sensitive(full),
				"env":         env,
			})})),
		},
		{
			name: "an environment value is sensitive",
			val: eksProviderConfig(cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
				"api_version": cty.StringVal("client.authentication.k8s.io/v1beta1"),
				"command":     cty.StringVal("aws"),
				"args":        full,
				"env":         cty.MapVal(map[string]cty.Value{"AWS_PROFILE": sensitive(cty.StringVal("ops"))}),
			})})),
		},
		{
			name: "the block names no command",
			val: eksProviderConfig(cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
				"api_version": cty.StringVal("client.authentication.k8s.io/v1beta1"),
				"command":     cty.NullVal(cty.String),
				"args":        full,
				"env":         env,
			})})),
		},
		{
			name: "the command is not known yet",
			val: eksProviderConfig(cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
				"api_version": cty.StringVal("client.authentication.k8s.io/v1beta1"),
				"command":     cty.UnknownVal(cty.String),
				"args":        full,
				"env":         env,
			})})),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := kubernetesSweepAttrs(tc.val, true).Exec; got != nil {
				t.Errorf("Exec = %+v, want nil: a marked or missing part of the block must drop the block", got)
			}
		})
	}
}

// A block whose optional parts are null is still a runnable plugin: the
// args and env arguments are both optional in the provider's schema.
func TestKubernetesSweepAttrsExecBlockWithoutArgs(t *testing.T) {
	val := eksProviderConfig(cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
		"api_version": cty.StringVal("client.authentication.k8s.io/v1beta1"),
		"command":     cty.StringVal("aws-iam-authenticator"),
		"args":        cty.NullVal(cty.List(cty.String)),
		"env":         cty.NullVal(cty.Map(cty.String)),
	})}))
	got := kubernetesSweepAttrs(val, true).Exec
	want := &kubesweep.ExecCredential{
		APIVersion: "client.authentication.k8s.io/v1beta1",
		Command:    "aws-iam-authenticator",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Exec = %+v, want %+v", got, want)
	}
}

// The provider's schema is read at run time, so the block can arrive as
// a tuple rather than a list, and with arguments this fork has never
// seen. Neither is a reason to drop a runnable plugin.
func TestKubernetesSweepAttrsExecBlockAsATuple(t *testing.T) {
	val := cty.ObjectVal(map[string]cty.Value{
		"host": cty.StringVal("https://h"),
		"exec": cty.TupleVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"api_version":       cty.StringVal("client.authentication.k8s.io/v1"),
			"command":           cty.StringVal("aws"),
			"args":              cty.TupleVal([]cty.Value{cty.StringVal("eks")}),
			"some_new_argument": cty.StringVal("ignored"),
		})}),
	})
	got := kubernetesSweepAttrs(val, true).Exec
	want := &kubesweep.ExecCredential{
		APIVersion: "client.authentication.k8s.io/v1",
		Command:    "aws",
		Args:       []string{"eks"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Exec = %+v, want %+v", got, want)
	}
}
