// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	authzv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	k8sassets "github.com/intentius/choudoufu/live/kubernetes"
)

// The places the cluster contract answered "fine" about something it had not
// looked at. GitHub issue #1448, section B.
//
// Every test here asserted the WRONG verdict before it asserted the right
// one: the audit's own probes drove this file's four checks and each came
// back green. What they have in common is the rule the file's own header
// states - a finding that could not be answered carries NotChecked and is
// never OK - so they are together rather than scattered through
// kubernetescontract_test.go, which holds the shapes that were already right.

// reviewAnswer is one authorizer answer, in full. withReviews above can only
// say yes or no, which is why nothing used to build an Allowed AND Denied
// status and why the mutation that dropped the Denied half of reviewSecrets
// survived (#1448, B7 / the audit's C1).
type reviewAnswer struct {
	Allowed bool
	Denied  bool
	Reason  string
}

// withDetailedReviews answers every SelfSubjectAccessReview from the whole
// ResourceAttributes - group, version, resource, name and verb - so a test
// can tell a review about secrets from one about the virtual estate, and can
// hand back a status no yes-or-no reactor could produce.
func withDetailedReviews(cs *fake.Clientset, answer func(*authzv1.ResourceAttributes) reviewAnswer) *fake.Clientset {
	cs.PrependReactor("create", "selfsubjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		review, ok := action.(k8stesting.CreateAction).GetObject().(*authzv1.SelfSubjectAccessReview)
		if !ok {
			return false, nil, nil
		}
		out := review.DeepCopy()
		a := answer(review.Spec.ResourceAttributes)
		out.Status.Allowed = a.Allowed
		out.Status.Denied = a.Denied
		out.Status.Reason = a.Reason
		return true, out, nil
	})
	return cs
}

// reviewsAsked is every SelfSubjectAccessReview the check made, as the
// attributes it asked about.
func reviewsAsked(cs *fake.Clientset) []*authzv1.ResourceAttributes {
	var asked []*authzv1.ResourceAttributes
	for _, action := range cs.Actions() {
		create, ok := action.(k8stesting.CreateAction)
		if !ok || action.GetResource().Resource != "selfsubjectaccessreviews" {
			continue
		}
		review, ok := create.GetObject().(*authzv1.SelfSubjectAccessReview)
		if !ok || review.Spec.ResourceAttributes == nil {
			continue
		}
		asked = append(asked, review.Spec.ResourceAttributes)
	}
	return asked
}

// recordSecret is a Secret as [KubernetesStore] writes one: the labels a
// listing selects on, and no payload, because nothing in the contract reads
// one.
func recordSecret(namespace, name, estate string) *corev1.Secret {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name:      name,
		Namespace: namespace,
		Labels: map[string]string{
			KubernetesManagedByLabel: KubernetesManagedByValue,
			KubernetesEstateLabel:    estate,
		},
	}}
}

func ns(name string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

// TestReadIsolationIsNotCheckedForAnIdentityThatCannotEnumerate is B1's
// headline, in the arrangement that produced it. The identity is scoped the
// way live/kubernetes/OPERATE.md recommends - Secrets in one namespace, no
// list on namespaces, nothing cluster-wide - and it also holds one stray
// RoleBinding into a neighbour's records namespace. Nothing here can see
// that RoleBinding, and the finding used to be Passed while its own text said
// "was not checked".
func TestReadIsolationIsNotCheckedForAnIdentityThatCannotEnumerate(t *testing.T) {
	cs := forbid(withReviews(fake.NewClientset(), func(namespace, verb string) bool {
		// Its own namespace, and the neighbour's, through a RoleBinding
		// there. Nothing cluster-wide: a RoleBinding grants nothing there.
		return namespace == contractNamespace ||
			(namespace == "tofu-records-bob" && (verb == "get" || verb == "list"))
	}), "list", "namespaces")

	f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true, Estate: "alice"}), ClusterReadIsolation)
	if f.OK() {
		t.Fatalf("an identity holding a RoleBinding into a neighbour's records namespace passed read isolation: %s", f.Found)
	}
	if f.Outcome != NotChecked {
		t.Fatalf("outcome %v, want NotChecked: %s", f.Outcome, f.Found)
	}
	for _, want := range []string{
		"not readable from here, not checked",
		"could not be listed here",
		"was not asked at all",
		"live-cluster",
		"may list namespaces",
	} {
		if !strings.Contains(f.Found, want) {
			t.Errorf("the finding does not say %q: %s", want, f.Found)
		}
	}
}

