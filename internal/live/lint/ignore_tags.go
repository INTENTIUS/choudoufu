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
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/staticeval"
)

// checkIgnoreTags refuses an aws provider block whose ignore_tags names an
// ownership marker (GitHub issue #2290): a key in keys equal to a marker
// key, or a prefix in key_prefixes that a marker key starts with.
// key_prefixes = ["tofu-"] covers all of them; "tofu-x" covers none.
//
// It is the provider-level twin of ignore-changes. ignore_changes = [tags]
// drops the markers from the writes; ignore_tags drops them from the reads.
// From the provider's source (hashicorp/aws v6.70.0, the transparent tagging
// interceptors in internal/provider/sdkv2/tags_interceptor.go and
// internal/provider/framework/tags_interceptor.go): on create, the tags sent
// are default_tags merged with the resource's tags with only system tags
// removed, so an ignored marker key IS sent and the object is created
// marked. Every read then strips ignored keys from tags and tags_all, and
// the planned tags_all is computed with them stripped, so an update never
// sends them again. The live object carries the markers, the provider never
// shows them, and a marker repair through the provider cannot reach them.
//
// An error, one per provider block. A keys or key_prefixes value that cannot
// be read from configuration alone is skipped, silently, the default every
// lint rule gives an unknown.
func checkIgnoreTags(ctx context.Context, mod *configs.Module, path addrs.Module, issues *[]Issue) {
	if mod == nil {
		return
	}
	names := make([]string, 0, len(mod.ProviderConfigs))
	for k := range mod.ProviderConfigs {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		pc := mod.ProviderConfigs[k]
		if pc.Config == nil || !isAWSProvider(mod.ProviderForLocalConfig(addrs.LocalProviderConfig{LocalName: pc.Name})) {
			continue
		}
		content, _, diags := pc.Config.PartialContent(&hcl.BodySchema{
			Blocks: []hcl.BlockHeaderSchema{{Type: "ignore_tags"}},
		})
		if diags.HasErrors() {
			continue
		}
		var covered []string
		var subject *hcl.Range
		for _, block := range content.Blocks {
			for _, arg := range []string{"keys", "key_prefixes"} {
				vals, rng, ok := ignoreTagsStrings(ctx, mod, block.Body, arg, "provider."+pc.Name+".ignore_tags."+arg)
				if !ok {
					continue
				}
				for _, v := range vals {
					for _, marker := range ignoreTagsMarkerKeys() {
						hit := v == marker
						if arg == "key_prefixes" {
							hit = v != "" && strings.HasPrefix(marker, v)
						}
						if hit {
							covered = append(covered, fmt.Sprintf("%s %q covers %s", arg, v, marker))
							if subject == nil {
								subject = rng.Ptr()
							}
						}
					}
				}
			}
		}
		if len(covered) == 0 {
			continue
		}
		block := fmt.Sprintf("provider %q", pc.Name)
		if pc.Alias != "" {
			block = fmt.Sprintf("provider %q (alias %q)", pc.Name, pc.Alias)
		}
		if !path.IsRoot() {
			block = path.String() + " " + block
		}
		if subject == nil {
			subject = pc.DeclRange.Ptr()
		}
		*issues = append(*issues, Issue{
			Rule:      RuleIgnoreTags,
			Construct: block,
			Module:    path,
			Detail: fmt.Sprintf(
				"%s's ignore_tags hides the ownership markers from every read this provider makes: %s. "+
					"The provider still sends them on create, so the object is created marked, but each read strips them from tags and tags_all and an update never sends them again, so a live run cannot read who owns the object through this provider and cannot repair a marker on it. "+
					"Config tags keeps the markers while the refreshed state does not, so every plan shows a tags diff and the estate never plans clean. "+
					"Remove the marker keys from keys, or narrow key_prefixes so no prefix is the start of %s.",
				block, strings.Join(covered, "; "), strings.Join(quoteEach(ignoreTagsMarkerKeys()), ", ")),
			Subject: *subject,
		})
	}
}

// ignoreTagsMarkerKeys is every tag key the stamp can write on an aws
// resource: tofu-estate, tofu-address and its continuations, and tofu-slot.
func ignoreTagsMarkerKeys() []string {
	out := []string{markers.TagEstate, markers.TagAddress}
	for n := 2; n <= markers.MaxContinuations; n++ {
		out = append(out, markers.ContinuationTag(n))
	}
	return append(out, markers.TagSlot)
}

func quoteEach(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}

// ignoreTagsStrings reads a list-of-strings argument of an ignore_tags block
// through the module's pure static evaluator. ok is false for an absent
// argument and for anything it cannot read as known strings.
func ignoreTagsStrings(ctx context.Context, mod *configs.Module, body hcl.Body, name, subject string) ([]string, hcl.Range, bool) {
	content, _, diags := body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{{Name: name}},
	})
	if diags.HasErrors() {
		return nil, hcl.Range{}, false
	}
	attr, ok := content.Attributes[name]
	if !ok || mod.StaticEvaluator == nil {
		return nil, hcl.Range{}, false
	}
	if _, bad := staticeval.FirstDisallowed(attr.Expr); bad {
		return nil, hcl.Range{}, false
	}
	val, ok := staticeval.EvaluateOK(ctx, mod.StaticEvaluator.Pure(), attr.Expr, subject)
	if !ok || val.IsMarked() || val.IsNull() || !val.IsWhollyKnown() || !val.CanIterateElements() {
		return nil, hcl.Range{}, false
	}
	var out []string
	for it := val.ElementIterator(); it.Next(); {
		_, v := it.Element()
		s, err := convert.Convert(v, cty.String)
		// val.IsMarked covers the collection, not each element: a list
		// holding one sensitive string is unmarked at the top. A marked
		// element is a value this rule cannot read, so it says nothing.
		if err != nil || s.IsNull() || s.IsMarked() || !s.IsKnown() {
			return nil, hcl.Range{}, false
		}
		out = append(out, s.AsString())
	}
	return out, attr.Expr.Range(), true
}
