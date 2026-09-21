// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	k8sassets "github.com/intentius/choudoufu/live/kubernetes"
)

// What "installed" has to mean for the estate boundary. GitHub issue #1448,
// section B4.
//
// kubernetescontract.go used to read the policy's NAME, its observed
// generation and its binding's validationActions, and nothing out of
// policy.Spec at all. A ValidatingAdmissionPolicy called
// choudoufu-estate-boundary whose one validation is `true`, or that matches
// only configmaps, or that fails open, was reported as a fence that denies.
// The name is the easiest part of the object to reproduce and the only part
// that was being checked.
//
// # Where the expectation comes from
//
// Two kinds of question, answered two ways, because they age differently.
//
// The STRUCTURE is asked directly: failurePolicy Fail, matchConstraints
// covering secrets for CREATE, UPDATE and DELETE, and a binding whose
// matchResources does not scope the policy away from the records namespace.
// Those are properties of any fence over record Secrets, they do not depend
// on how the CEL is written, and they are spelled out here.
//
// The CEL is compared against live/kubernetes/estate-boundary.yaml itself,
// embedded through [k8sassets.EstateBoundaryYAML]. Proving an arbitrary
// expression equivalent to the shipped one is not something this package can
// do, and pinning today's expressions into Go would put a second copy of
// them one edit away from disagreeing with the file a cluster admin applies
// (#1449 is editing that file's matchConditions right now). So the shipped
// file is the expectation: variables and matchConditions are compared by
// name, validations by expression, and all of them with runs of whitespace
// collapsed, because the file writes them as folded YAML scalars and the API
// server stores what the fold produced.
//
// A policy whose CEL differs is a finding that NAMES what differs. It is not
// a claim that the installed policy is wrong - an operator may have extended
// it on purpose - but it is the end of this check's ability to say the
// records are fenced, and the waiver is the way to say so out loud.

// EstateGrantGroup, EstateGrantResource and EstateGrantVerb are the virtual
// triple the shipped policy's CEL asks the authorizer about, and
// live/kubernetes/estate-grant.yaml grants. No object of that group or
// resource exists anywhere: the triple lives in RBAC and nothing but
// admission reads it.
//
// The estate_boundary assertion asks the same question of THIS identity, so
// a cluster whose policy is in force and whose identity holds no grant is
// reported as what it is - every write refused - rather than as four greens
// (#1448, B6). TestEstateGrantTripleIsTheOneTheShippedPolicyAsksFor pins
// these three to the shipped policy's own CEL, so an edit to that file moves
// them here or fails the suite.
const (
	EstateGrantGroup    = "choudoufu.intentius.io"
	EstateGrantResource = "estates"
	EstateGrantVerb     = "use"
)

// estateBoundaryRecordOperations is what a fence over record Secrets has to
// see. The store creates, updates and deletes them; admission is never
// consulted for a get or a list, so those are not here and cannot be.
var estateBoundaryRecordOperations = []admissionv1.OperationType{
	admissionv1.Create, admissionv1.Update, admissionv1.Delete,
}

var (
	shippedPolicyOnce sync.Once
	shippedPolicy     *admissionv1.ValidatingAdmissionPolicy
	shippedPolicyErr  error
)

// ShippedEstateBoundaryPolicy is live/kubernetes/estate-boundary.yaml's
// ValidatingAdmissionPolicy, parsed once and handed out as a copy, so a
// caller that edits one gets its own.
//
// It is exported for the tests that need a cluster the contract passes:
// since a policy is now read rather than recognised by name, "the policy
// this repository ships" is the only fixture that is one, and a hand-written
// stand-in in another package would be a second copy of the CEL.
func ShippedEstateBoundaryPolicy() (*admissionv1.ValidatingAdmissionPolicy, error) {
	policy, err := shippedEstateBoundaryPolicy()
	if err != nil {
		return nil, err
	}
	return policy.DeepCopy(), nil
}

// shippedEstateBoundaryPolicy is the parsed policy itself, kept unexported so
// this package's own readers cannot copy it needlessly.
func shippedEstateBoundaryPolicy() (*admissionv1.ValidatingAdmissionPolicy, error) {
	shippedPolicyOnce.Do(func() {
		shippedPolicy, shippedPolicyErr = parseShippedEstateBoundaryPolicy(k8sassets.EstateBoundaryYAML())
	})
	return shippedPolicy, shippedPolicyErr
}