// TestReadIsolationTruncatedReviewIsNotAPass is B1's other half. Twenty-five
// of thirty namespaces reviewed is not an answer about the other five, and it
// used to be reported as a pass that named the numbers.
func TestReadIsolationTruncatedReviewIsNotAPass(t *testing.T) {
	objects := []runtime.Object{ns(contractNamespace)}
	for i := 0; i < 30; i++ {
		objects = append(objects, ns(fmt.Sprintf("tofu-records-neighbour-%02d", i)))
	}
	cs := withReviews(fake.NewClientset(objects...), func(namespace, verb string) bool {
		return namespace == contractNamespace
	})

	f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true, Estate: "alice"}), ClusterReadIsolation)
	if f.OK() {
		t.Fatalf("a review that stopped at %d of 30 namespaces passed read isolation: %s", maxForeignNamespaceReviews, f.Found)
	}
	if f.Outcome != NotChecked {
		t.Fatalf("outcome %v, want NotChecked: %s", f.Outcome, f.Found)
	}
	want := fmt.Sprintf("%d of this cluster's 30 other namespaces were reviewed", maxForeignNamespaceReviews)
	if !strings.Contains(f.Found, want) {
		t.Errorf("the finding does not say how many of how many (%q): %s", want, f.Found)
	}
}

// TestReadIsolationRefusesTwoEstatesInOneNamespace is B2. Two record_store
// blocks given the same `namespace` put two estates' records side by side,
// and the check skipped its own namespace, so the neighbour whose records are
// closest of all was the one nothing asked about. Every identity that may
// list Secrets here - which is every identity the store needs - reads the
// other estate's payloads, and no admission policy is consulted for a read.
func TestReadIsolationRefusesTwoEstatesInOneNamespace(t *testing.T) {
	cs := withReviews(fake.NewClientset(
		ns(contractNamespace),
		recordSecret(contractNamespace, "tofu-record-aaa", "alice"),
		recordSecret(contractNamespace, "tofu-record-bbb", "bob"),
	), func(namespace, verb string) bool { return namespace == contractNamespace })

	findings := check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true, Estate: "alice"})
	f := findingFor(t, findings, ClusterReadIsolation)
	if f.OK() || f.Outcome == NotChecked || f.Outcome == Warned {
		t.Fatalf("a records namespace holding another estate's records was not a refusal: outcome %v: %s", f.Outcome, f.Found)
	}
	if !strings.Contains(f.Found, `"bob"`) && !strings.Contains(f.Found, "bob") {
		t.Errorf("the finding does not name the other estate: %s", f.Found)
	}
	if !strings.Contains(f.Found, contractNamespace) {
		t.Errorf("the finding does not name the namespace they share: %s", f.Found)
	}

	// It is a failure of read_isolation and so it is waivable by that name,
	// like every other failure of that setting.
	refused, _, waived := SplitWaived(findings, []string{string(ClusterReadIsolation)})
	for _, r := range refused {
		if r.Setting == ClusterReadIsolation {
			t.Error("allow_insecure = [\"read_isolation\"] did not reach a shared-namespace refusal")
		}
	}
	found := false
	for _, w := range waived {
		if w.Setting == ClusterReadIsolation {
			found = true
		}
	}
	if !found {
		t.Error("the shared-namespace refusal is not in the waived list, so nothing would print what the waiver costs")
	}
}

// TestReadIsolationSeesANeighbourUnderAnyName is B3. A records namespace is
// whatever a record_store block's `namespace` says, so the enumeration cannot
// go by the tofu-records- prefix: `team-prod-records` was invisible to it and
// the finding said "this cluster holds no other tofu-records-* namespace to
// check" as though that settled the question.
func TestReadIsolationSeesANeighbourUnderAnyName(t *testing.T) {
	cs := withReviews(fake.NewClientset(
		ns(contractNamespace),
		ns("team-prod-records"),
		ns("default"),
		recordSecret("team-prod-records", "tofu-record-ccc", "prod"),
	), func(namespace, verb string) bool {
		return namespace == contractNamespace || namespace == "team-prod-records"
	})

	f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true, Estate: "alice"}), ClusterReadIsolation)
	if f.OK() || f.Outcome == NotChecked || f.Outcome == Warned {
		t.Fatalf("a readable records namespace under a custom name was not a refusal: outcome %v: %s", f.Outcome, f.Found)
	}
	if !strings.Contains(f.Found, "team-prod-records") {
		t.Errorf("the finding does not name the namespace it can read: %s", f.Found)
	}
}

