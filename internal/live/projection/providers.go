// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Providers is how [Build] reaches provider plugins.
//
// The instances it hands back must already be configured: [Build] calls
// ImportResourceState and ReadResource on them and nothing else, so a
// provider that has not had ConfigureProvider called will reject every
// request. Configuring a provider means evaluating its configuration
// block, which needs an evaluation context that this package deliberately
// does not have; the caller that owns the run (P1.4's live-plan
// command) owns that, along with the plugin processes' lifetimes. Build
// never closes a provider it was given.
//
// Keying on the full [addrs.AbsProviderConfig] rather than on
// [addrs.Provider] is what lets a configuration with provider aliases work
// later without changing this interface: two aliased configurations of the
// same provider are two different configured instances.
type Providers interface {
	ConfiguredProvider(ctx context.Context, addr addrs.AbsProviderConfig) (providers.Interface, error)
}

// ProviderFunc adapts a plain function to [Providers].
type ProviderFunc func(ctx context.Context, addr addrs.AbsProviderConfig) (providers.Interface, error)

// ConfiguredProvider implements [Providers].
func (f ProviderFunc) ConfiguredProvider(ctx context.Context, addr addrs.AbsProviderConfig) (providers.Interface, error) {
	return f(ctx, addr)
}

// SingleProvider is a [Providers] that answers every request with one
// already-configured provider instance. It is the shape both the unit
// tests (one mock) and the integration test (one AWS plugin process) need,
// and it is honest about its limit: a configuration that names a second
// provider gets an error rather than the wrong plugin.
func SingleProvider(addr addrs.AbsProviderConfig, p providers.Interface) Providers {
	return ProviderFunc(func(_ context.Context, want addrs.AbsProviderConfig) (providers.Interface, error) {
		if want.String() != addr.String() {
			return nil, fmt.Errorf("no provider configured for %s (this projection was given only %s)", want, addr)
		}
		return p, nil
	})
}

// providerCache resolves each provider configuration once per Build and
// remembers its schema, so that a run over twenty resource instances makes
// one GetProviderSchema call rather than twenty.
type providerCache struct {
	source Providers

	// signal is what the configuration under projection says about who
	// names each of its resources, or nil. It is half the input to the
	// identity check: the schemas are the other half, and neither settles
	// the Optional+Computed cohort alone. See schema_check.go.
	signal *identity.ConfigSignal

	entries map[string]*providerEntry
}

type providerEntry struct {
	provider providers.Interface
	schema   providers.GetProviderSchemaResponse
	err      error

	// verification is what the provider's own resource identity schemas
	// say about the identity package's hand-maintained type table, checked
	// once when the schemas arrive. See schema_check.go.
	verification identity.Verification
}

func newProviderCache(source Providers, signal *identity.ConfigSignal) *providerCache {
	return &providerCache{
		source:  source,
		signal:  signal,
		entries: make(map[string]*providerEntry),
	}
}

// get returns the configured provider for an address along with its
// schema. The error is sticky: a provider that failed to start is not
// retried once per resource instance.
func (c *providerCache) get(ctx context.Context, addr addrs.AbsProviderConfig) (*providerEntry, error) {
	key := addr.String()
	if e, ok := c.entries[key]; ok {
		return e, e.err
	}

	e := &providerEntry{}
	c.entries[key] = e

	p, err := c.source.ConfiguredProvider(ctx, addr)
	if err != nil {
		e.err = fmt.Errorf("cannot reach provider %s: %w", addr, err)
		return e, e.err
	}
	if p == nil {
		e.err = fmt.Errorf("no provider instance available for %s", addr)
		return e, e.err
	}
	e.provider = p

	schema := p.GetProviderSchema(ctx)
	if schema.Diagnostics.HasErrors() {
		e.err = fmt.Errorf("cannot read the schema of provider %s: %w", addr, schema.Diagnostics.Err())
		return e, e.err
	}
	e.schema = schema

	// The schemas are here, so the identity table's claims about these
	// types can be checked against the provider's own account of them.
	e.verification = verifySchemas(addr, schema.ResourceTypes, c.signal)

	return e, nil
}

// resourceSchema looks up one managed resource type's schema, returning a
// diagnostic rather than a nil block if the provider does not serve the
// type.
func (e *providerEntry) resourceSchema(addr addrs.AbsProviderConfig, typeName string) (providers.Schema, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	schema, ok := e.schema.ResourceTypes[typeName]
	if !ok || schema.Block == nil {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Unsupported resource type for the provider",
			fmt.Sprintf(
				"Provider %s has no schema for managed resource type %q, so a projection cannot be built for it. This is either a configuration error that validation should have caught or a provider bug.",
				addr, typeName,
			),
		))
		return providers.Schema{}, diags
	}
	// An entry in the identity table that builds this type's identity out
	// of arguments the type does not have can produce no identity at all,
	// and this is the point where that is known: the table is asserted
	// cloud-free, the schema arrives with a running provider. See
	// schema_check.go.
	if fatal := e.fatalDiags(typeName); fatal.HasErrors() {
		return providers.Schema{}, diags.Append(fatal)
	}
	return schema, diags
}