// parseShippedEstateBoundaryPolicy reads the ValidatingAdmissionPolicy out of
// a multi-document YAML file. The documents go through JSON on the way in,
// because the Kubernetes types are tagged for JSON and not for YAML.
func parseShippedEstateBoundaryPolicy(raw []byte) (*admissionv1.ValidatingAdmissionPolicy, error) {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	for {
		var doc map[string]any
		err := dec.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("staterecord: kubernetes: reading the shipped estate boundary: %w", err)
		}
		if kind, _ := doc["kind"].(string); kind != "ValidatingAdmissionPolicy" {
			continue
		}
		asJSON, err := json.Marshal(doc)
		if err != nil {
			return nil, fmt.Errorf("staterecord: kubernetes: re-encoding the shipped estate boundary: %w", err)
		}
		policy := &admissionv1.ValidatingAdmissionPolicy{}
		if err := json.Unmarshal(asJSON, policy); err != nil {
			return nil, fmt.Errorf("staterecord: kubernetes: parsing the shipped estate boundary: %w", err)
		}
		return policy, nil
	}
	return nil, fmt.Errorf("staterecord: kubernetes: live/kubernetes/estate-boundary.yaml holds no ValidatingAdmissionPolicy")
}

// normalizeCEL collapses every run of whitespace to one space and trims the
// ends, so the same expression written as a folded YAML scalar, as a quoted
// one line, or with different indentation compares equal.
func normalizeCEL(expr string) string {
	return strings.Join(strings.Fields(expr), " ")
}

// policyCELDifferences names every way installed's CEL differs from the
// shipped policy's, ignoring whitespace and the order things are written in.
// Empty means the two say the same thing.
func policyCELDifferences(installed, shipped *admissionv1.ValidatingAdmissionPolicy) []string {
	var diffs []string

	gotVars := map[string]string{}
	for _, v := range installed.Spec.Variables {
		gotVars[v.Name] = normalizeCEL(v.Expression)
	}
	wantVars := map[string]string{}
	for _, v := range shipped.Spec.Variables {
		wantVars[v.Name] = normalizeCEL(v.Expression)
	}
	diffs = append(diffs, namedExpressionDifferences("variable", gotVars, wantVars)...)

	gotConds := map[string]string{}
	for _, c := range installed.Spec.MatchConditions {
		gotConds[c.Name] = normalizeCEL(c.Expression)
	}
	wantConds := map[string]string{}
	for _, c := range shipped.Spec.MatchConditions {
		wantConds[c.Name] = normalizeCEL(c.Expression)
	}
	diffs = append(diffs, namedExpressionDifferences("match condition", gotConds, wantConds)...)

	got := map[string]int{}
	for _, v := range installed.Spec.Validations {
		got[normalizeCEL(v.Expression)]++
	}
	var missing []string
	for _, v := range shipped.Spec.Validations {
		expr := normalizeCEL(v.Expression)
		if got[expr] > 0 {
			got[expr]--
			continue
		}
		missing = append(missing, expr)
	}
	sort.Strings(missing)
	for _, expr := range missing {
		diffs = append(diffs, fmt.Sprintf("it does not make the shipped policy's validation %q", expr))
	}
	extra := 0
	for _, n := range got {
		extra += n
	}
	if extra > 0 {
		diffs = append(diffs, fmt.Sprintf("it makes %d validation(s) the shipped policy does not", extra))
	}
	return diffs
}

