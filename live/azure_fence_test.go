// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The Azure fence (#1120, ruled on #1122) is two ARM templates an operator
// applies with az alone: live/azure/estate-grant.json (RBAC at the estate's
// resource groups, plus the sweep's read at the subscription) and
// live/azure/estate-boundary.json (Policy deny on a missing or foreign
// tofu-estate tag, denyAction on delete). floci-az stores Policy and never
// evaluates it (live/FLOCI.md), so nothing exercises the rules; this test is
// what holds the templates to what live/MARKERS.md, "Granting an Azure
// estate", says they do.
//
// Proving it red: change the deny's tag to tags['tofu-estat'], drop
// "delete" from the denyAction's actionNames, or write a literal estate
// name where [parameters('estate')] stands; each fails a different check
// below.

const (
	azGrantTemplate    = "azure/estate-grant.json"
	azBoundaryTemplate = "azure/estate-boundary.json"
	azMarkersDoc       = "MARKERS.md"
	azEstateTag        = "tags['tofu-estate']"
	// A policy rule's expression inside an ARM template is escaped with a
	// doubled bracket, so ARM passes it to Policy instead of evaluating it
	// at deployment.
	azPolicyEstateParam = "[[parameters('estate')]"
)

func armTemplate(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var tmpl map[string]any
	if err := json.Unmarshal(raw, &tmpl); err != nil {
		t.Fatalf("%s: not valid JSON: %v", path, err)
	}
	return tmpl
}

// armResources is every resource in a template, nested deployments'
// inline templates included.
func armResources(tmpl map[string]any) []map[string]any {
	var out []map[string]any
	res, _ := tmpl["resources"].([]any)
	for _, r := range res {
		m, _ := r.(map[string]any)
		if m == nil {
			continue
		}
		out = append(out, m)
		if props, _ := m["properties"].(map[string]any); props != nil {
			if inner, _ := props["template"].(map[string]any); inner != nil {
				out = append(out, armResources(inner)...)
			}
		}
	}
	return out
}

func ofType(res []map[string]any, typ string) []map[string]any {
	var out []map[string]any
	for _, r := range res {
		if r["type"] == typ {
			out = append(out, r)
		}
	}
	return out
}

func dig(m map[string]any, path ...string) any {
	var cur any = m
	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[p]
	}
	return cur
}

// requireEstateParameters: both templates take the estate and its groups as
// parameters, with no default that would quietly name one estate.
func requireEstateParameters(t *testing.T, path string, tmpl map[string]any) {
	t.Helper()
	if !strings.Contains(tmpl["$schema"].(string), "subscriptionDeploymentTemplate.json") {
		t.Errorf("%s is not a subscription-scope template; the README command is az deployment sub create", path)
	}
	for name, typ := range map[string]string{"estate": "string", "resourceGroups": "array"} {
		p, _ := dig(tmpl, "parameters", name).(map[string]any)
		if p == nil {
			t.Errorf("%s declares no %s parameter", path, name)
			continue
		}
		if p["type"] != typ {
			t.Errorf("%s: parameter %s is %v, want %s", path, name, p["type"], typ)
		}
		if _, ok := p["defaultValue"]; ok {
			t.Errorf("%s: parameter %s has a default, which hard-codes it for anyone who omits it", path, name)
		}
	}
}

// perGroupDeployment returns the nested deployment that loops over the
// estate's resource groups, failing if the loop is not driven by the
// resourceGroups parameter.
func perGroupDeployment(t *testing.T, path string, tmpl map[string]any) map[string]any {
	t.Helper()
	for _, d := range ofType(armResources(tmpl), "Microsoft.Resources/deployments") {
		if d["resourceGroup"] == "[parameters('resourceGroups')[copyIndex()]]" &&
			dig(d, "copy", "count") == "[length(parameters('resourceGroups'))]" {
			return d
		}
	}
	t.Fatalf("%s has no nested deployment looping over parameters('resourceGroups'); the fence is applied per resource group", path)
	return nil
}

