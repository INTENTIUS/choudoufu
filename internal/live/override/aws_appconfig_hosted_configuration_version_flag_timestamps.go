// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package override

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"

	"github.com/zclconf/go-cty/cty"
)

// For an AWS.AppConfig.FeatureFlags profile, AppConfig stores the hosted
// content with "_createdAt" and "_updatedAt" added to every object under
// "flags" and "values", and the provider reads that back into content. The
// configuration never has them, content forces replacement, so an unchanged
// version plans "content = (sensitive value) # forces replacement" on every
// plan. Reproduced on real AWS with provider 6.71.0 on 2026-11-04 (#2309,
// live/override-repro/appconfig-44847-20273/v1).
var _ = register(Override{
	Name:             "aws-appconfig-hosted-configuration-version-flag-timestamps",
	Provider:         awsProvider,
	Type:             "aws_appconfig_hosted_configuration_version",
	Approach:         RewriteDiff,
	SDK:              SDKv2,
	Upstream:         "hashicorp/terraform-provider-aws#44847",
	Credits:          []Credit{{Login: "KyMidd", For: "reported the bug and wrote the fix in the upstream pull request"}},
	StockAlternative: "Stock OpenTofu offers only lifecycle { ignore_changes = [content] }, which also hides every real content change.",
	Summary:          "The _createdAt and _updatedAt keys AppConfig adds to feature-flag content replace an unchanged version on every plan",
	ProvenOn:         "6.71.0",
	Requires: map[string]cty.Type{
		"content": cty.String,
	},
	Plan: KeepPriorWhenEquivalent(
		Equivalence{Path: cty.GetAttrPath("content"), Equal: sameFlagContent},
	),
})

// appConfigInjectedKeys are the keys AppConfig adds to each object under
// "flags" and "values", as hashicorp/terraform-provider-aws#44847 strips them.
var appConfigInjectedKeys = []string{"_createdAt", "_updatedAt"}

// sameFlagContent is equal when prior, with AppConfig's injected keys taken
// out of each object under "flags" and "values", decodes to the same JSON
// as config. Only prior is stripped, and only at those two levels, so a key
// the configuration itself sets, or any other difference, still plans the
// replacement. It is false when nothing was stripped: a difference that is
// not the injection is not this override's to judge.
func sameFlagContent(prior, config cty.Value) bool {
	if prior.IsMarked() || config.IsMarked() || prior.Type() != cty.String || config.Type() != cty.String {
		return false
	}
	p, ok := decodeJSONObject(prior.AsString())
	if !ok {
		return false
	}
	c, ok := decodeJSONObject(config.AsString())
	if !ok {
		return false
	}
	stripped := false
	for _, level := range []string{"flags", "values"} {
		entries, ok := p[level].(map[string]any)
		if !ok {
			continue
		}
		for _, entry := range entries {
			obj, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			for _, k := range appConfigInjectedKeys {
				if _, has := obj[k]; has {
					delete(obj, k)
					stripped = true
				}
			}
		}
	}
	return stripped && reflect.DeepEqual(p, c)
}

// decodeJSONObject decodes s as one JSON object, keeping numbers as their
// literal text so 1 and 1.0 stay different.
func decodeJSONObject(s string) (map[string]any, bool) {
	dec := json.NewDecoder(bytes.NewReader([]byte(s)))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil || out == nil {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	return out, true
}