// TestReadIsolationDoesNotSettleANamespaceItCannotList is the other side of
// B3, and the reason the check does not simply assert every readable
// namespace is a breach. `scratch` is readable through get and not through
// list, so whether it holds another estate's records is a question this
// identity cannot ask, and the finding says so instead of passing or
// refusing.
func TestReadIsolationDoesNotSettleANamespaceItCannotList(t *testing.T) {
	cs := withReviews(fake.NewClientset(ns(contractNamespace), ns("scratch")), func(namespace, verb string) bool {
		return namespace == contractNamespace || (namespace == "scratch" && verb == "get")
	})

	f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true, Estate: "alice"}), ClusterReadIsolation)
	if f.OK() {
		t.Fatalf("a namespace this identity may read and may not enumerate was called fenced: %s", f.Found)
	}
	if f.Outcome != NotChecked {
		t.Fatalf("outcome %v, want NotChecked: %s", f.Outcome, f.Found)
	}
	if !strings.Contains(f.Found, "scratch (get)") {
		t.Errorf("the finding does not name the namespace it could not settle: %s", f.Found)
	}
	if !strings.Contains(f.Found, "under any other name") {
		t.Errorf("the finding does not say why a name is not the question: %s", f.Found)
	}
}

// TestReadIsolationSettlesAReadableNamespaceByItsContents is the pass this
// leaves reachable. A cluster-wide reader on a cluster where nothing else
// holds a record Secret is a capability and not a breach, and saying so means
// looking in those namespaces rather than at their names.
func TestReadIsolationSettlesAReadableNamespaceByItsContents(t *testing.T) {
	cs := withReviews(fake.NewClientset(
		ns(contractNamespace),
		ns("default"),
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "some-app-token", Namespace: "default"}},
	), func(namespace, verb string) bool { return true })

	f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true, Estate: "alice"}), ClusterReadIsolation)
	if f.Outcome != Warned {
		t.Fatalf("outcome %v, want Warned for a capability that has exposed nothing: %s", f.Outcome, f.Found)
	}
	if !strings.Contains(f.Found, "holds a record Secret") {
		t.Errorf("the warning does not say what it looked for: %s", f.Found)
	}
	if strings.Contains(f.Found, KubernetesRecordNamespacePrefix+"*") {
		t.Errorf("the warning still settles the question by namespace name: %s", f.Found)
	}
}

// boundaryBindingScopedAway is a binding that names the right policy and
// denies, and selects a namespace that is not the records namespace.
func boundaryBindingScopedAway(namespace string) *admissionv1.ValidatingAdmissionPolicyBinding {
	binding := boundaryBinding(admissionv1.Deny)
	binding.Spec.MatchResources = &admissionv1.MatchResources{
		NamespaceSelector: &metav1.LabelSelector{
			MatchExpressions: []metav1.LabelSelectorRequirement{{
				Key:      "kubernetes.io/metadata.name",
				Operator: metav1.LabelSelectorOpNotIn,
				Values:   []string{namespace},
			}},
		},
	}
	return binding
}

