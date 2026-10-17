// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"reflect"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/states"
)

const (
	sibRole       = "tl-team-0000-role"
	sibPolicyARN  = "arn:aws:iam::000000000000:policy/tl-team-0000-policy"
	sibManagedARN = "arn:aws:iam::aws:policy/ReadOnlyAccess"
	sibInlineDoc  = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:GetObject","s3:ListBucket"],"Resource":"*"}]}`
)

// siblingState is the identity layer of one terralith team, as a stock apply
// leaves it: the role and policy recorded before anything was attached to
// them, and the attachments recorded after. The attachments sit in a child
// module to prove the index reads every module, as the terralith's pods do.
func siblingState() *states.State {
	state := states.NewState()
	provider := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")}
	set := func(mod *states.Module, typeName, name, attrs string) {
		mod.SetResourceInstanceCurrent(
			addrs.Resource{Mode: addrs.ManagedResourceMode, Type: typeName, Name: name}.Instance(addrs.NoKey),
			&states.ResourceInstanceObjectSrc{AttrsJSON: []byte(attrs), Status: states.ObjectReady},
			provider, addrs.NoKey,
		)
	}
	root := state.RootModule()
	set(root, "aws_iam_role", "team_0000_role", `{"name":"`+sibRole+`","managed_policy_arns":[],"inline_policy":[]}`)
	set(root, "aws_iam_policy", "team_0000_policy", `{"arn":"`+sibPolicyARN+`","attachment_count":0}`)
	set(root, "aws_iam_role_policy", "team_0000_inline", `{"role":"`+sibRole+`","name":"tl-team-0000-inline","policy":`+jsonString(sibInlineDoc)+`}`)
	child := state.EnsureModule(addrs.RootModuleInstance.Child("pod", addrs.NoKey))
	set(child, "aws_iam_role_policy_attachment", "managed", `{"role":"`+sibRole+`","policy_arn":"`+sibManagedARN+`"}`)
	set(child, "aws_iam_role_policy_attachment", "custom", `{"role":"`+sibRole+`","policy_arn":"`+sibPolicyARN+`"}`)
	return state
}

func jsonString(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

var sibInlineTy = cty.Object(map[string]cty.Type{"name": cty.String, "policy": cty.String})

func sibRoleVal(arns []string, inline map[string]string) cty.Value {
	arnSet := cty.SetValEmpty(cty.String)
	if len(arns) > 0 {
		var vs []cty.Value
		for _, a := range arns {
			vs = append(vs, cty.StringVal(a))
		}
		arnSet = cty.SetVal(vs)
	}
	inlineSet := cty.SetValEmpty(sibInlineTy)
	if len(inline) > 0 {
		var vs []cty.Value
		for n, p := range inline {
			vs = append(vs, cty.ObjectVal(map[string]cty.Value{"name": cty.StringVal(n), "policy": cty.StringVal(p)}))
		}
		inlineSet = cty.SetVal(vs)
	}
	return cty.ObjectVal(map[string]cty.Value{
		"name":                 cty.StringVal(sibRole),
		"max_session_duration": cty.NumberIntVal(3600),
		"managed_policy_arns":  arnSet,
		"inline_policy":        inlineSet,
	})
}

func sibRoleBlock() *configschema.Block {
	return &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"name":                 {Type: cty.String, Optional: true},
		"max_session_duration": {Type: cty.Number, Optional: true},
		"managed_policy_arns":  {Type: cty.Set(cty.String), Optional: true, Computed: true},
		"inline_policy":        {Type: cty.Set(sibInlineTy), Optional: true, Computed: true},
	}}
}

func sibPolicyVal(count int64) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"arn":              cty.StringVal(sibPolicyARN),
		"attachment_count": cty.NumberIntVal(count),
	})
}

// TestDriftedAttrs_siblingWrittenAttributesAfterStockApply is the
// terralith demo's false drift: a role and a policy read back seconds after
// a stock apply, carrying exactly what their sibling attachments wrote, must
// not be reported as drifted - and anything beyond that must still be.
func TestDriftedAttrs_siblingWrittenAttributesAfterStockApply(t *testing.T) {
	idx := buildSiblingIndex(siblingState())
	// The live read reformats the document; that is not drift either.
	reformatted := `{"Statement":[{"Action":["s3:GetObject","s3:ListBucket"],"Effect":"Allow","Resource":"*"}],"Version":"2012-10-17"}`

	priorRole := sibRoleVal(nil, nil)
	roleCases := []struct {
		name string
		live cty.Value
		want []string // drifted attribute names
	}{
		{
			"exactly what the siblings wrote",
			sibRoleVal([]string{sibManagedARN, sibPolicyARN}, map[string]string{"tl-team-0000-inline": reformatted}),
			nil,
		},
		{
			"a policy attached by hand as well",
			sibRoleVal([]string{sibManagedARN, sibPolicyARN, "arn:aws:iam::aws:policy/AdministratorAccess"}, map[string]string{"tl-team-0000-inline": sibInlineDoc}),
			[]string{"managed_policy_arns"},
		},
		{
			"an inline policy edited out of band",
			sibRoleVal([]string{sibManagedARN, sibPolicyARN}, map[string]string{"tl-team-0000-inline": `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"*","Resource":"*"}]}`}),
			[]string{"inline_policy"},
		},
		{
			"an inline policy nobody declared",
			sibRoleVal([]string{sibManagedARN, sibPolicyARN}, map[string]string{"tl-team-0000-inline": sibInlineDoc, "handmade": sibInlineDoc}),
			[]string{"inline_policy"},
		},
	}
	for _, tc := range roleCases {
		t.Run("aws_iam_role/"+tc.name, func(t *testing.T) {
			explained := idx.siblingExplained("aws_iam_role", priorRole, tc.live)
			got := attrNames(driftedAttrs(sibRoleBlock(), priorRole, tc.live, explained))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("drifted = %v, want %v", got, tc.want)
			}
		})
	}

	policyBlock := &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"arn":              {Type: cty.String, Computed: true},
		"attachment_count": {Type: cty.Number, Computed: true},
	}}
	for _, tc := range []struct {
		live int64
		want []string
	}{
		{1, nil}, // the custom attachment in the state
		{2, []string{"attachment_count"}},
	} {
		prior, live := sibPolicyVal(0), sibPolicyVal(tc.live)
		explained := idx.siblingExplained("aws_iam_policy", prior, live)
		got := attrNames(driftedAttrs(policyBlock, prior, live, explained))
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("aws_iam_policy attachment_count 0 -> %d: drifted = %v, want %v", tc.live, got, tc.want)
		}
	}

	// A role the state attaches nothing to keeps the old behaviour.
	other := buildSiblingIndex(states.NewState())
	live := sibRoleVal([]string{sibManagedARN}, nil)
	if got := attrNames(driftedAttrs(sibRoleBlock(), priorRole, live, other.siblingExplained("aws_iam_role", priorRole, live))); !reflect.DeepEqual(got, []string{"managed_policy_arns"}) {
		t.Errorf("with no siblings in the state: drifted = %v, want [managed_policy_arns]", got)
	}
}

// attrNames strips changedAttrs' "name (prior -> next)" rendering to names.
func attrNames(drifted []string) []string {
	var out []string
	for _, d := range drifted {
		name, _, _ := strings.Cut(d, " ")
		out = append(out, name)
	}
	return out
}
