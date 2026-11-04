// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package override

import (
	"context"
	"fmt"
	"regexp"
	"sort"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/providers"
)

// Approach is #2309's letter for what an override does to a call.
type Approach string

const (
	// RewriteConfig rewrites config or planned values going into a call.
	RewriteConfig Approach = "A"
	// RewriteDiff makes an equivalent value plan as no change.
	RewriteDiff Approach = "B"
	// AroundCall adds a step around a call, or retries a call on a known error.
	AroundCall Approach = "C"
	// Refuse refuses a call.
	Refuse Approach = "D"
	// HandleItself handles one operation of one type in place of the provider.
	HandleItself Approach = "E"
)

// SDK is the framework a provider builds a resource type on. It decides
// whether a planned value may differ from configuration: OpenTofu tolerates
// that from SDKv2 resources and refuses it from Plugin Framework ones.
type SDK string

const (
	SDKv2     SDK = "SDKv2"
	Framework SDK = "Framework"
)

// PlanFunc is an override's hook on PlanResourceChange. It receives the
// request and the wrapped provider, and returns the response OpenTofu should
// see and whether the override changed anything. An override that finds
// nothing to do returns the inner provider's own response and false.
type PlanFunc func(ctx context.Context, inner providers.Interface, req providers.PlanResourceChangeRequest) (providers.PlanResourceChangeResponse, bool)

// ApplyFunc is an override's hook on ApplyResourceChange, the shape C and E
// overrides need: a step before or after the call, a bounded retry, or the
// operation handled in place of the provider. It returns the response
// OpenTofu should see and whether the override changed anything.
type ApplyFunc func(ctx context.Context, inner providers.Interface, req providers.ApplyResourceChangeRequest) (providers.ApplyResourceChangeResponse, bool)

// Override is one registered provider behaviour override.
type Override struct {
	// Name identifies the override in plan warnings and in
	// CHOUDOUFU_PROVIDER_OVERRIDES. Lower-case words joined by hyphens.
	Name string

	// Provider is the provider's namespace and type, for example
	// "hashicorp/aws". The registry host is not part of it, so a source
	// written as registry.terraform.io/hashicorp/aws gets the same overrides.
	Provider string

	// Type is the resource type the override acts on.
	Type string

	// Approach is #2309's letter for what it does.
	Approach Approach

	// SDK is what the resource type is built on, at ProvenOn.
	SDK SDK

	// Upstream is the issue or pull request the override stands in for, as
	// "owner/repo#number". It is retired when that closes as fixed.
	Upstream string

	// Summary is one sentence: what users see without the override.
	Summary string

	// ProvenOn is the provider version the override's proof was run
	// against. It is a record, not a gate: [Requires] is the gate.
	ProvenOn string

	// Requires maps each attribute the override reads, as a dotted path
	// through nested blocks ("authentication_mode.type"), to the type it
	// must have in the loaded provider's schema.
	Requires map[string]cty.Type

	// Plan is the override's PlanResourceChange hook, if it has one.
	Plan PlanFunc

	// Apply is the override's ApplyResourceChange hook, if it has one. An
	// override has at least one of Plan and Apply.
	Apply ApplyFunc
}

var registry = map[string]Override{}

var nameRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// register adds an override. Each override's file calls it from a package
// variable initialiser, so parallel units never edit a shared list.
func register(o Override) Override {
	if _, dup := registry[o.Name]; dup {
		panic(fmt.Sprintf("override %q registered twice", o.Name))
	}
	registry[o.Name] = o
	return o
}

// All returns every registered override, sorted by name.
func All() []Override {
	out := make([]Override, 0, len(registry))
	for _, o := range registry {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Lookup returns the registered override with this name.
func Lookup(name string) (Override, bool) {
	o, ok := registry[name]
	return o, ok
}

// ForProvider returns the overrides in set that act on this provider, keyed
// by resource type.
func ForProvider(provider addrs.Provider, set []Override) map[string][]Override {
	key := provider.Namespace + "/" + provider.Type
	out := map[string][]Override{}
	for _, o := range set {
		if o.Provider == key {
			out[o.Type] = append(out[o.Type], o)
		}
	}
	return out
}