// TestEstateBoundaryReadsThePolicyAndNotOnlyItsName is B4. Each of these
// reported "the policy is installed, observed at generation 1, type-checks
// clean, and its binding denies", because nothing was read out of policy.Spec
// or out of the binding's matchResources at all.
func TestEstateBoundaryReadsThePolicyAndNotOnlyItsName(t *testing.T) {
	allowAll := func(namespace, verb string) bool { return true }

	t.Run("the CEL is true", func(t *testing.T) {
		policy := boundaryPolicy()
		policy.Spec.Variables = nil
		policy.Spec.Validations = []admissionv1.Validation{{Expression: "true"}}
		cs := withReviews(fake.NewClientset(policy, boundaryBinding(admissionv1.Deny)), allowAll)
		f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterEstateBoundary)
		if f.OK() {
			t.Fatalf("a policy whose one validation is `true` passed: %s", f.Found)
		}
		if !strings.Contains(f.Found, "estate-boundary.yaml ships") {
			t.Errorf("the finding does not say what it compared against: %s", f.Found)
		}
		if !strings.Contains(f.Found, "boundToOld") {
			t.Errorf("the finding does not name what differs: %s", f.Found)
		}
	})

	t.Run("it fails open", func(t *testing.T) {
		policy := boundaryPolicy()
		ignore := admissionv1.Ignore
		policy.Spec.FailurePolicy = &ignore
		cs := withReviews(fake.NewClientset(policy, boundaryBinding(admissionv1.Deny)), allowAll)
		f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterEstateBoundary)
		if f.OK() {
			t.Fatalf("a policy with failurePolicy Ignore passed: %s", f.Found)
		}
		if !strings.Contains(f.Found, "failurePolicy is Ignore") {
			t.Errorf("the finding does not name the failure policy: %s", f.Found)
		}
	})

	t.Run("it matches only configmaps", func(t *testing.T) {
		policy := boundaryPolicy()
		policy.Spec.MatchConstraints.ResourceRules = []admissionv1.NamedRuleWithOperations{{
			RuleWithOperations: admissionv1.RuleWithOperations{
				Operations: []admissionv1.OperationType{admissionv1.Create, admissionv1.Update, admissionv1.Delete},
				Rule: admissionv1.Rule{
					APIGroups:   []string{""},
					APIVersions: []string{"v1"},
					Resources:   []string{"configmaps"},
				},
			},
		}}
		cs := withReviews(fake.NewClientset(policy, boundaryBinding(admissionv1.Deny)), allowAll)
		f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterEstateBoundary)
		if f.OK() {
			t.Fatalf("a policy that never sees a Secret passed: %s", f.Found)
		}
		if !strings.Contains(f.Found, "do not cover secrets for CREATE, UPDATE, DELETE") {
			t.Errorf("the finding does not name the writes that go unevaluated: %s", f.Found)
		}
	})

	t.Run("it covers secrets for create only", func(t *testing.T) {
		policy := boundaryPolicy()
		policy.Spec.MatchConstraints.ResourceRules = []admissionv1.NamedRuleWithOperations{{
			RuleWithOperations: admissionv1.RuleWithOperations{
				Operations: []admissionv1.OperationType{admissionv1.Create},
				Rule: admissionv1.Rule{
					APIGroups:   []string{"*"},
					APIVersions: []string{"*"},
					Resources:   []string{"*"},
				},
			},
		}}
		cs := withReviews(fake.NewClientset(policy, boundaryBinding(admissionv1.Deny)), allowAll)
		f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterEstateBoundary)
		if f.OK() {
			t.Fatalf("a policy that never sees an update or a delete passed: %s", f.Found)
		}
		if !strings.Contains(f.Found, "do not cover secrets for UPDATE, DELETE") {
			t.Errorf("the finding does not name the two operations it leaves out: %s", f.Found)
		}
	})

	t.Run("the binding is scoped away from the records namespace", func(t *testing.T) {
		cs := withReviews(fake.NewClientset(boundaryPolicy(), boundaryBindingScopedAway(contractNamespace)), allowAll)
		f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterEstateBoundary)
		if f.OK() {
			t.Fatalf("a binding that selects every namespace but this one passed: %s", f.Found)
		}
		if !strings.Contains(f.Found, "namespaceSelector does not select namespace") {
			t.Errorf("the finding does not say where the policy is not in force: %s", f.Found)
		}
	})

	t.Run("the binding is keyed on a label nobody here can read", func(t *testing.T) {
		binding := boundaryBinding(admissionv1.Deny)
		binding.Spec.MatchResources = &admissionv1.MatchResources{
			NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"tier": "production"}},
		}
		cs := withReviews(fake.NewClientset(boundaryPolicy(), binding), allowAll)
		f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterEstateBoundary)
		if f.OK() {
			t.Fatalf("a binding scoped by a label this identity cannot read passed: %s", f.Found)
		}
		if f.Outcome != NotChecked {
			t.Fatalf("outcome %v, want NotChecked: %s", f.Outcome, f.Found)
		}
		if !strings.Contains(f.Found, "tier") {
			t.Errorf("the finding does not name the label it could not read: %s", f.Found)
		}
	})

	// And the shipped policy itself, applied as the file ships it, still
	// passes: a comparison that refused the real thing would be a check
	// nobody could satisfy.
	t.Run("the shipped policy passes", func(t *testing.T) {
		cs := withReviews(fake.NewClientset(boundaryPolicy(), boundaryBinding(admissionv1.Deny)), allowAll)
		f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterEstateBoundary)
		if !f.OK() {
			t.Fatalf("the policy live/kubernetes/estate-boundary.yaml ships failed its own comparison: %s", f.Found)
		}
	})
}

