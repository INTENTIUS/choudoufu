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
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/armtypes"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/staticeval"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/providers"
)

// The arm-tag-limit rule (GitHub issue #1120, unit 12).
//
// An Azure resource takes a bounded number of tags, and some take fewer than
// ARM's 50: azurerm validates its own maximum per type (key vault secrets
// take 15), and Azure's own type data documents fewer for others (Cosmos DB
// accounts and storage accounts 15, API Management 10). The stamp adds two
// marker tags, tofu-estate and tofu-address, one more for each continuation
// an address longer than 256 characters needs, and tofu-slot on a count
// block. A configuration stock would create with 49 tags on a resource
// group, or 14 on a Cosmos DB account, therefore fails its create only
// because of the markers. The rule says so at lint, naming the limit and
// where it comes from:
//
//   - azurerm's own validation of the type's tags argument
//     ([armtypes.AzurermTagRules], read from azurerm's schema at the pin);
//   - the ARM type's documented maximum ([armtypes.TagLimit], from the
//     tags description in azapi's type data), with the ARM type of an
//     azurerm type read off its id template ([armtypes.AzurermARMType]) and
//     an azapi resource's from its type argument. Seven types document a
//     count at some API versions only, so an azapi block is measured at the
//     version its type argument names (GitHub issue #2133, finding 8). An
//     azurerm block is measured at the version azurerm sends, which its
//     configuration does not name but the provider fixes per type: the
//     go-azure-sdk package of the type's id parser
//     ([armtypes.AzurermAPIVersion], GitHub issue #2147). A type whose id
//     parser names no version (a commonids type, such as
//     azurerm_storage_account's) is measured at the lowest count any
//     version documents, the figure no version of the type contradicts;
//   - ARM's default of 50 otherwise.
//
// It also checks every marker tag the stamp would write against
// [armtypes.CheckTag]: a key spelled as the configuration spells it
// (the stamp keeps a configuration's Tofu-Estate rather than add a second
// key, which azurerm_data_share_account's lowercase-only validation then
// refuses), and every address chunk's length in bytes, which is what
// azurerm and ARM measure while the continuation split counts characters.
//
// A warning, not a refusal: the create fails at Azure with Azure's own
// error, and nothing is created wrongly. Its job is to name the cause
// before an apply, where Azure's error names only the count.
//
// It reads what configuration alone states, the same can-see boundary as
// the overlong-address rule: a tags argument whose keys do not evaluate is
// skipped; a key whose value is unknown is counted, since the key is what
// takes a slot. An azapi body that is not an object constructor (body =
// local.b) is evaluated whole for its tags, and skipped when it does not
// evaluate (GitHub issue #2147).
func checkARMTagLimit(ctx context.Context, cfg *configs.Config, modInst addrs.ModuleInstance, schemas map[string]providers.Schema, scope identity.Scope, issues *[]Issue) {
	mod := cfg.Module
	names := make([]string, 0, len(mod.ManagedResources))
	for name := range mod.ManagedResources {
		names = append(names, name)
	}
	sort.Strings(names)
	estate := ""
	if root := cfg.Root; root != nil && root.Module != nil && root.Module.Live != nil {
		estate = root.Module.Live.Estate
	}
	for _, name := range names {
		rc := mod.ManagedResources[name]
		if sub, ok := substrate.ForProvider(rc.Provider.Type); !ok || sub != substrate.Azure || rc.Config == nil {
			continue
		}
		if scopeExcludes(scope, cfg.Path, rc.Addr()) {
			continue
		}
		if issue, ok := armTagLimitIssue(ctx, mod, modInst, cfg.Path, rc, schemas, estate); ok {
			*issues = append(*issues, issue)
		}
	}
}

// armTagBudget is what one resource's tags are measured against: the
// lowest of the limits that apply, and every limit's source.
type armTagBudget struct {
	max     int
	sources []string
	// rulesType is the Terraform type azurerm's own tag validation is
	// keyed by, for [armtypes.CheckTag]; azapi has none.
	rulesType string
	armType   string
	// apiVersion is the azapi block's own, from its type argument, or the
	// one azurerm sends for the type; empty when neither is known.
	apiVersion string
}

func (b *armTagBudget) apply(max int, source string) {
	if max <= 0 {
		return
	}
	b.sources = append(b.sources, fmt.Sprintf("%d (%s)", max, source))
	if b.max == 0 || max < b.max {
		b.max = max
	}
}