// TestAzureBoundaryTemplateGuardsTheMarker: the deny reads tofu-estate and
// refuses both a missing tag and a different estate, the denyAction covers
// delete, both are mode Indexed, and both are assigned at each of the
// estate's groups with the estate passed through as a parameter.
func TestAzureBoundaryTemplateGuardsTheMarker(t *testing.T) {
	tmpl := armTemplate(t, azBoundaryTemplate)
	requireEstateParameters(t, azBoundaryTemplate, tmpl)
	res := armResources(tmpl)

	defs := ofType(res, "Microsoft.Authorization/policyDefinitions")
	var deny, denyAction map[string]any
	for _, d := range defs {
		switch dig(d, "properties", "policyRule", "then", "effect") {
		case "deny":
			deny = d
		case "denyAction":
			denyAction = d
		}
		if mode := dig(d, "properties", "mode"); mode != "Indexed" {
			t.Errorf("%s: policy definition %v has mode %v, want Indexed (the tag guard only means anything on types that carry tags)", azBoundaryTemplate, d["name"], mode)
		}
		if dig(d, "properties", "parameters", "estate", "type") != "String" {
			t.Errorf("%s: policy definition %v takes no estate parameter", azBoundaryTemplate, d["name"])
		}
	}
	if deny == nil || denyAction == nil {
		t.Fatalf("%s defines %d policies, want one deny and one denyAction", azBoundaryTemplate, len(defs))
	}

	// The deny: anyOf { tag missing, tag != estate }.
	anyOf, _ := dig(deny, "properties", "policyRule", "if", "anyOf").([]any)
	var missing, differs bool
	for _, c := range anyOf {
		cond, _ := c.(map[string]any)
		if cond["field"] != azEstateTag {
			continue
		}
		if cond["exists"] == "false" {
			missing = true
		}
		if cond["notEquals"] == azPolicyEstateParam {
			differs = true
		}
	}
	if !missing {
		t.Errorf("%s: the deny does not refuse a resource whose %s is missing", azBoundaryTemplate, azEstateTag)
	}
	if !differs {
		t.Errorf("%s: the deny does not refuse a resource whose %s differs from %s", azBoundaryTemplate, azEstateTag, azPolicyEstateParam)
	}

	// The denyAction: on the estate's own tag, covering delete, blocking
	// a resource-group delete that would cascade to it.
	cond, _ := dig(denyAction, "properties", "policyRule", "if").(map[string]any)
	if cond["field"] != azEstateTag || cond["equals"] != azPolicyEstateParam {
		t.Errorf("%s: the denyAction's condition is %v, want %s equals %s", azBoundaryTemplate, cond, azEstateTag, azPolicyEstateParam)
	}
	actions, _ := dig(denyAction, "properties", "policyRule", "then", "details", "actionNames").([]any)
	var deletes bool
	for _, a := range actions {
		if a == "delete" {
			deletes = true
		}
	}
	if !deletes {
		t.Errorf("%s: the denyAction names actions %v, which do not include delete", azBoundaryTemplate, actions)
	}
	if got := dig(denyAction, "properties", "policyRule", "then", "details", "cascadeBehaviors", "resourceGroup"); got != "deny" {
		t.Errorf("%s: cascadeBehaviors.resourceGroup is %v, want deny (live/MARKERS.md says a group delete is blocked)", azBoundaryTemplate, got)
	}

	// Assigned at each group, with the estate passed through.
	perGroupDeployment(t, azBoundaryTemplate, tmpl)
	assignments := ofType(res, "Microsoft.Authorization/policyAssignments")
	if len(assignments) != 2 {
		t.Fatalf("%s makes %d policy assignments, want the two definitions assigned per group", azBoundaryTemplate, len(assignments))
	}
	for _, a := range assignments {
		if v := dig(a, "properties", "parameters", "estate", "value"); v != "[parameters('estate')]" {
			t.Errorf("%s: assignment %v passes estate = %v, want the template's own estate parameter", azBoundaryTemplate, a["name"], v)
		}
	}
}

