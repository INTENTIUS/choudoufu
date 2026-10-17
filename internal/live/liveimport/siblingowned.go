// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"encoding/json"
	"reflect"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/states"
)

// # Attributes another resource in the same state writes
//
// A stock apply records an aws_iam_role the moment CreateRole returns, and
// the AWS provider's create path reads managed_policy_arns and inline_policy
// back at that moment: both empty, because the role's
// aws_iam_role_policy_attachment and aws_iam_role_policy instances depend on
// the role and are created after it. The same apply records an
// aws_iam_policy's attachment_count as 0 for the same reason. Nothing
// refreshes the parent afterwards, so the state file ends up holding the
// pre-attachment values for good.
//
// The first live read after that apply then returns the attached ARNs, the
// inline policies and a non-zero count. That is the cloud agreeing with the
// state, not drifting from it: every one of those values is written by a
// sibling resource the same state file records. Before this, live-import
// reported every attached role and policy in a freshly applied estate as
// DRIFTED (terragucci's terralith demo: 21 of 82 instances, seconds after a
// stock apply on floci; real AWS reads the same way, since the order is the
// provider's, not the emulator's).
//
// The rule is deliberately narrow. An attribute is forgiven only when its
// live value is EXACTLY what the state's own sibling instances say it should
// be. A policy attached by hand, or an inline policy nobody declared, still
// makes the live value differ from that and still reports as drift.

// siblingIndex is what the state's attachment-type instances say about the
// parents they attach to, keyed by the parent's own natural key.
type siblingIndex struct {
	// roleManaged is role name -> the policy ARNs attached to it.
	roleManaged map[string]map[string]bool
	// roleInline is role name -> inline policy name -> the policy
	// document its aws_iam_role_policy recorded.
	roleInline map[string]map[string]string
	// policyAttachments is policy ARN -> how many principals it is
	// attached to.
	policyAttachments map[string]int
}

// The two parent types whose attributes a sibling writes. Package-level so
// the type names are spelled once, next to the table below.
const (
	iamRoleType   = "aws_iam_role"
	iamPolicyType = "aws_iam_policy"
)

// siblingWriter is what one attachment-type instance writes onto its
// parents, read from its own recorded attributes: role (or roles) names the
// role, policy_arn the policy, name and policy the inline policy.
type siblingWriter struct {
	// managedOnRole: policy_arn appears in each named role's
	// managed_policy_arns.
	managedOnRole bool
	// inlineOnRole: name and policy appear in the role's inline_policy.
	inlineOnRole bool
	// countsPolicy: policy_arn's attachment_count rises by one per
	// principal (role, user or group) the instance attaches it to.
	countsPolicy bool
}

// siblingWriters is the hand-verified table of which AWS provider types
// write which parent attributes. No provider schema says that one type's
// instance changes another type's computed attribute; each row is the
// provider's own documentation for the parent attribute ("conflicts with
// aws_iam_role_policy_attachment", "the number of entities ... the policy is
// attached to"). A writer missing here only means its parent stays reported
// as drifted, the behaviour before this table existed.
var siblingWriters = map[string]siblingWriter{
	"aws_iam_role_policy_attachment":  {managedOnRole: true, countsPolicy: true},
	"aws_iam_user_policy_attachment":  {countsPolicy: true},
	"aws_iam_group_policy_attachment": {countsPolicy: true},
	"aws_iam_policy_attachment":       {managedOnRole: true, countsPolicy: true},
	"aws_iam_role_policy":             {inlineOnRole: true},
}

// buildSiblingIndex reads every module's attachment-type instances once.
// Attributes are read from AttrsJSON directly: only plain string and list
// fields are needed, and decoding through a provider schema here would need
// a schema for types this run may never otherwise load.
func buildSiblingIndex(state *states.State) *siblingIndex {
	idx := &siblingIndex{
		roleManaged:       map[string]map[string]bool{},
		roleInline:        map[string]map[string]string{},
		policyAttachments: map[string]int{},
	}
	if state == nil {
		return idx
	}
	for _, mod := range state.Modules {
		for _, res := range mod.Resources {
			w, ok := siblingWriters[res.Addr.Resource.Type]
			if !ok {
				continue
			}
			for _, inst := range res.Instances {
				if inst.Current == nil || len(inst.Current.AttrsJSON) == 0 {
					continue
				}
				var a struct {
					Role      string   `json:"role"`
					User      string   `json:"user"`
					Group     string   `json:"group"`
					Name      string   `json:"name"`
					Policy    string   `json:"policy"`
					PolicyARN string   `json:"policy_arn"`
					Roles     []string `json:"roles"`
					Users     []string `json:"users"`
					Groups    []string `json:"groups"`
				}
				if json.Unmarshal(inst.Current.AttrsJSON, &a) != nil {
					continue
				}
				roles := a.Roles
				if a.Role != "" {
					roles = append(roles, a.Role)
				}
				if w.managedOnRole && a.PolicyARN != "" {
					for _, r := range roles {
						if idx.roleManaged[r] == nil {
							idx.roleManaged[r] = map[string]bool{}
						}
						idx.roleManaged[r][a.PolicyARN] = true
					}
				}
				if w.inlineOnRole && a.Role != "" && a.Name != "" {
					if idx.roleInline[a.Role] == nil {
						idx.roleInline[a.Role] = map[string]string{}
					}
					idx.roleInline[a.Role][a.Name] = a.Policy
				}
				if w.countsPolicy && a.PolicyARN != "" {
					n := len(roles) + len(a.Users) + len(a.Groups)
					if a.User != "" {
						n++
					}
					if a.Group != "" {
						n++
					}
					idx.policyAttachments[a.PolicyARN] += n
				}
			}
		}
	}
	return idx
}