// armTagLimitIssue is [checkARMTagLimit]'s verdict on one block.
func armTagLimitIssue(ctx context.Context, mod *configs.Module, modInst addrs.ModuleInstance, path addrs.Module, rc *configs.Resource, schemas map[string]providers.Schema, estate string) (Issue, bool) {
	if schema, ok := schemas[rc.Type]; ok && schema.Block != nil {
		if _, carries := substrate.SurfaceOf(rc.Provider.Type, schema.Block); !carries {
			return Issue{}, false
		}
	}

	var budget armTagBudget
	tagsExpr, hasTags := armBodyAttr(rc.Config, markers.ARMTagsArgument)
	if rules, ok := armtypes.AzurermTagRules(rc.Type); ok {
		budget.rulesType = rc.Type
		budget.apply(rules.MaxTags, fmt.Sprintf("azurerm %s's own validation of tags, %s", armtypes.DefaultRMTags().Version, rules.Validator))
		budget.armType, _ = armtypes.AzurermARMType(rc.Type)
		budget.apiVersion, _ = armtypes.AzurermAPIVersion(rc.Type)
	} else {
		// azapi_resource: the ARM type and API version are its type
		// argument, and the stamp writes only where azapi's type data says
		// that version takes tags (internal/live/projection's armTagTarget).
		typ, reason := staticeval.Argument(ctx, mod, rc, "type")
		if reason != "" {
			return Issue{}, false
		}
		armType, apiVersion, ok := armtypes.ParseAzapiType(typ)
		if !ok {
			return Issue{}, false
		}
		if taggable, known := armtypes.Taggable(armType, apiVersion); !taggable || !known {
			return Issue{}, false
		}
		budget.armType, budget.apiVersion = armType, apiVersion
		if !hasTags {
			// azapi sends body.tags when the body carries them, and the
			// stamp writes there; the top-level argument is the other path.
			tagsExpr, hasTags = armBodyTagsExpr(rc.Config)
			if !hasTags {
				var readable bool
				if tagsExpr, hasTags, readable = armEvaluatedBodyTags(ctx, mod, rc.Config); !readable {
					return Issue{}, false
				}
			}
		}
	}
	if budget.armType != "" {
		max, source := armtypes.TagLimit(budget.armType, budget.apiVersion)
		budget.apply(max, budget.armType+": "+source)
	} else {
		budget.apply(armtypes.DefaultTagLimit, "ARM default (go-azure-helpers tags.Validate; Azure's documented limit)")
	}

	keys := map[string]bool{}
	unknownValues := false
	if hasTags {
		got, unknown, ok := armStaticTagKeys(ctx, mod, tagsExpr)
		if !ok {
			return Issue{}, false
		}
		keys, unknownValues = got, unknown
	}

	// The configuration's own spelling of each marker key, which the stamp
	// keeps rather than add a lowercase twin (markers.SetARMMarkerTag).
	var own []string
	spelling := map[string]string{}
	for k := range keys {
		if canon, marker := markers.CanonicalMarkerKey(k); marker {
			if prev, seen := spelling[canon]; !seen || k < prev {
				spelling[canon] = k
			}
			continue
		}
		own = append(own, k)
	}
	spelled := func(canon string) string {
		if s, ok := spelling[canon]; ok {
			return s
		}
		return canon
	}

	// The longest address this block can stamp: the most continuation
	// tags, and the chunks most likely to overrun a byte limit.
	addr := armWorstInstance(ctx, mod, modInst, rc)
	chunks := markers.SplitAddress(markers.EscapeAddress(addr))
	written := map[string]string{spelled(markers.TagEstate): estate}
	var writtenOrder []string
	writtenOrder = append(writtenOrder, spelled(markers.TagEstate))
	for i, chunk := range chunks {
		k := spelled(markers.AddressTagKey(i))
		written[k] = chunk
		writtenOrder = append(writtenOrder, k)
	}
	if rc.Count != nil {
		k := spelled(markers.TagSlot)
		written[k] = ""
		writtenOrder = append(writtenOrder, k)
	}

	total := len(own) + len(writtenOrder)
	var problems []string
	if total > budget.max {
		problems = append(problems, fmt.Sprintf(
			"it would carry %d tags once this run adds its ownership markers (%d from its own tags, plus %s), and the most it takes is %d. The limits that apply: %s",
			total, len(own), strings.Join(writtenOrder, ", "), budget.max, strings.Join(budget.sources, "; ")))
	}
	var lim armtypes.TypeTagLimit
	if budget.armType != "" {
		lim, _ = armtypes.DefaultTagLimits().Limit(budget.armType)
	}
	rulesType := budget.rulesType
	if rulesType == "" {
		rulesType = rc.Type
	}
	for _, k := range writtenOrder {
		v := written[k]
		if k == spelled(markers.TagEstate) && v == "" {
			// No live block in reach to name the estate: its grammar caps
			// it at 128 characters, inside every limit, so only the key is
			// checked.
			v = "x"
		}
		for _, err := range armtypes.CheckTag(rulesType, k, v) {
			problems = append(problems, fmt.Sprintf("the marker tag %s: %s", k, err))
		}
		if lim.MaxKeyLen > 0 && len(k) > lim.MaxKeyLen {
			problems = append(problems, fmt.Sprintf("the marker tag key %q is %d characters, and %s's documented limit is %d (azapi %s type data)", k, len(k), budget.armType, lim.MaxKeyLen, armtypes.DefaultTagLimits().AzapiVersion))
		}
		if lim.MaxValueLen > 0 && len(v) > lim.MaxValueLen {
			problems = append(problems, fmt.Sprintf("the value of marker tag %q is %d bytes, and %s's documented limit is %d (azapi %s type data)", k, len(v), budget.armType, lim.MaxValueLen, armtypes.DefaultTagLimits().AzapiVersion))
		}
	}
	if len(problems) == 0 {
		return Issue{}, false
	}

	construct := addrs.ConfigResource{Module: path, Resource: rc.Addr()}.String()
	detail := strings.Join(problems, ". ") + ". Azure refuses the write that carries the markers: the create or update fails, " +
		"or, for a type azurerm tags after its create, the object is created and left unmarked. Free room in the block's own tags, or keep its identity in the record store instead of a marker (strict { markers \"record\" }; live/LIMITATIONS.md, \"strict-markers\")."
	if unknownValues {
		detail += " Some tags' values are not known until apply; each of those keys is counted, since the key takes a slot whatever its value."
	}
	subject := rc.DeclRange
	if hasTags {
		subject = tagsExpr.Range()
	}
	return Issue{
		Rule:      RuleARMTagLimit,
		Construct: construct,
		Module:    path,
		Detail:    detail,
		Subject:   subject,
	}, true
}

