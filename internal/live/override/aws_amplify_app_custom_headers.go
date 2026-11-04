// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package override

import (
	"errors"
	"io"
	"reflect"
	"strings"

	"github.com/zclconf/go-cty/cty"
	"gopkg.in/yaml.v3"
)

// aws_amplify_app's custom_headers is written as YAML under a root
// customHeaders: key, the form Amplify documents, and AWS returns it as JSON
// without that root key: an array of {pattern, headers}. The provider
// compares the strings, so every plan shows the JSON being replaced by the
// YAML. Reproduced on real AWS with provider 6.71.0 on 2026-11-04 (#2309).
var _ = register(Override{
	Name:             "aws-amplify-app-custom-headers",
	Provider:         awsProvider,
	Type:             "aws_amplify_app",
	Approach:         RewriteDiff,
	SDK:              SDKv2,
	Upstream:         "hashicorp/terraform-provider-aws#34318",
	Credits:          []Credit{{Login: "KholdStare", For: "reported the bug"}},
	StockAlternative: "None: AWS rejects the form it returns (no customHeaders root key), so no configuration matches what is read back.",
	Summary:          "custom_headers written as YAML plans an update on every plan, because AWS returns the same headers as JSON without the customHeaders root key",
	ProvenOn:         "6.71.0",
	Requires: map[string]cty.Type{
		"custom_headers": cty.String,
	},
	Plan: KeepPriorWhenEquivalent(
		Equivalence{Path: cty.GetAttrPath("custom_headers"), Equal: sameCustomHeaders},
	),
})

// sameCustomHeaders is an [Equivalence.Equal] for Amplify custom headers:
// equal when both decode, as YAML (which JSON also is), to the same
// structure once a sole customHeaders root key is taken off. Mapping key
// order, comments and scalar quoting do not count; every scalar compares by
// its text, which is what Amplify sends as a header. Anything that does not
// decode to one plain document is never equal.
func sameCustomHeaders(prior, config cty.Value) bool {
	if prior.IsMarked() || config.IsMarked() || prior.Type() != cty.String || config.Type() != cty.String {
		return false
	}
	p, ok := customHeadersValue(prior.AsString())
	if !ok {
		return false
	}
	c, ok := customHeadersValue(config.AsString())
	if !ok {
		return false
	}
	return reflect.DeepEqual(p, c)
}

// customHeadersValue decodes s and returns the header rules it holds: the
// value of a customHeaders key when that is the document's only key, or the
// whole document otherwise.
func customHeadersValue(s string) (any, bool) {
	dec := yaml.NewDecoder(strings.NewReader(s))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		return nil, false
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, false
	}
	v, ok := plainYAML(&doc, 0)
	if !ok {
		return nil, false
	}
	if m, isMap := v.(map[string]any); isMap && len(m) == 1 {
		if inner, has := m["customHeaders"]; has {
			return inner, true
		}
	}
	return v, true
}

// plainYAML turns a YAML node into maps, slices, strings and nil. A scalar
// is its text whatever its quoting or tag, except null. It refuses merge
// keys, duplicate keys, non-scalar keys and alias chains past a fixed depth,
// so nothing it accepts can mean two things.
func plainYAML(n *yaml.Node, depth int) (any, bool) {
	if n == nil || depth > 64 {
		return nil, false
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) != 1 {
			return nil, false
		}
		return plainYAML(n.Content[0], depth+1)
	case yaml.AliasNode:
		return plainYAML(n.Alias, depth+1)
	case yaml.ScalarNode:
		if n.ShortTag() == "!!null" {
			return nil, true
		}
		return n.Value, true
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, e := range n.Content {
			v, ok := plainYAML(e, depth+1)
			if !ok {
				return nil, false
			}
			out = append(out, v)
		}
		return out, true
	case yaml.MappingNode:
		if len(n.Content)%2 != 0 {
			return nil, false
		}
		out := make(map[string]any, len(n.Content)/2)
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || k.ShortTag() == "!!merge" {
				return nil, false
			}
			if _, dup := out[k.Value]; dup {
				return nil, false
			}
			v, ok := plainYAML(n.Content[i+1], depth+1)
			if !ok {
				return nil, false
			}
			out[k.Value] = v
		}
		return out, true
	}
	return nil, false
}
