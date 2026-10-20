// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/armtypes"
)

// The Azure fence (#1120, ruled on #1122) is two ARM templates an operator
// applies with az alone: live/azure/estate-grant.json (RBAC at the estate's
// resource groups, plus the sweep's read at the subscription) and
// live/azure/estate-boundary.json (Policy deny on a missing or foreign
// tofu-estate tag, and the opt-in delete lock, a denyAction on delete that
// is assigned only when deleteLock=true; ruled on #1122, 2026-10-20).
// floci-az evaluates Policy since 30000b0 (live/FLOCI.md), but no scenario
// has run against it, so nothing has exercised the rules yet; this test is
// what holds the templates to what live/MARKERS.md, "Granting an Azure
// estate", says they do.
//
// Proving it red: change the deny's tag to tags['tofu-estat'], drop
// "delete" from the denyAction's actionNames, write a literal estate
// name where [parameters('estate')] stands, or drop the condition from the
// delete-lock assignment; each fails a different check below.

const (
	azGrantTemplate    = "azure/estate-grant.json"
	azBoundaryTemplate = "azure/estate-boundary.json"
	azMarkersDoc       = "MARKERS.md"
	azEstateTag        = "tags['tofu-estate']"
	// A policy rule's expression inside an ARM template is escaped with a
	// doubled bracket, so ARM passes it to Policy instead of evaluating it
	// at deployment.
	azPolicyEstateParam = "[[parameters('estate')]"
	// azPolicyTaggedAfterCreateParam is the marker guard's exemption for
	// types created untagged and tagged by a second call.
	azPolicyTaggedAfterCreateParam = "[[parameters('taggedAfterCreate')]"
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

// armDeployed is the resources a deployment of tmpl with params would
// make, nested deployments' inline templates included: a resource whose
// condition is false is left out, with everything inside it. params
// override the template's defaults. The only condition form it reads is
// [parameters('<bool>')], and a nested template's parameter only as a
// value passed straight through from [parameters('<name>')]; any other
// form fails the test rather than being guessed at.
func armDeployed(t *testing.T, tmpl map[string]any, params map[string]any) []map[string]any {
	t.Helper()
	vals := map[string]any{}
	decl, _ := tmpl["parameters"].(map[string]any)
	for name, d := range decl {
		if dv, ok := d.(map[string]any)["defaultValue"]; ok {
			vals[name] = dv
		}
	}
	for k, v := range params {
		vals[k] = v
	}
	paramRef := regexp.MustCompile(`^\[parameters\('([A-Za-z]+)'\)\]$`)
	var out []map[string]any
	res, _ := tmpl["resources"].([]any)
	for _, r := range res {
		m, _ := r.(map[string]any)
		if m == nil {
			continue
		}
		if c, ok := m["condition"]; ok {
			sm := paramRef.FindStringSubmatch(fmt.Sprint(c))
			if sm == nil {
				t.Fatalf("resource %v has condition %v, which this test cannot evaluate", m["name"], c)
			}
			on, ok := vals[sm[1]].(bool)
			if !ok {
				t.Fatalf("resource %v's condition reads parameter %s, which is %v, not a bool", m["name"], sm[1], vals[sm[1]])
			}
			if !on {
				continue
			}
		}
		out = append(out, m)
		props, _ := m["properties"].(map[string]any)
		inner, _ := props["template"].(map[string]any)
		if inner == nil {
			continue
		}
		passed := map[string]any{}
		pp, _ := props["parameters"].(map[string]any)
		for name, p := range pp {
			v := p.(map[string]any)["value"]
			if sm := paramRef.FindStringSubmatch(fmt.Sprint(v)); sm != nil {
				v = vals[sm[1]]
			}
			passed[name] = v
		}
		out = append(out, armDeployed(t, inner, passed)...)
	}
	return out
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
// delete, both are mode Indexed, and each is assigned at each of the
// estate's groups with the estate passed through as a parameter (the
// denyAction only when deleteLock is on: TestAzureDeleteLockIsOptIn).
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

	// The deny: anyOf { allOf { tag missing, type not tagged after
	// create }, allOf { tag present, tag != estate } }. The second clause
	// carries no type exemption: a resource of any type that names another
	// estate is refused.
	anyOf, _ := dig(deny, "properties", "policyRule", "if", "anyOf").([]any)
	var missing, differs bool
	for _, c := range anyOf {
		group, _ := c.(map[string]any)
		all, _ := group["allOf"].([]any)
		var absent, present, other, exempt, typed bool
		for _, a := range all {
			cond, _ := a.(map[string]any)
			switch {
			case cond["field"] == azEstateTag && cond["exists"] == "false":
				absent = true
			case cond["field"] == azEstateTag && cond["exists"] == "true":
				present = true
			case cond["field"] == azEstateTag && cond["notEquals"] == azPolicyEstateParam:
				other = true
			case cond["field"] == "type" && cond["notIn"] == azPolicyTaggedAfterCreateParam:
				exempt = true
			default:
				typed = true
			}
		}
		if absent && exempt && !typed && len(all) == 2 {
			missing = true
		}
		if present && other && !typed && len(all) == 2 {
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
		t.Fatalf("%s declares %d policy assignments, want the marker guard and the delete lock, each assigned per group", azBoundaryTemplate, len(assignments))
	}
	for _, a := range assignments {
		if v := dig(a, "properties", "parameters", "estate", "value"); v != "[parameters('estate')]" {
			t.Errorf("%s: assignment %v passes estate = %v, want the template's own estate parameter", azBoundaryTemplate, a["name"], v)
		}
	}
	if v := dig(deny, "properties", "parameters", "taggedAfterCreate", "type"); v != "Array" {
		t.Errorf("%s: the marker guard's taggedAfterCreate parameter is %v, want Array", azBoundaryTemplate, v)
	}
	var passed bool
	for _, a := range assignments {
		if dig(a, "properties", "parameters", "taggedAfterCreate", "value") == "[parameters('taggedAfterCreate')]" {
			passed = true
		}
	}
	if !passed {
		t.Errorf("%s: no assignment passes the template's taggedAfterCreate through to the marker guard", azBoundaryTemplate)
	}
}

// TestAzureDeleteLockIsOptIn (#2133 finding 1, ruled on #1122 2026-10-20):
// deployed with its defaults, the boundary template assigns the marker
// guard at each group and no denyAction at all, so resource-group RBAC
// alone governs deletes, as on AWS and Kubernetes. deleteLock=true adds
// the denyAction's definition and its assignment, and that denyAction
// still covers delete. The marker guard is assigned either way.
func TestAzureDeleteLockIsOptIn(t *testing.T) {
	tmpl := armTemplate(t, azBoundaryTemplate)
	p, _ := dig(tmpl, "parameters", "deleteLock").(map[string]any)
	if p == nil || p["type"] != "bool" || p["defaultValue"] != false {
		t.Fatalf("%s: parameter deleteLock is %v, want a bool defaulting to false", azBoundaryTemplate, p)
	}

	// effects returns the effect of each policy definition the deployment
	// makes and of the definition each of its assignments names. An
	// assignment gets its definition's id through the per-group
	// deployment's parameters, built from the definition's name variable.
	declared := map[string]string{}
	for _, d := range ofType(armResources(tmpl), "Microsoft.Authorization/policyDefinitions") {
		declared[fmt.Sprint(d["name"])] = fmt.Sprint(dig(d, "properties", "policyRule", "then", "effect"))
	}
	defVar := regexp.MustCompile(`variables\('([A-Za-z]+)'\)`)
	effects := func(params map[string]any) (defs, assigned []string) {
		res := armDeployed(t, tmpl, params)
		made := map[string]bool{}
		for _, d := range ofType(res, "Microsoft.Authorization/policyDefinitions") {
			made[fmt.Sprint(d["name"])] = true
			defs = append(defs, declared[fmt.Sprint(d["name"])])
		}
		deps := ofType(res, "Microsoft.Resources/deployments")
		if len(deps) != 1 {
			t.Fatalf("%s with %v makes %d deployments, want the one per-group deployment", azBoundaryTemplate, params, len(deps))
		}
		for _, a := range ofType(res, "Microsoft.Authorization/policyAssignments") {
			ref := strings.TrimSuffix(strings.TrimPrefix(fmt.Sprint(dig(a, "properties", "policyDefinitionId")), "[parameters('"), "')]")
			sm := defVar.FindStringSubmatch(fmt.Sprint(dig(deps[0], "properties", "parameters", ref, "value")))
			if sm == nil {
				t.Fatalf("%s: assignment %v's definition id is not built from a definition name variable", azBoundaryTemplate, a["name"])
			}
			def := "[variables('" + sm[1] + "')]"
			if !made[def] {
				t.Errorf("%s with %v assigns %s, which that deployment does not define", azBoundaryTemplate, params, def)
			}
			assigned = append(assigned, declared[def])
		}
		return defs, assigned
	}

	for _, tc := range []struct {
		name   string
		params map[string]any
		want   []string
	}{
		{"default", map[string]any{"deleteLock": false}, []string{"deny"}},
		{"deleteLock=true", map[string]any{"deleteLock": true}, []string{"deny", "denyAction"}},
	} {
		defs, assigned := effects(tc.params)
		if strings.Join(defs, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s with %s defines policies with effects %v, want %v", azBoundaryTemplate, tc.name, defs, tc.want)
		}
		if strings.Join(assigned, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s with %s assigns policies with effects %v, want %v", azBoundaryTemplate, tc.name, assigned, tc.want)
		}
	}

	// Turned on, the lock is the delete guard the other test reads: it
	// still names delete.
	for _, d := range ofType(armDeployed(t, tmpl, map[string]any{"deleteLock": true}), "Microsoft.Authorization/policyDefinitions") {
		if dig(d, "properties", "policyRule", "then", "effect") != "denyAction" {
			continue
		}
		actions, _ := dig(d, "properties", "policyRule", "then", "details", "actionNames").([]any)
		if len(actions) != 1 || actions[0] != "delete" {
			t.Errorf("%s: with deleteLock=true the denyAction names %v, want [delete]", azBoundaryTemplate, actions)
		}
	}
}

// azAfterCreateOutsideGroups are the after-create azurerm types the
// boundary template has no reason to exempt, each with why: no assignment
// it makes can reach them.
var azAfterCreateOutsideGroups = map[string]string{
	"azurerm_subscription": "a subscription is in no resource group; the template assigns only at the estate's groups",
}

// TestAzureBoundaryExemptsExactlyTheAfterCreateTypes (#1120 unit 12): the
// boundary template's default taggedAfterCreate is the ARM type of every
// after-create azurerm type (internal/live/armtypes, generated from
// azurerm's Create functions) whose id lives in a resource group, read off
// that type's id template in live/azurerm-ids.json, and nothing else. A
// provider bump that adds an after-create type, or moves one's ARM type,
// fails here until the template's default follows it; a hand-added type
// fails too.
func TestAzureBoundaryExemptsExactlyTheAfterCreateTypes(t *testing.T) {
	tmpl := armTemplate(t, azBoundaryTemplate)
	raw, _ := dig(tmpl, "parameters", "taggedAfterCreate", "defaultValue").([]any)
	got := map[string]bool{}
	for _, v := range raw {
		s, _ := v.(string)
		got[strings.ToLower(s)] = true
	}

	data, err := os.ReadFile("azurerm-ids.json")
	if err != nil {
		t.Fatal(err)
	}
	var ids struct {
		Types []struct {
			Type     string `json:"type"`
			Template string `json:"template"`
		} `json:"types"`
	}
	if err := json.Unmarshal(data, &ids); err != nil {
		t.Fatal(err)
	}
	templates := map[string]string{}
	for _, r := range ids.Types {
		templates[r.Type] = r.Template
	}

	want := map[string]string{}
	for _, row := range armtypes.DefaultRMTags().Rows() {
		if row.Create == armtypes.CreateInCreate {
			continue
		}
		if why, ok := azAfterCreateOutsideGroups[row.Type]; ok {
			if strings.Contains(templates[row.Type], "/resourceGroups/") {
				t.Errorf("%s is listed as outside any group (%s), and its id template %s is in one", row.Type, why, templates[row.Type])
			}
			continue
		}
		arm, ok := armtypes.AzurermARMType(row.Type)
		if !ok || !strings.Contains(templates[row.Type], "/resourceGroups/") {
			t.Errorf("after-create type %s has no resource-group ARM type to exempt (template %q); exempt it by hand in the template and say why here", row.Type, templates[row.Type])
			continue
		}
		want[strings.ToLower(arm)] = row.Type
	}
	for arm, tf := range want {
		if !got[arm] {
			t.Errorf("%s: taggedAfterCreate does not list %s, the ARM type of after-create %s, so the marker guard denies its untagged create", azBoundaryTemplate, arm, tf)
		}
	}
	for arm := range got {
		if _, ok := want[arm]; !ok {
			t.Errorf("%s: taggedAfterCreate lists %s, which is no after-create azurerm type's; every listed type is a type the guard lets exist untagged", azBoundaryTemplate, arm)
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
			t.Errorf("%s: the estate role can delete its resource group, which removes every resource in it at once, untaggable children included", azGrantTemplate)
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