// armWorstInstance is the block's instance whose escaped address is the
// longest this rule can see, as [checkOverlongAddresses] measures it: each
// static for_each key, the highest static count index, or the unkeyed
// instance.
func armWorstInstance(ctx context.Context, mod *configs.Module, modInst addrs.ModuleInstance, rc *configs.Resource) string {
	worst := rc.Addr().Instance(addrs.NoKey).Absolute(modInst).String()
	longer := func(s string) {
		if len(markers.EscapeAddress(s)) > len(markers.EscapeAddress(worst)) {
			worst = s
		}
	}
	switch {
	case rc.ForEach != nil:
		if keys, ok := staticeval.ForEachKeys(ctx, mod, rc.ForEach); ok {
			for _, k := range keys {
				longer(rc.Addr().Instance(addrs.StringKey(k)).Absolute(modInst).String())
			}
		}
	case rc.Count != nil:
		if n, ok := staticeval.Count(ctx, mod, rc.Count); ok && n > 0 {
			longer(rc.Addr().Instance(addrs.IntKey(n - 1)).Absolute(modInst).String())
		}
	}
	return worst
}

// armBodyAttr is body's argument name, when it sets one.
func armBodyAttr(body hcl.Body, name string) (hcl.Expression, bool) {
	content, _, diags := body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{{Name: name}},
	})
	if diags.HasErrors() {
		return nil, false
	}
	attr, ok := content.Attributes[name]
	if !ok {
		return nil, false
	}
	return attr.Expr, true
}

// armBodyTagsExpr is the tags item of an azapi body written as an object
// constructor, the one shape whose tags this rule can read without
// evaluating the whole body.
func armBodyTagsExpr(body hcl.Body) (hcl.Expression, bool) {
	expr, ok := armBodyAttr(body, "body")
	if !ok {
		return nil, false
	}
	obj, ok := expr.(*hclsyntax.ObjectConsExpr)
	if !ok {
		return nil, false
	}
	for _, item := range obj.Items {
		if hcl.ExprAsKeyword(item.KeyExpr) == markers.ARMTagsArgument {
			return item.ValueExpr, true
		}
		if key, ok := item.KeyExpr.(*hclsyntax.ObjectConsKeyExpr); ok {
			if v, diags := key.Value(nil); !diags.HasErrors() && v.Type() == cty.String && v.AsString() == markers.ARMTagsArgument {
				return item.ValueExpr, true
			}
		}
	}
	return nil, false
}