// TestEstateBoundaryComparesCELThroughWhitespaceAndOrder holds the comparison
// to what it promises: the shipped file writes its expressions as folded YAML
// scalars, an operator may have applied the same policy from a re-indented
// copy, and the order the variables are declared in is not what a policy
// means.
func TestEstateBoundaryComparesCELThroughWhitespaceAndOrder(t *testing.T) {
	policy := boundaryPolicy()
	for i := range policy.Spec.Variables {
		policy.Spec.Variables[i].Expression = "  " + strings.ReplaceAll(policy.Spec.Variables[i].Expression, " ", "\n   ") + "\n"
	}
	for i, j := 0, len(policy.Spec.Variables)-1; i < j; i, j = i+1, j-1 {
		policy.Spec.Variables[i], policy.Spec.Variables[j] = policy.Spec.Variables[j], policy.Spec.Variables[i]
	}
	cs := withReviews(fake.NewClientset(policy, boundaryBinding(admissionv1.Deny)), func(namespace, verb string) bool { return true })
	f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterEstateBoundary)
	if !f.OK() {
		t.Fatalf("re-wrapping and re-ordering the same expressions read as a different policy: %s", f.Found)
	}
}

// virtualEstateCheck is the authorizer call the shipped policy's CEL makes:
// authorizer.group(G).resource(R).name(...).check(V). live's own
// kubernetes_gate_test.go holds estate-grant.yaml to the same triple.
var virtualEstateCheck = regexp.MustCompile(`authorizer\.group\('([^']+)'\)\.resource\('([^']+)'\)\.name\([^)]*\)\.check\('([^']+)'\)`)

// TestEstateGrantTripleIsTheOneTheShippedPolicyAsksFor stops the grant review
// from drifting away from the policy that reads it. Nothing else can: the
// resource is virtual, so no cluster and no schema would ever notice.
func TestEstateGrantTripleIsTheOneTheShippedPolicyAsksFor(t *testing.T) {
	checks := virtualEstateCheck.FindAllStringSubmatch(string(k8sassets.EstateBoundaryYAML()), -1)
	if len(checks) == 0 {
		t.Fatal("the shipped estate boundary makes no authorizer check at all")
	}
	for _, c := range checks {
		if c[1] != EstateGrantGroup || c[2] != EstateGrantResource || c[3] != EstateGrantVerb {
			t.Errorf("the shipped policy asks the authorizer for %s/%s/%s and this package reviews %s/%s/%s",
				c[1], c[2], c[3], EstateGrantGroup, EstateGrantResource, EstateGrantVerb)
		}
	}
}

// estateUseAnswers builds a detailed reactor: secrets in ownNamespace are
// allowed, and the virtual estate grant answers grantHeld.
func estateUseAnswers(ownNamespace string, grantHeld bool) func(*authzv1.ResourceAttributes) reviewAnswer {
	return func(ra *authzv1.ResourceAttributes) reviewAnswer {
		if ra.Group == EstateGrantGroup && ra.Resource == EstateGrantResource {
			if !grantHeld {
				return reviewAnswer{Reason: "no RBAC policy matched"}
			}
			return reviewAnswer{Allowed: true}
		}
		return reviewAnswer{Allowed: ra.Namespace == ownNamespace}
	}
}

