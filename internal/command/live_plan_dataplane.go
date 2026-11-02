// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/staticeval"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/live/sweepconfig"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/pkg/azsweep"
)

// The command layer's half of the Key Vault data-plane sweep (GitHub issue
// #2205; the leg is discovery.DataPlaneSweep). Resource Graph does not index
// a vault's keys, secrets and certificates, so for each vault a pass's
// azurerm configuration declares, this leg lists the three collections at
// the vault's own URL and files the objects carrying the estate's marker.
//
// A vault's URL is "https://{name}." plus the Key Vault DNS suffix of the
// provider block's cloud (identity.CloudKeyVaultDNSSuffix, GitHub issue
// #2206), the name read statically from the azurerm_key_vault block or from
// the key_vault_id of a key, secret or certificate. A vault whose name does
// not evaluate statically cannot be listed and is one warning, never a
// silent skip. The client is built from the same provider block as the
// Resource Graph client, so it calls as the provider does.

var keyVaultObjectTypes = map[string]bool{
	"azurerm_key_vault_key":         true,
	"azurerm_key_vault_secret":      true,
	"azurerm_key_vault_certificate": true,
}

// keyVaultLister adapts the azsweep client to discovery.DataPlaneLister.
type keyVaultLister struct{ client *azsweep.KeyVaultClient }

func (l keyVaultLister) List(ctx context.Context, scope discovery.DataPlaneScope) ([]discovery.DataPlaneItem, error) {
	items, err := l.client.List(ctx, scope.BaseURL, string(scope.Kind))
	if err != nil {
		return nil, err
	}
	out := make([]discovery.DataPlaneItem, 0, len(items))
	for _, it := range items {
		out = append(out, discovery.DataPlaneItem{ID: it.ID, Name: it.Name, Tags: it.Tags})
	}
	return out, nil
}

// dataPlaneLeg builds the Key Vault leg for addr's pass, or nil when addr's
// configuration declares no vault object this pass owns.
func (p *projectionProviders) dataPlaneLeg(ctx context.Context, addr addrs.AbsProviderConfig) (discovery.Sweeper, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	if p.config == nil || addr.Provider.Type != "azurerm" {
		return nil, nil
	}
	vaults, problems := keyVaultNamesFor(ctx, p.config, addr)
	for _, msg := range problems {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, azsweep.SummarySweepUnavailable, msg))
	}
	if len(vaults) == 0 {
		return nil, diags
	}
	suffix, ok := identity.ProviderCloudValue(p.config, addr, identity.CloudKeyVaultDNSSuffix, identity.CloudContext{Environment: azsweep.EnvEnvironment(os.Getenv)})
	if !ok {
		return nil, diags.Append(tfdiags.Sourceless(tfdiags.Warning, azsweep.SummarySweepUnavailable,
			fmt.Sprintf("Provider configuration %s sets an Azure environment this run cannot read statically (or a metadata_host), so the Key Vault URL of %s is unknown and the keys, secrets and certificates in %s are not listed this run.",
				addr, strings.Join(vaults, ", "), plural(len(vaults), "that vault", "those vaults"))))
	}
	val, have := p.familyProviderValue(ctx, addr)
	client, err := sweepconfig.AzureKeyVault(ctx, substrate.AzureSweepAttrs(val, have))
	if err != nil {
		return nil, diags.Append(tfdiags.Sourceless(tfdiags.Warning, azsweep.SummarySweepUnavailable,
			fmt.Sprintf("No Key Vault client could be built from provider configuration %s, so the keys, secrets and certificates in %s are not listed this run: %s.", addr, strings.Join(vaults, ", "), err)))
	}
	leg := discovery.DataPlaneSweep{Lister: keyVaultLister{client: client}}
	for _, v := range vaults {
		base := "https://" + v + "." + suffix
		for _, k := range []discovery.DataPlaneKind{discovery.DataPlaneKeys, discovery.DataPlaneSecrets, discovery.DataPlaneCertificates} {
			leg.Scopes = append(leg.Scopes, discovery.DataPlaneScope{BaseURL: base, Kind: k})
		}
	}
	return leg, diags
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// keyVaultNamesFor is the sorted, deduplicated names of the vaults the
// azurerm resources under addr declare or point at, and one message per
// resource whose vault name could not be read statically.
func keyVaultNamesFor(ctx context.Context, cfg *configs.Config, addr addrs.AbsProviderConfig) (names, problems []string) {
	seen := map[string]bool{}
	add := func(n string) {
		if n != "" && !seen[strings.ToLower(n)] {
			seen[strings.ToLower(n)] = true
			names = append(names, n)
		}
	}
	walkManagedResources(cfg, func(rc *configs.Resource, modCfg *configs.Config) {
		if rc.Type != "azurerm_key_vault" && !keyVaultObjectTypes[rc.Type] {
			return
		}
		if providerConfigAddr(modCfg, rc).String() != addr.String() {
			return
		}
		if rc.Type == "azurerm_key_vault" {
			if n, why := safeStaticArgument(ctx, modCfg.Module, rc, "name"); why == "" {
				add(n)
			} else {
				problems = append(problems, fmt.Sprintf("The name of %s.%s could not be read statically (%s), so the keys, secrets and certificates in that vault are not listed this run.", rc.Type, rc.Name, why))
			}
			return
		}
		if n, ok := vaultNameOfObject(ctx, modCfg, rc); ok {
			add(n)
		} else {
			problems = append(problems, fmt.Sprintf("The vault of %s.%s could not be read statically from its key_vault_id, so the vault is not listed this run unless another resource names it.", rc.Type, rc.Name))
		}
	})
	sort.Strings(names)
	return names, problems
}