// armEvaluatedBodyTags is the tags of an azapi body that is not an object
// constructor (body = local.b, or a merge of objects), evaluated whole, as a
// literal expression [armStaticTagKeys] reads. readable is false when the
// body does not evaluate to a known object, so the rule cannot say how many
// tags it carries; hasTags is false when the body carries none, or there is
// no body.
func armEvaluatedBodyTags(ctx context.Context, mod *configs.Module, body hcl.Body) (tags hcl.Expression, hasTags bool, readable bool) {
	expr, ok := armBodyAttr(body, "body")
	if !ok {
		return nil, false, true
	}
	if _, cons := expr.(*hclsyntax.ObjectConsExpr); cons {
		// An object constructor with no tags item carries none.
		return nil, false, true
	}
	if mod == nil || mod.StaticEvaluator == nil {
		return nil, false, false
	}
	val, vok := staticeval.EvaluateOK(ctx, mod.StaticEvaluator.Pure(), expr, "body")
	if !vok || val.IsMarked() || !val.IsKnown() {
		return nil, false, false
	}
	if val.IsNull() {
		return nil, false, true
	}
	if !val.Type().IsObjectType() && !val.Type().IsMapType() {
		return nil, false, false
	}
	var tagsVal cty.Value
	switch {
	case val.Type().IsObjectType() && val.Type().HasAttribute(markers.ARMTagsArgument):
		tagsVal = val.GetAttr(markers.ARMTagsArgument)
	case val.Type().IsMapType() && val.HasIndex(cty.StringVal(markers.ARMTagsArgument)).True():
		tagsVal = val.Index(cty.StringVal(markers.ARMTagsArgument))
	default:
		return nil, false, true
	}
	if tagsVal.IsNull() {
		return nil, false, true
	}
	return hcl.StaticExpr(tagsVal, expr.Range()), true, true
}

// armStaticTagKeys is the set of keys a tags expression states, read from
// configuration alone: an object constructor key by key (so a key whose
// value refers to another resource still counts, and unknown reports that
// one did), a merge() call as the union of its arguments, anything else
// evaluated whole. ok is false when any key cannot be read.
func armStaticTagKeys(ctx context.Context, mod *configs.Module, expr hcl.Expression) (keys map[string]bool, unknown bool, ok bool) {
	if mod == nil || mod.StaticEvaluator == nil {
		return nil, false, false
	}
	eval := mod.StaticEvaluator.Pure()
	keys = map[string]bool{}

	switch e := expr.(type) {
	case *hclsyntax.ObjectConsExpr:
		for _, item := range e.Items {
			k, kok := staticeval.EvaluateOK(ctx, eval, item.KeyExpr, "tags key")
			if !kok || !k.IsWhollyKnown() || k.IsNull() || k.IsMarked() {
				return nil, false, false
			}
			ks, err := convert.Convert(k, cty.String)
			if err != nil {
				return nil, false, false
			}
			keys[ks.AsString()] = true
			if v, vok := staticeval.EvaluateOK(ctx, eval, item.ValueExpr, "tags value"); !vok || !v.IsWhollyKnown() {
				unknown = true
			}
		}
		return keys, unknown, true
	case *hclsyntax.FunctionCallExpr:
		if e.Name == "merge" && !e.ExpandFinal {
			for _, arg := range e.Args {
				got, u, aok := armStaticTagKeys(ctx, mod, arg)
				if !aok {
					return nil, false, false
				}
				unknown = unknown || u
				for k := range got {
					keys[k] = true
				}
			}
			return keys, unknown, true
		}
	}

	val, vok := staticeval.EvaluateOK(ctx, eval, expr, "tags")
	if !vok || val.IsMarked() || !val.IsKnown() {
		return nil, false, false
	}
	if val.IsNull() {
		return keys, false, true
	}
	if !val.Type().IsMapType() && !val.Type().IsObjectType() {
		return nil, false, false
	}
	for it := val.ElementIterator(); it.Next(); {
		k, v := it.Element()
		keys[k.AsString()] = true
		if !v.IsWhollyKnown() {
			unknown = true
		}
	}
	return keys, unknown, true
}