// namedExpressionDifferences compares two name-to-expression maps, reporting
// in a fixed order so one cluster's finding reads the same on every run.
func namedExpressionDifferences(kind string, got, want map[string]string) []string {
	var diffs []string
	names := make([]string, 0, len(want))
	for name := range want {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		have, ok := got[name]
		switch {
		case !ok:
			diffs = append(diffs, fmt.Sprintf("the %s %q the shipped policy declares is missing", kind, name))
		case have != want[name]:
			diffs = append(diffs, fmt.Sprintf("the %s %q is not the expression the shipped policy declares", kind, name))
		}
	}
	extra := make([]string, 0, len(got))
	for name := range got {
		if _, ok := want[name]; !ok {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	for _, name := range extra {
		diffs = append(diffs, fmt.Sprintf("it declares a %s %q the shipped policy does not", kind, name))
	}
	return diffs
}

// policyStructureProblems is what a fence over record Secrets has to be,
// whatever its CEL says: it fails closed, and it is consulted for the writes
// the store makes to the objects the store writes.
//
// undecided carries a selector this check cannot evaluate from here; see
// [selectorExcludes].
func policyStructureProblems(policy *admissionv1.ValidatingAdmissionPolicy, namespace, estate string) (problems, undecided []string) {
	// nil is the API's own default, which is Fail.
	if fp := policy.Spec.FailurePolicy; fp != nil && *fp != admissionv1.Fail {
		problems = append(problems, fmt.Sprintf("the policy's failurePolicy is %s and not Fail, so a write to a record Secret goes through whenever the policy cannot be evaluated", *fp))
	}

	if policy.Spec.MatchConstraints == nil {
		return append(problems, "the policy's matchConstraints are empty, so it is never consulted for any write at all"), nil
	}
	if missing := operationsNotCoveringSecrets(policy.Spec.MatchConstraints.ResourceRules); len(missing) > 0 {
		problems = append(problems, fmt.Sprintf("the policy's matchConstraints do not cover secrets for %s, so those writes to a record Secret are never evaluated", strings.Join(missing, ", ")))
	}
	if excluded := operationsCoveringSecrets(policy.Spec.MatchConstraints.ExcludeResourceRules); len(excluded) > 0 {
		problems = append(problems, fmt.Sprintf("the policy's excludeResourceRules take secrets back out for %s", strings.Join(excluded, ", ")))
	}

	p, u := selectorProblems(policy.Spec.MatchConstraints.NamespaceSelector, policy.Spec.MatchConstraints.ObjectSelector,
		"the policy's", namespace, estate)
	return append(problems, p...), append(undecided, u...)
}

// selectorProblems evaluates the namespace and object selectors a policy or a
// binding carries against what is known about the records namespace and about
// the Secrets this store writes into it.
func selectorProblems(namespaceSelector, objectSelector *metav1.LabelSelector, whose, namespace, estate string) (problems, undecided []string) {
	// Every namespace carries its own name as a label, set by the API server
	// itself, so a selector keyed on that is answerable without reading the
	// namespace. Any other key is not, from an identity scoped to Secrets.
	nsLabels := labels.Set{"kubernetes.io/metadata.name": namespace}
	if excludes, unknown := selectorExcludes(namespaceSelector, nsLabels, nil); excludes {
		problems = append(problems, fmt.Sprintf("%s namespaceSelector does not select namespace %q, so the policy is not in force where the records are", whose, namespace))
	} else if len(unknown) > 0 {
		undecided = append(undecided, fmt.Sprintf("%s namespaceSelector is keyed on %s, which is not readable from here, so whether it selects namespace %q was not established", whose, strings.Join(unknown, ", "), namespace))
	}

	// What every record Secret carries. The estate label is always there;
	// its VALUE is only known when the caller named an estate, so a selector
	// that merely requires the label can be answered either way and one that
	// pins a value cannot.
	recordLabels := labels.Set{KubernetesManagedByLabel: KubernetesManagedByValue}
	presentUnknown := map[string]bool{KubernetesNamespaceLabel: true}
	if estate != "" {
		recordLabels[KubernetesEstateLabel] = estate
	} else {
		presentUnknown[KubernetesEstateLabel] = true
	}
	if excludes, unknown := selectorExcludes(objectSelector, recordLabels, presentUnknown); excludes {
		problems = append(problems, fmt.Sprintf("%s objectSelector does not select this store's record Secrets, so the policy is not in force over them", whose))
	} else if len(unknown) > 0 {
		undecided = append(undecided, fmt.Sprintf("%s objectSelector is keyed on %s, whose value is not known here, so whether it selects the record Secrets was not established", whose, strings.Join(unknown, ", ")))
	}
	return problems, undecided
}

// operationsCoveringSecrets is which of [estateBoundaryRecordOperations]
// these rules reach on a core/v1 Secret.
func operationsCoveringSecrets(rules []admissionv1.NamedRuleWithOperations) []string {
	covered := map[admissionv1.OperationType]bool{}
	for _, rule := range rules {
		if !matchesAny(rule.APIGroups, "") || !matchesAny(rule.APIVersions, "v1") || !matchesAny(rule.Resources, "secrets") {
			continue
		}
		for _, op := range rule.Operations {
			if op == admissionv1.OperationAll {
				for _, want := range estateBoundaryRecordOperations {
					covered[want] = true
				}
				continue
			}
			covered[op] = true
		}
	}
	var names []string
	for _, want := range estateBoundaryRecordOperations {
		if covered[want] {
			names = append(names, string(want))
		}
	}
	return names
}

// operationsNotCoveringSecrets is the complement: what these rules leave out.
func operationsNotCoveringSecrets(rules []admissionv1.NamedRuleWithOperations) []string {
	have := map[string]bool{}
	for _, name := range operationsCoveringSecrets(rules) {
		have[name] = true
	}
	var missing []string
	for _, want := range estateBoundaryRecordOperations {
		if !have[string(want)] {
			missing = append(missing, string(want))
		}
	}
	return missing
}

// matchesAny reports whether values names want, either outright or through
// the "*" every admission rule field accepts.
func matchesAny(values []string, want string) bool {
	for _, v := range values {
		if v == "*" || v == want {
			return true
		}
	}
	return false
}

// bindingScopeProblems is GitHub issue #1448's other half of B4: a binding
// can carry matchResources of its own, and one that selects namespaces other
// than this one, or objects other than the record Secrets, puts the policy in
// force somewhere that is not where the records are.
//
// undecided is for a selector this check cannot evaluate from here - one
// keyed on a namespace label it cannot read, or on the estate label when no
// estate was named. Those are reported as not checked rather than passed.
func bindingScopeProblems(binding *admissionv1.ValidatingAdmissionPolicyBinding, namespace, estate string) (problems, undecided []string) {
	match := binding.Spec.MatchResources
	if match == nil {
		return nil, nil
	}

	problems, undecided = selectorProblems(match.NamespaceSelector, match.ObjectSelector, "the binding's", namespace, estate)

	if len(match.ResourceRules) > 0 {
		if missing := operationsNotCoveringSecrets(match.ResourceRules); len(missing) > 0 {
			problems = append(problems, fmt.Sprintf("the binding's matchResources do not cover secrets for %s", strings.Join(missing, ", ")))
		}
	}
	if excluded := operationsCoveringSecrets(match.ExcludeResourceRules); len(excluded) > 0 {
		problems = append(problems, fmt.Sprintf("the binding's excludeResourceRules take secrets back out for %s", strings.Join(excluded, ", ")))
	}
	return problems, undecided
}

// valuePresentUnknown stands in for a label this check knows is on the object
// and does not know the value of. It is not a value any selector can name -
// a label value cannot hold a NUL - so a requirement that tests the value
// never matches it by accident, and such requirements are reported as
// undecided before the selector is evaluated at all.
const valuePresentUnknown = "\x00value-not-known-here"

// selectorExcludes evaluates sel against the labels that ARE known.
//
// have is key to value. presentUnknown names labels known to be ON the object
// whose value is not known here - the estate label on a record Secret when no
// estate was named. An Exists or DoesNotExist requirement on one of those is
// answerable; anything testing its value is not.
//
// A selector naming a label key nobody here can supply is not decided either
// way: those keys come back in unknown, and the caller reports them rather
// than guessing.
func selectorExcludes(sel *metav1.LabelSelector, have labels.Set, presentUnknown map[string]bool) (excludes bool, unknown []string) {
	if sel == nil || (len(sel.MatchLabels) == 0 && len(sel.MatchExpressions) == 0) {
		return false, nil
	}
	known := labels.Set{}
	for key, value := range have {
		known[key] = value
	}
	seen := map[string]bool{}
	note := func(key string) {
		if !seen[key] {
			seen[key] = true
			unknown = append(unknown, key)
		}
	}
	for key := range sel.MatchLabels {
		if _, ok := have[key]; !ok {
			// matchLabels is equality, so a value nobody knows settles
			// nothing even when the label is known to be there.
			note(key)
		}
	}
	for _, req := range sel.MatchExpressions {
		if _, ok := have[req.Key]; ok {
			continue
		}
		switch {
		case !presentUnknown[req.Key]:
			note(req.Key)
		case req.Operator == metav1.LabelSelectorOpExists || req.Operator == metav1.LabelSelectorOpDoesNotExist:
			known[req.Key] = valuePresentUnknown
		default:
			note(req.Key)
		}
	}
	sort.Strings(unknown)
	if len(unknown) > 0 {
		return false, unknown
	}
	selector, err := metav1.LabelSelectorAsSelector(sel)
	if err != nil {
		return false, []string{"a selector the API server would not parse (" + err.Error() + ")"}
	}
	return !selector.Matches(known), nil
}