// TestAzureGrantTemplateGrantsTheGroups: the estate's role is assigned at
// each group, the custom role's assignable scopes are exactly the groups,
// and the sweep's read is granted at the subscription.
func TestAzureGrantTemplateGrantsTheGroups(t *testing.T) {
	tmpl := armTemplate(t, azGrantTemplate)
	requireEstateParameters(t, azGrantTemplate, tmpl)
	if dig(tmpl, "parameters", "principalId") == nil {
		t.Errorf("%s declares no principalId parameter", azGrantTemplate)
	}
	res := armResources(tmpl)

	perGroup := perGroupDeployment(t, azGrantTemplate, tmpl)
	inner, _ := dig(perGroup, "properties", "template").(map[string]any)
	if len(ofType(armResources(inner), "Microsoft.Authorization/roleAssignments")) != 1 {
		t.Errorf("%s: the per-group deployment does not make exactly one role assignment", azGrantTemplate)
	}

	var estateRole, sweepRole map[string]any
	for _, r := range ofType(res, "Microsoft.Authorization/roleDefinitions") {
		perms, _ := dig(r, "properties", "permissions").([]any)
		for _, p := range perms {
			acts, _ := p.(map[string]any)["actions"].([]any)
			for _, a := range acts {
				if a == "Microsoft.ResourceGraph/resources/read" {
					sweepRole = r
				}
			}
		}
		if dig(r, "properties", "assignableScopes") == "[variables('groupScopes')]" {
			estateRole = r
		}
	}
	if estateRole == nil {
		t.Errorf("%s: no role definition is assignable at exactly the estate's groups (variables('groupScopes'))", azGrantTemplate)
	} else {
		perms, _ := dig(estateRole, "properties", "permissions").([]any)
		notActions, _ := perms[0].(map[string]any)["notActions"].([]any)
		var noGroupDelete bool
		for _, n := range notActions {
			if n == "Microsoft.Resources/subscriptions/resourceGroups/delete" {
				noGroupDelete = true
			}
		}
		if !noGroupDelete {
			t.Errorf("%s: the estate role can delete its resource group, which removes untaggable children past the delete guard", azGrantTemplate)
		}
	}
	if sweepRole == nil {
		t.Fatalf("%s grants no Microsoft.ResourceGraph/resources/read; the plan's sweep needs it", azGrantTemplate)
	}
	var readAll bool
	perms, _ := dig(sweepRole, "properties", "permissions").([]any)
	for _, a := range perms[0].(map[string]any)["actions"].([]any) {
		if a == "*/read" {
			readAll = true
		}
	}
	if !readAll {
		t.Errorf("%s: the sweep reader does not carry */read", azGrantTemplate)
	}
	scopes, _ := dig(sweepRole, "properties", "assignableScopes").([]any)
	if len(scopes) != 1 || scopes[0] != "[subscription().id]" {
		t.Errorf("%s: the sweep reader is assignable at %v, want the subscription", azGrantTemplate, scopes)
	}
}

// armLiteralEstate is a quoted tofu-estate comparison or assignment against
// something that is not a parameter expression.
var armLiteralEstate = regexp.MustCompile(`"(?:equals|notEquals|value)":\s*"([^"\[][^"]*)"`)

// TestAzureFenceTemplatesNameNoEstate: nothing in either template is
// written for one estate. Every value a condition compares or an
// assignment passes is an expression, never a literal, and the documented
// placeholders are the only estate names that appear.
func TestAzureFenceTemplatesNameNoEstate(t *testing.T) {
	for _, path := range []string{azGrantTemplate, azBoundaryTemplate} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range armLiteralEstate.FindAllStringSubmatch(string(raw), -1) {
			t.Errorf("%s compares or passes the literal %q; an estate's name reaches it only through parameters('estate')", path, m[1])
		}
		if strings.Contains(string(raw), "tofu-estate=") && !strings.Contains(string(raw), "tofu-estate={0}") {
			t.Errorf("%s spells a tofu-estate=<value> outside a format() placeholder", path)
		}
	}
}

// TestAzureFenceIsDocumented: live/MARKERS.md has the section the warning
// and the templates point at, and it names both templates.
func TestAzureFenceIsDocumented(t *testing.T) {
	raw, err := os.ReadFile(azMarkersDoc)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	i := strings.Index(doc, "### Granting an Azure estate")
	if i < 0 {
		t.Fatalf("%s has no \"Granting an Azure estate\" section", azMarkersDoc)
	}
	section := doc[i:]
	if j := strings.Index(section[1:], "\n## "); j >= 0 {
		section = section[:j+1]
	}
	for _, tmpl := range []string{azGrantTemplate, azBoundaryTemplate} {
		if !strings.Contains(section, "live/"+tmpl) {
			t.Errorf("%s's Azure section does not name live/%s", azMarkersDoc, tmpl)
		}
	}
}