// TestEstateBoundaryFailsAnIdentityHoldingNoEstateGrant is B6. The policy
// asks the authorizer whether the writer holds `use` on the virtual estate
// resource, and nothing asked that of THIS identity, so a cluster could be
// four-green while the fence refused every write the run was about to make.
// ClusterContractOptions.Estate was set by both callers and read by nothing.
func TestEstateBoundaryFailsAnIdentityHoldingNoEstateGrant(t *testing.T) {
	cs := withDetailedReviews(fake.NewClientset(boundaryPolicy(), boundaryBinding(admissionv1.Deny)),
		estateUseAnswers(contractNamespace, false))

	f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true, Estate: "alice"}), ClusterEstateBoundary)
	if f.OK() {
		t.Fatalf("an identity the boundary policy refuses every write from passed estate_boundary: %s", f.Found)
	}
	if f.Outcome != Failed {
		t.Fatalf("outcome %v, want Failed: this was read, and it is wrong: %s", f.Outcome, f.Found)
	}
	for _, want := range []string{"`use`", "estates.choudoufu.intentius.io/alice", "estate-grant.yaml", "kubectl apply -f -"} {
		if !strings.Contains(f.Found, want) {
			t.Errorf("the finding does not carry %q: %s", want, f.Found)
		}
	}

	asked := false
	for _, ra := range reviewsAsked(cs) {
		if ra.Group == EstateGrantGroup && ra.Resource == EstateGrantResource {
			asked = true
			if ra.Name != "alice" {
				t.Errorf("the grant review asks about estate %q, want alice", ra.Name)
			}
			if ra.Verb != EstateGrantVerb {
				t.Errorf("the grant review asks for verb %q, want %q", ra.Verb, EstateGrantVerb)
			}
		}
	}
	if !asked {
		t.Error("no review asked about the estate grant at all")
	}

	// Held, and the same cluster passes.
	held := withDetailedReviews(fake.NewClientset(boundaryPolicy(), boundaryBinding(admissionv1.Deny)),
		estateUseAnswers(contractNamespace, true))
	ok := findingFor(t, check(t, held, ClusterContractOptions{NamespaceKnownToExist: true, Estate: "alice"}), ClusterEstateBoundary)
	if !ok.OK() {
		t.Fatalf("an identity granted its estate failed estate_boundary: %s", ok.Found)
	}
}

// TestEstateBoundaryDoesNotRequireTheGrantOfAPlanIdentity is the other half
// of B6, and #1370's rule again: a plan writes no record, so the fence never
// refuses it, and refusing a plan identity for lacking a grant it does not
// need would be the gate everyone waives on their first day.
func TestEstateBoundaryDoesNotRequireTheGrantOfAPlanIdentity(t *testing.T) {
	cs := withDetailedReviews(fake.NewClientset(boundaryPolicy(), boundaryBinding(admissionv1.Deny)),
		estateUseAnswers(contractNamespace, false))

	f := findingFor(t, check(t, cs, ClusterContractOptions{
		NamespaceKnownToExist: true,
		Estate:                "alice",
		RequiredVerbs:         KubernetesPlanVerbs,
	}), ClusterEstateBoundary)
	if !f.OK() {
		t.Fatalf("a plan identity was refused for lacking a grant on a write it does not make: %s", f.Found)
	}
	if !strings.Contains(f.Found, "writes no record") {
		t.Errorf("the finding does not say why the grant was not required: %s", f.Found)
	}
	for _, ra := range reviewsAsked(cs) {
		if ra.Resource == EstateGrantResource {
			t.Error("a plan identity's check asked the authorizer about the estate grant anyway")
		}
	}
}

// TestEstateBoundarySaysWhenNoEstateWasNamed is the honest gap this leaves:
// `choudoufu live-cluster -namespace=<name>` reports on a namespace with no
// configuration behind it, so there is no estate name to ask the authorizer
// about. The policy half is still fully checked and the finding says which
// half was not asked and what asks it.
func TestEstateBoundarySaysWhenNoEstateWasNamed(t *testing.T) {
	cs := withDetailedReviews(fake.NewClientset(boundaryPolicy(), boundaryBinding(admissionv1.Deny)),
		estateUseAnswers(contractNamespace, false))
	f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterEstateBoundary)
	if !f.OK() {
		t.Fatalf("a report with no estate named failed estate_boundary: %s", f.Found)
	}
	if !strings.Contains(f.Found, "-estate=<name>") {
		t.Errorf("the finding does not say what would ask the other half: %s", f.Found)
	}
	for _, ra := range reviewsAsked(cs) {
		if ra.Resource == EstateGrantResource {
			t.Error("a check with no estate named asked the authorizer about an estate anyway")
		}
	}
}