// siblingExplained names the attributes of one instance whose live value is
// exactly what the state's sibling instances wrote, so [driftedAttrs] can
// leave them out. prior is the state's recorded object, used only for the
// parent's natural key (a role's name, a policy's ARN), and live is the
// provider's fresh read.
func (idx *siblingIndex) siblingExplained(typeName string, prior, live cty.Value) map[string]bool {
	if idx == nil || !wholeObject(prior) || !wholeObject(live) {
		return nil
	}
	out := map[string]bool{}
	switch typeName {
	case iamRoleType:
		name := stringAttr(prior, "name")
		if name == "" {
			return nil
		}
		if names, ok := stringSet(live, "managed_policy_arns", nil); ok && sameSet(names, idx.roleManaged[name]) {
			out["managed_policy_arns"] = true
		}
		if inlineMatches(live, idx.roleInline[name]) {
			out["inline_policy"] = true
		}
	case iamPolicyType:
		arn := stringAttr(prior, "arn")
		if arn == "" || !live.Type().HasAttribute("attachment_count") {
			return nil
		}
		n := live.GetAttr("attachment_count")
		if n.IsMarked() || n.IsNull() || !n.IsKnown() || n.Type() != cty.Number {
			return nil
		}
		if got, acc := n.AsBigFloat().Int64(); acc == 0 && got == int64(idx.policyAttachments[arn]) {
			out["attachment_count"] = true
		}
	}
	return out
}

func wholeObject(v cty.Value) bool {
	return v != cty.NilVal && !v.IsNull() && v.IsKnown() && v.Type().IsObjectType()
}

// stringAttr is obj.name as a Go string, or "" when it is absent, null,
// unknown or not a string.
func stringAttr(obj cty.Value, name string) string {
	if !wholeObject(obj) || !obj.Type().HasAttribute(name) {
		return ""
	}
	v := obj.GetAttr(name)
	if v.IsMarked() || v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
		return ""
	}
	return v.AsString()
}

// stringSet reads obj.name, a set or list, into a Go set: each element
// itself when key is nil, or key(element) otherwise. ok is false when the
// attribute cannot be read that way, so the caller forgives nothing.
func stringSet(obj cty.Value, name string, key func(cty.Value) string) (map[string]bool, bool) {
	if !obj.Type().HasAttribute(name) {
		return nil, false
	}
	v := obj.GetAttr(name)
	if v.IsMarked() || !v.IsWhollyKnown() {
		return nil, false
	}
	out := map[string]bool{}
	if v.IsNull() {
		return out, true
	}
	ty := v.Type()
	if !ty.IsSetType() && !ty.IsListType() {
		return nil, false
	}
	for it := v.ElementIterator(); it.Next(); {
		_, el := it.Element()
		if el.IsMarked() {
			return nil, false
		}
		var s string
		switch {
		case key != nil:
			s = key(el)
		case !el.IsNull() && el.Type() == cty.String:
			s = el.AsString()
		}
		if s == "" {
			return nil, false
		}
		out[s] = true
	}
	return out, true
}

// inlineMatches reports whether live's inline_policy holds exactly the
// state's aws_iam_role_policy instances for this role: the same names, and
// for each one a document that says the same thing as the one recorded.
// "The same thing" is JSON equality after decoding, so key order and
// whitespace do not count; anything else - a statement added out of band,
// an inline policy nobody declared - still reads as drift.
func inlineMatches(live cty.Value, want map[string]string) bool {
	if !live.Type().HasAttribute("inline_policy") {
		return false
	}
	v := live.GetAttr("inline_policy")
	if v.IsMarked() || !v.IsWhollyKnown() {
		return false
	}
	if v.IsNull() {
		return len(want) == 0
	}
	if !v.Type().IsSetType() && !v.Type().IsListType() {
		return false
	}
	if v.LengthInt() != len(want) {
		return false
	}
	for it := v.ElementIterator(); it.Next(); {
		_, el := it.Element()
		name := stringAttr(el, "name")
		doc, ok := want[name]
		if name == "" || !ok || !sameJSON(stringAttr(el, "policy"), doc) {
			return false
		}
	}
	return true
}

func sameJSON(a, b string) bool {
	if a == b {
		return true
	}
	var av, bv any
	if json.Unmarshal([]byte(a), &av) != nil || json.Unmarshal([]byte(b), &bv) != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