// vaultNameOfObject reads the vault name off a key, secret or certificate
// block's key_vault_id: a static ARM id naming .../vaults/{name}, or a
// reference to azurerm_key_vault.X's id whose name is static.
func vaultNameOfObject(ctx context.Context, modCfg *configs.Config, rc *configs.Resource) (string, bool) {
	content, _, diags := rc.Config.PartialContent(&hcl.BodySchema{Attributes: []hcl.AttributeSchema{{Name: "key_vault_id"}}})
	if diags.HasErrors() || content.Attributes["key_vault_id"] == nil {
		return "", false
	}
	// A reference to a resource is read off the traversal, never evaluated:
	// the static evaluator has no value for a resource attribute.
	if trav, tdiags := hcl.AbsTraversalForExpr(content.Attributes["key_vault_id"].Expr); !tdiags.HasErrors() {
		if len(trav) != 3 || trav.RootName() != "azurerm_key_vault" {
			return "", false
		}
		name, ok := trav[1].(hcl.TraverseAttr)
		attr, ok2 := trav[2].(hcl.TraverseAttr)
		if !ok || !ok2 || attr.Name != "id" {
			return "", false
		}
		vault, found := modCfg.Module.ManagedResources["azurerm_key_vault."+name.Name]
		if !found {
			return "", false
		}
		n, why := safeStaticArgument(ctx, modCfg.Module, vault, "name")
		return n, why == "" && n != ""
	}
	id, why := safeStaticArgument(ctx, modCfg.Module, rc, "key_vault_id")
	if why != "" {
		return "", false
	}
	return vaultNameFromARMID(id)
}

// vaultNameFromARMID is the vault name in
// /subscriptions/{s}/resourceGroups/{g}/providers/Microsoft.KeyVault/vaults/{name}.
func vaultNameFromARMID(id string) (string, bool) {
	parts := strings.Split(strings.Trim(id, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		if strings.EqualFold(parts[i], "vaults") && parts[i+1] != "" && !strings.ContainsAny(parts[i+1], ":?#") {
			if _, err := url.Parse("https://" + parts[i+1]); err == nil {
				return parts[i+1], true
			}
		}
	}
	return "", false
}

// safeStaticArgument is staticeval.Argument with the static evaluator's
// panic class (a variable no value reached) turned into a reason, because a
// vault this leg cannot name is one warning, not a failed plan.
func safeStaticArgument(ctx context.Context, mod *configs.Module, rc *configs.Resource, name string) (val, why string) {
	defer func() {
		if r := recover(); r != nil {
			val, why = "", fmt.Sprintf("its %s argument could not be evaluated", name)
		}
	}()
	return staticeval.Argument(ctx, mod, rc, name)
}