// TestReviewsReadTheAuthorizersDeniedFlag is the audit's surviving mutation
// C1: reviewSecrets reduced to out.Status.Allowed, dropping the
// `&& !out.Status.Denied`. Nothing noticed, because no test built a status
// carrying both. An authorizer that says Allowed AND Denied has an explicit
// deny rule matching, and the deny is the answer.
func TestReviewsReadTheAuthorizersDeniedFlag(t *testing.T) {
	cs := withDetailedReviews(fake.NewClientset(), func(ra *authzv1.ResourceAttributes) reviewAnswer {
		// Everything allowed, and secrets in the records namespace also
		// explicitly denied, which is what a webhook authorizer returning
		// Denied looks like.
		if ra.Namespace == contractNamespace && ra.Resource == "secrets" {
			return reviewAnswer{Allowed: true, Denied: true, Reason: "denied by the cluster's authorizing webhook"}
		}
		return reviewAnswer{Allowed: true}
	})

	f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterNamespaceAccess)
	if f.OK() {
		t.Fatalf("an explicit deny on every verb passed namespace_access: %s", f.Found)
	}
	for _, verb := range KubernetesRecordVerbs {
		if !strings.Contains(f.Found, verb) {
			t.Errorf("the finding does not name the denied verb %q: %s", verb, f.Found)
		}
	}
	for _, v := range f.Verbs {
		if v.Allowed {
			t.Errorf("verb %q reads as allowed although the authorizer denied it", v.Verb)
		}
		if !strings.Contains(v.Reason, "authorizing webhook") {
			t.Errorf("verb %q loses what the authorizer said: %q", v.Verb, v.Reason)
		}
	}

	// The same reactor with Denied off, so the test cannot pass because
	// everything is denied for some other reason.
	allowed := withDetailedReviews(fake.NewClientset(), func(ra *authzv1.ResourceAttributes) reviewAnswer {
		return reviewAnswer{Allowed: true}
	})
	if a := findingFor(t, check(t, allowed, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterNamespaceAccess); !a.OK() {
		t.Fatalf("the same reactor without Denied did not pass, so the test above measured nothing: %s", a.Found)
	}
}

// TestReviewsAskAboutSecretsAndNothingElse is the audit's other surviving
// mutation, C4: the review's Resource changed from "secrets" to "configmaps"
// and every test still passed, because the reactor answered on the namespace
// and the verb and ignored what was being asked about. An identity with
// configmaps and no secrets would have read green.
func TestReviewsAskAboutSecretsAndNothingElse(t *testing.T) {
	cs := withDetailedReviews(fake.NewClientset(ns(contractNamespace), ns("tofu-records-bob")),
		func(ra *authzv1.ResourceAttributes) reviewAnswer { return reviewAnswer{Allowed: true} })
	check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true, Estate: "alice"})

	asked := reviewsAsked(cs)
	if len(asked) == 0 {
		t.Fatal("the contract asked the authorizer nothing")
	}
	secrets := 0
	for _, ra := range asked {
		if ra.Group == EstateGrantGroup && ra.Resource == EstateGrantResource {
			continue
		}
		secrets++
		if ra.Resource != "secrets" || ra.Group != "" || ra.Version != "v1" {
			t.Errorf("a review asks about %q in group %q version %q; the records are core/v1 Secrets and a review about anything else answers a question nobody asked",
				ra.Resource, ra.Group, ra.Version)
		}
	}
	if secrets == 0 {
		t.Fatal("no review asked about secrets at all")
	}
}

// TestCheckClusterContractHonoursTheStoresEstate pins the plumbing B6 needs:
// the estate a store reports on is its own, the way its namespace is, so a
// caller cannot ask about a grant on some other estate's name.
func TestCheckClusterContractHonoursTheStoresEstate(t *testing.T) {
	cs := withDetailedReviews(fake.NewClientset(
		ns(fakeRecordNamespace), boundaryPolicy(), boundaryBinding(admissionv1.Deny),
	), estateUseAnswers(fakeRecordNamespace, false))
	store, err := NewKubernetesStore(KubernetesConfig{
		Secrets:   cs.CoreV1().Secrets(fakeRecordNamespace),
		Clientset: cs,
		Namespace: fakeRecordNamespace,
		Estate:    "alice",
	})
	if err != nil {
		t.Fatalf("NewKubernetesStore: %v", err)
	}
	if _, err := store.CheckClusterContract(context.Background(), ClusterContractOptions{Estate: "somebody-else"}); err != nil {
		t.Fatalf("CheckClusterContract: %v", err)
	}
	for _, ra := range reviewsAsked(cs) {
		if ra.Resource == EstateGrantResource && ra.Name != "alice" {
			t.Errorf("the store reviewed the grant on estate %q, and its records are estate alice's", ra.Name)
		}
	}
}
