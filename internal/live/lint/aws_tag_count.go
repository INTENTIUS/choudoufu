// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/providerscope"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/providers"
)

// awsTagMax is the most user tags AWS lets one resource carry.
const awsTagMax = 50

// awsTagsArgument is the aws provider's tags argument, on a resource and
// inside a provider's default_tags block.
const awsTagsArgument = "tags"

// checkAWSTagCount refuses an aws resource whose tags, once this run adds
// its markers, come to more than AWS's 50 (GitHub issue #2131). It is
// gcp-label-count's AWS twin and takes the same shape:
//
//   - The count is the union of the keys the block's own tags argument
//     states and the keys its provider block's default_tags { tags } states.
//     A resource key overrides a default of the same name, so neither is
//     counted twice.
//   - The markers are tofu-estate, tofu-address, one continuation tag for
//     each further 256 characters of the longest address this block can
//     stamp, and tofu-slot on a count block. terraform-aws-modules write
//     count = var.create ? 1 : 0 on every resource, so three markers is
//     their ordinary case.
//   - A key whose value is unknown is counted as present. Only a map whose
//     keys cannot be read from configuration alone is skipped, silently.
//
// With provider schemas, only a type whose tags map carries the marker
// ([substrate.SurfaceOf]) is counted. Without them, an aws block that sets
// tags is counted.
//
// An error: a configuration stock creates with 48 to 50 tags would fail its
// create only because of the markers, and the stock-compatibility rule says
// that is refused by name before the plan, never left to the API.
func checkAWSTagCount(ctx context.Context, cfg *configs.Config, modInst addrs.ModuleInstance, schemas map[string]providers.Schema, scope identity.Scope, issues *[]Issue) {
	if cfg == nil || cfg.Module == nil {
		return
	}
	root := cfg.Root
	if root == nil {
		root = cfg
	}
	mod := cfg.Module
	names := make([]string, 0, len(mod.ManagedResources))
	for name := range mod.ManagedResources {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		rc := mod.ManagedResources[name]
		if !isAWSProvider(rc.Provider) || rc.Config == nil {
			continue
		}
		if scopeExcludes(scope, cfg.Path, rc.Addr()) {
			continue
		}
		if issue, ok := awsTagCountIssue(ctx, root, cfg, modInst, rc, schemas); ok {
			*issues = append(*issues, issue)
		}
	}
}

// isAWSProvider is true for hashicorp/aws and any other source whose type
// is "aws". awscc writes tags as a list of key/value objects and is not
// counted here.
func isAWSProvider(fqn addrs.Provider) bool {
	sub, ok := substrate.ForProvider(fqn.Type)
	return ok && sub == substrate.AWS && fqn.Type == "aws"
}

// awsTagCountIssue is [checkAWSTagCount]'s verdict on one block.
func awsTagCountIssue(ctx context.Context, root, c *configs.Config, modInst addrs.ModuleInstance, rc *configs.Resource, schemas map[string]providers.Schema) (Issue, bool) {
	tagsExpr, hasTags := bodyAttrExpr(rc.Config, awsTagsArgument)
	if schema, ok := schemas[rc.Type]; ok && schema.Block != nil {
		if _, carries := substrate.SurfaceOf(rc.Provider.Type, schema.Block); !carries {
			return Issue{}, false
		}
	} else if !hasTags {
		return Issue{}, false
	}

	keys := map[string]bool{}
	unknownValues := false
	if hasTags {
		own, unknown, ok := staticLabelKeys(ctx, c.Module, tagsExpr)
		if !ok {
			return Issue{}, false
		}
		unknownValues = unknownValues || unknown
		for k := range own {
			keys[k] = true
		}
	}

	defaults, ok := awsProviderDefaultTags(ctx, root, c, rc)
	if !ok {
		return Issue{}, false
	}
	for k := range defaults {
		keys[k] = true
	}
	stock := len(keys)

	added := []string{markers.TagEstate}
	chunks := markers.SplitAddress(markers.EscapeAddress(armWorstInstance(ctx, c.Module, modInst, rc)))
	for i := range chunks {
		added = append(added, markers.AddressTagKey(i))
	}
	if rc.Count != nil {
		added = append(added, markers.TagSlot)
	}
	newKeys := 0
	for _, k := range added {
		if !keys[k] {
			newKeys++
		}
		keys[k] = true
	}
	if len(keys) <= awsTagMax {
		return Issue{}, false
	}

	addr := addrs.ConfigResource{Module: c.Path, Resource: rc.Addr()}.String()
	detail := fmt.Sprintf(
		"%s would carry %d tags once this run adds its ownership markers (%d from its tags and its provider's default_tags, which stock would send, plus %s), and AWS refuses more than %d on one resource. "+
			"The create would fail at the API because of the markers alone if stock would send %d or fewer. Remove tags until the block and default_tags carry at most %d together.",
		addr, len(keys), stock, strings.Join(added, ", "), awsTagMax, awsTagMax, awsTagMax-newKeys)
	if unknownValues {
		detail += " Some tags' values are not known until apply; each of those keys is counted, since the key takes a slot whatever its value."
	}
	subject := rc.DeclRange
	if hasTags {
		subject = tagsExpr.Range()
	}
	return Issue{
		Rule:      RuleAWSTagCount,
		Construct: addr,
		Module:    c.Path,
		Detail:    detail,
		Subject:   subject,
	}, true
}

// awsProviderDefaultTags is the key set of the default_tags block of the
// provider block serving rc. No block, or a block with no default_tags, is
// no defaults. ok is false when the provider block has its own for_each or
// its default_tags keys cannot be read.
func awsProviderDefaultTags(ctx context.Context, root, c *configs.Config, rc *configs.Resource) (map[string]bool, bool) {
	pc, mod, ok := providerBlockFor(root, c, rc)
	if !ok {
		return nil, false
	}
	if pc == nil || pc.Config == nil {
		return nil, true
	}
	content, _, diags := pc.Config.PartialContent(&hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{{Type: "default_tags"}},
	})
	if diags.HasErrors() {
		return nil, true
	}
	out := map[string]bool{}
	for _, block := range content.Blocks {
		expr, ok := bodyAttrExpr(block.Body, awsTagsArgument)
		if !ok {
			continue
		}
		got, _, ok := staticLabelKeys(ctx, mod, expr)
		if !ok {
			return nil, false
		}
		for k := range got {
			out[k] = true
		}
	}
	return out, true
}

// providerBlockFor is the provider block serving rc and the module it is
// declared in. A nil block with ok true is "no block": the provider is
// configured from the environment alone. ok is false when the block has its
// own for_each, which configures several instances this rule cannot tell
// apart.
func providerBlockFor(root, c *configs.Config, rc *configs.Resource) (*configs.Provider, *configs.Module, bool) {
	abs := providerscope.ResolveResource(c, rc)
	owner := root.Descendent(abs.Module)
	if owner == nil || owner.Module == nil {
		return nil, nil, true
	}
	mod := owner.Module
	keys := make([]string, 0, len(mod.ProviderConfigs))
	for k := range mod.ProviderConfigs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		cand := mod.ProviderConfigs[k]
		if cand.Alias == abs.Alias && mod.ProviderForLocalConfig(addrs.LocalProviderConfig{LocalName: cand.Name}) == abs.Provider {
			if cand.ForEach != nil {
				return nil, mod, false
			}
			return cand, mod, true
		}
	}
	return nil, mod, true
}
