// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	authzv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// These tests drive the cluster contract over client-go's fake clientset,
// which answers every request from an object tracker. What that CAN measure
// is the whole of this file's judgement: which questions get asked, how an
// authorizer's yes or no becomes a finding, and what a refused read turns
// into. What it cannot measure is anything about a real authorizer, because
// the fake has none - a SelfSubjectAccessReview against it answers whatever
// the reactor below says. The claim 39 arms are where the answers come from
// a real API server.

// allowFunc decides one review. namespace is "" for a cluster-wide question.
type allowFunc func(namespace, verb string) bool

// withReviews makes cs answer SelfSubjectAccessReview creates through allow.
// Without it the fake returns the review unchanged, whose Status.Allowed is
// false, so every verb would read as denied for the wrong reason.
func withReviews(cs *fake.Clientset, allow allowFunc) *fake.Clientset {
	cs.PrependReactor("create", "selfsubjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		review, ok := action.(k8stesting.CreateAction).GetObject().(*authzv1.SelfSubjectAccessReview)
		if !ok {
			return false, nil, nil
		}
		ra := review.Spec.ResourceAttributes
		out := review.DeepCopy()
		out.Status.Allowed = allow(ra.Namespace, ra.Verb)
		if !out.Status.Allowed {
			out.Status.Reason = "no RBAC policy matched"
		}
		return true, out, nil
	})
	return cs
}

// forbid makes every verb on resource answer 403, the way the API server
// answers an identity with no rule for it.
func forbid(cs *fake.Clientset, verb, resource string) *fake.Clientset {
	cs.PrependReactor(verb, resource, func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, k8serrors.NewForbidden(schema.GroupResource{Resource: resource}, "", nil)
	})
	return cs
}

const contractNamespace = "tofu-records-alice"

func findingFor(t *testing.T, findings []ClusterFinding, setting ClusterSetting) ClusterFinding {
	t.Helper()
	for _, f := range findings {
		if f.Setting == setting {
			return f
		}
	}
	t.Fatalf("no finding for %q in %v", setting, findings)
	return ClusterFinding{}
}

func check(t *testing.T, cs kubernetes.Interface, opts ClusterContractOptions) []ClusterFinding {
	t.Helper()
	if opts.Namespace == "" {
		opts.Namespace = contractNamespace
	}
	findings, err := CheckClusterContract(context.Background(), cs, opts)
	if err != nil {
		t.Fatalf("CheckClusterContract: %v", err)
	}
	if len(findings) != len(ClusterSettings) {
		t.Fatalf("got %d findings, want one per setting (%d)", len(findings), len(ClusterSettings))
	}
	for i, f := range findings {
		if f.Setting != ClusterSettings[i] {
			t.Fatalf("finding %d is %q, want %q: the order findings are reported in is part of the report", i, f.Setting, ClusterSettings[i])
		}
		if f.OK && (f.NotChecked || f.Warning) {
			t.Fatalf("finding %q is OK and also NotChecked or a Warning; a question that was not answered, and a concern, are neither of them a pass", f.Setting)
		}
		if f.Found == "" {
			t.Fatalf("finding %q says nothing about what was found", f.Setting)
		}
	}
	return findings
}

// TestClusterContractNamespaceAccessReviewsEveryVerb is assertion 1's shape:
// all five verbs are reviewed whatever the run needs, so the report says what
// this identity holds and not only whether it is enough.
func TestClusterContractNamespaceAccessReviewsEveryVerb(t *testing.T) {
	cs := withReviews(fake.NewClientset(), func(ns, verb string) bool { return ns == contractNamespace })
	f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterNamespaceAccess)

	if !f.OK {
		t.Fatalf("an identity allowed every verb failed the assertion: %s", f.Found)
	}
	if len(f.Verbs) != len(KubernetesRecordVerbs) {
		t.Fatalf("reviewed %d verbs, want %d", len(f.Verbs), len(KubernetesRecordVerbs))
	}
	for i, v := range f.Verbs {
		if v.Verb != KubernetesRecordVerbs[i] {
			t.Errorf("verb %d is %q, want %q", i, v.Verb, KubernetesRecordVerbs[i])
		}
		if !v.Allowed || !v.Required {
			t.Errorf("verb %q: allowed=%v required=%v, want both true", v.Verb, v.Allowed, v.Required)
		}
	}
}

// TestClusterContractNamespaceAccessNeverWrites is the assertion's method,
// and the reason the issue names SelfSubjectAccessReview rather than "try it
// and see". A permission probe that writes has to write something, and in
// this namespace the only thing there is to write is a record.
func TestClusterContractNamespaceAccessNeverWrites(t *testing.T) {
	cs := withReviews(fake.NewClientset(), func(ns, verb string) bool { return true })
	check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true})

	for _, action := range cs.Actions() {
		if action.GetResource().Resource == "secrets" {
			t.Errorf("the contract check acted on a Secret (%s %s); it must ask the authorizer, never attempt a write",
				action.GetVerb(), action.GetResource().Resource)
		}
	}
}

// TestClusterContractNamespaceAccessNamesTheVerbsItLacks is the red arm of
// assertion 1: three of the five denied, and the refusal names those three.
func TestClusterContractNamespaceAccessNamesTheVerbsItLacks(t *testing.T) {
	readOnly := func(ns, verb string) bool {
		return ns == contractNamespace && (verb == "get" || verb == "list")
	}
	f := findingFor(t, check(t, withReviews(fake.NewClientset(), readOnly), ClusterContractOptions{NamespaceKnownToExist: true}), ClusterNamespaceAccess)

	if f.OK {
		t.Fatal("an identity with no create, update or delete passed the assertion")
	}
	for _, verb := range []string{"create", "update", "delete"} {
		if !strings.Contains(f.Found, verb) {
			t.Errorf("the finding does not name the missing verb %q: %s", verb, f.Found)
		}
	}
	summary, detail := ClusterContractRefusal(contractNamespace, f)
	if summary == "" || !strings.Contains(detail, contractNamespace) {
		t.Errorf("the refusal does not name the namespace: %q / %q", summary, detail)
	}
}

// TestClusterContractPlanOnlyIdentityIsNotRefusedForWriteVerbs is #1370's
// lesson carried onto this store (GitHub issue #1393's third Accept item). A
// plan reads records and writes none, so an identity with get and list is a
// plan identity and not a broken one. It is told what it lacks; it is not
// refused for it.
func TestClusterContractPlanOnlyIdentityIsNotRefusedForWriteVerbs(t *testing.T) {
	readOnly := func(ns, verb string) bool {
		return ns == contractNamespace && (verb == "get" || verb == "list")
	}
	f := findingFor(t, check(t, withReviews(fake.NewClientset(), readOnly), ClusterContractOptions{
		NamespaceKnownToExist: true,
		RequiredVerbs:         KubernetesPlanVerbs,
	}), ClusterNamespaceAccess)

	if !f.OK {
		t.Fatalf("a plan-only identity was refused for lacking create, update and delete: %s", f.Found)
	}
	for _, v := range f.Verbs {
		wantRequired := v.Verb == "get" || v.Verb == "list"
		if v.Required != wantRequired {
			t.Errorf("verb %q: required=%v, want %v for a plan-only run", v.Verb, v.Required, wantRequired)
		}
		if v.Allowed != wantRequired {
			t.Errorf("verb %q: allowed=%v, want %v", v.Verb, v.Allowed, wantRequired)
		}
	}
	if !strings.Contains(f.Found, "denied: create, update, delete") {
		t.Errorf("the finding does not report the verbs this identity lacks: %s", f.Found)
	}
}

// TestClusterContractReportsAnAbsentNamespaceInTheStoresOwnWords is the "do
// not double-report" half. A caller that has already used the namespace says
// so and nothing is re-probed; one that has not gets the same sentence
// *NamespaceMissingError carries, so the contract check and the store's
// refusal on use cannot drift apart.
func TestClusterContractReportsAnAbsentNamespaceInTheStoresOwnWords(t *testing.T) {
	allowAll := func(ns, verb string) bool { return true }

	t.Run("probed and absent", func(t *testing.T) {
		cs := withReviews(fake.NewClientset(), allowAll)
		f := findingFor(t, check(t, cs, ClusterContractOptions{}), ClusterNamespaceAccess)
		if f.OK {
			t.Fatal("a missing records namespace passed the assertion")
		}
		want := "create it with `kubectl create namespace " + contractNamespace + "`"
		if !strings.Contains(f.Found, want) {
			t.Errorf("the finding does not carry NamespaceMissingError's own words (%q): %s", want, f.Found)
		}
	})

	t.Run("known to exist, not probed", func(t *testing.T) {
		cs := withReviews(fake.NewClientset(), allowAll)
		f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterNamespaceAccess)
		if !f.OK {
			t.Fatalf("a namespace the caller has already used was reported as a problem: %s", f.Found)
		}
		for _, action := range cs.Actions() {
			if action.GetResource().Resource == "namespaces" && action.GetVerb() == "get" {
				t.Error("the namespace was re-probed although the caller had already used it; that read needs a permission the recommended Role has no reason to hold, and two answers to one question is what 'do not double-report' means")
			}
		}
	})

	t.Run("probe forbidden", func(t *testing.T) {
		cs := forbid(withReviews(fake.NewClientset(), allowAll), "get", "namespaces")
		f := findingFor(t, check(t, cs, ClusterContractOptions{}), ClusterNamespaceAccess)
		if !f.OK {
			t.Fatalf("an identity that may do everything to Secrets was refused for not being able to get the namespace: %s", f.Found)
		}
		if !strings.Contains(f.Found, "was not established here") {
			t.Errorf("the finding does not say the namespace's existence was not established: %s", f.Found)
		}
		if !strings.Contains(f.Found, "allowed: get, list, create, update, delete") {
			t.Errorf("the finding lost the verb review because the namespace probe was denied: %s", f.Found)
		}
	})

	// The case that measured nothing until it was found on kind: an
	// identity missing verbs AND unable to get the namespace was told only
	// about the namespace, so the refusal never said which verb to grant.
	t.Run("probe forbidden and verbs missing", func(t *testing.T) {
		cs := forbid(withReviews(fake.NewClientset(), func(ns, verb string) bool {
			return verb == "get" || verb == "list"
		}), "get", "namespaces")
		f := findingFor(t, check(t, cs, ClusterContractOptions{}), ClusterNamespaceAccess)
		if f.OK {
			t.Fatal("an identity with no create, update or delete passed the apply question")
		}
		for _, verb := range []string{"create", "update", "delete"} {
			if !strings.Contains(f.Found, verb) {
				t.Errorf("the finding does not name the missing verb %q: %s", verb, f.Found)
			}
		}
	})
}

// TestClusterContractReadIsolationSeesAClusterWideReader is assertion 2's
// minimum, and the one that needs no permission to ask: either verb
// cluster-wide is seen and said, by name.
//
// Whether it refuses or warns is the next two tests' subject, and depends on
// whether another estate's records are there to be read.
func TestClusterContractReadIsolationSeesAClusterWideReader(t *testing.T) {
	for _, verb := range []string{"get", "list"} {
		t.Run(verb+" everywhere", func(t *testing.T) {
			cs := withReviews(fake.NewClientset(), func(ns, v string) bool {
				return ns == contractNamespace || v == verb
			})
			f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterReadIsolation)
			if f.OK {
				t.Fatalf("an identity that may %s secrets in every namespace passed read isolation: %s", verb, f.Found)
			}
			if !strings.Contains(f.Found, "EVERY namespace") {
				t.Errorf("the finding does not say the read reaches every namespace: %s", f.Found)
			}
			if !strings.Contains(f.Found, verb) {
				t.Errorf("the finding does not name the verb it found: %s", f.Found)
			}
		})
	}
}

// TestClusterContractReadIsolationRefusesAReadableForeignNamespace is the
// case the cluster-wide review cannot see: a RoleBinding in someone else's
// records namespace, which grants this identity Secrets there without
// granting anything cluster-wide.
func TestClusterContractReadIsolationRefusesAReadableForeignNamespace(t *testing.T) {
	cs := withReviews(fake.NewClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: contractNamespace}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tofu-records-bob"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}},
	), func(ns, verb string) bool {
		return ns == contractNamespace || ns == "tofu-records-bob"
	})
	f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterReadIsolation)
	if f.OK {
		t.Fatalf("an identity that may read Bob's records passed read isolation: %s", f.Found)
	}
	if !strings.Contains(f.Found, "tofu-records-bob") {
		t.Errorf("the finding does not name the namespace it can read: %s", f.Found)
	}
}

// TestClusterContractReadIsolationPassesAScopedIdentity is the arrangement
// the docs recommend, and it has to pass or the check refuses the correct
// setup. The foreign namespace is there and is NOT readable.
func TestClusterContractReadIsolationPassesAScopedIdentity(t *testing.T) {
	cs := withReviews(fake.NewClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: contractNamespace}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tofu-records-bob"}},
	), func(ns, verb string) bool { return ns == contractNamespace })
	f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterReadIsolation)
	if !f.OK {
		t.Fatalf("an identity scoped to its own records namespace failed read isolation: %s", f.Found)
	}
	if !strings.Contains(f.Found, "all 1 other") {
		t.Errorf("the finding does not say how many other records namespaces it checked: %s", f.Found)
	}
}

// TestClusterContractReadIsolationSaysWhatItCouldNotEnumerate is the honesty
// half. The recommended Role cannot list namespaces, so the second question
// goes unanswered, and the finding says which one rather than implying both
// were asked.
func TestClusterContractReadIsolationSaysWhatItCouldNotEnumerate(t *testing.T) {
	cs := forbid(withReviews(fake.NewClientset(), func(ns, verb string) bool {
		return ns == contractNamespace
	}), "list", "namespaces")
	f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterReadIsolation)
	if !f.OK {
		t.Fatalf("a scoped identity that may not list namespaces failed read isolation: %s", f.Found)
	}
	if !strings.Contains(f.Found, "could not be enumerated") {
		t.Errorf("the finding does not say the second question went unanswered: %s", f.Found)
	}
}

// apiServerPod is a kube-apiserver static Pod, with or without the flag.
func apiServerPod(name string, argv ...string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "kube-system",
			Labels:    map[string]string{"component": "kube-apiserver", "tier": "control-plane"},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name:    "kube-apiserver",
			Command: append([]string{"kube-apiserver"}, argv...),
		}}},
	}
}

// TestClusterContractEncryptionAtRest is assertion 3, all three of its
// answers. The third is the one the issue insisted on: a cluster that cannot
// say is reported as not checked and is never a pass.
func TestClusterContractEncryptionAtRest(t *testing.T) {
	allowAll := func(ns, verb string) bool { return true }

	t.Run("the flag is set", func(t *testing.T) {
		cs := withReviews(fake.NewClientset(apiServerPod("kube-apiserver-cp",
			"--encryption-provider-config=/etc/kubernetes/enc/enc.yaml")), allowAll)
		f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterEncryptionAtRest)
		if !f.OK {
			t.Fatalf("an API server started with the flag failed: %s", f.Found)
		}
		if !strings.Contains(f.Found, "/etc/kubernetes/enc/enc.yaml") {
			t.Errorf("the finding does not quote the configuration it read: %s", f.Found)
		}
	})

	t.Run("the flag is set as two arguments", func(t *testing.T) {
		cs := withReviews(fake.NewClientset(apiServerPod("kube-apiserver-cp",
			"--encryption-provider-config", "/etc/kubernetes/enc/enc.yaml")), allowAll)
		f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterEncryptionAtRest)
		if !f.OK {
			t.Fatalf("the two-argument spelling of the flag was not read: %s", f.Found)
		}
	})

	t.Run("the flag is absent", func(t *testing.T) {
		cs := withReviews(fake.NewClientset(apiServerPod("kube-apiserver-cp", "--advertise-address=10.0.0.1")), allowAll)
		f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterEncryptionAtRest)
		if f.OK || f.NotChecked {
			t.Fatalf("an API server with no encryption configuration was not a failure: OK=%v NotChecked=%v %s", f.OK, f.NotChecked, f.Found)
		}
		if !strings.Contains(f.Found, "not encrypted") {
			t.Errorf("the finding does not say the records are unencrypted: %s", f.Found)
		}
	})

	t.Run("no API server Pod is visible", func(t *testing.T) {
		cs := withReviews(fake.NewClientset(), allowAll)
		f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterEncryptionAtRest)
		if !f.NotChecked || f.OK {
			t.Fatalf("a managed control plane was not reported as not checked: OK=%v NotChecked=%v", f.OK, f.NotChecked)
		}
		if !strings.Contains(f.Found, "not readable from here, not checked") {
			t.Errorf("the finding does not use the words the report prints: %s", f.Found)
		}
	})

	t.Run("listing the Pod is forbidden", func(t *testing.T) {
		cs := forbid(withReviews(fake.NewClientset(), allowAll), "list", "pods")
		f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterEncryptionAtRest)
		if !f.NotChecked || f.OK {
			t.Fatalf("a denied read was treated as something other than not checked: OK=%v NotChecked=%v", f.OK, f.NotChecked)
		}
	})
}

// boundaryPolicy is estate-boundary.yaml's policy as the API server serves it
// once observed.
func boundaryPolicy() *admissionv1.ValidatingAdmissionPolicy {
	return &admissionv1.ValidatingAdmissionPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: EstateBoundaryPolicyName, Generation: 1},
		Status:     admissionv1.ValidatingAdmissionPolicyStatus{ObservedGeneration: 1},
	}
}

func boundaryBinding(actions ...admissionv1.ValidationAction) *admissionv1.ValidatingAdmissionPolicyBinding {
	return &admissionv1.ValidatingAdmissionPolicyBinding{
		ObjectMeta: metav1.ObjectMeta{Name: EstateBoundaryPolicyName},
		Spec: admissionv1.ValidatingAdmissionPolicyBindingSpec{
			PolicyName:        EstateBoundaryPolicyName,
			ValidationActions: actions,
		},
	}
}

// TestClusterContractEstateBoundary is assertion 4. Installed and in force
// are different things, and every way of being installed without being in
// force has its own case here, because any of them would otherwise read as a
// fence that refuses nothing.
func TestClusterContractEstateBoundary(t *testing.T) {
	allowAll := func(ns, verb string) bool { return true }

	t.Run("installed and denying", func(t *testing.T) {
		cs := withReviews(fake.NewClientset(boundaryPolicy(), boundaryBinding(admissionv1.Deny)), allowAll)
		f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterEstateBoundary)
		if !f.OK {
			t.Fatalf("an installed, observed, denying policy failed: %s", f.Found)
		}
	})

	t.Run("not installed", func(t *testing.T) {
		cs := withReviews(fake.NewClientset(), allowAll)
		f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterEstateBoundary)
		if f.OK || f.NotChecked {
			t.Fatalf("an absent policy was not a failure: OK=%v NotChecked=%v", f.OK, f.NotChecked)
		}
		if !strings.Contains(f.Found, "estate-boundary.yaml") {
			t.Errorf("the finding does not say what to install: %s", f.Found)
		}
	})

	t.Run("no binding", func(t *testing.T) {
		cs := withReviews(fake.NewClientset(boundaryPolicy()), allowAll)
		f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterEstateBoundary)
		if f.OK {
			t.Fatal("a policy with no binding passed; it evaluates nothing")
		}
		if !strings.Contains(f.Found, "inert") {
			t.Errorf("the finding does not say an unbound policy is inert: %s", f.Found)
		}
	})

	t.Run("the binding warns instead of denying", func(t *testing.T) {
		cs := withReviews(fake.NewClientset(boundaryPolicy(), boundaryBinding(admissionv1.Warn, admissionv1.Audit)), allowAll)
		f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterEstateBoundary)
		if f.OK {
			t.Fatal("a binding whose validationActions are Warn and Audit passed; a cross-estate write is logged and then allowed")
		}
		if !strings.Contains(f.Found, "Deny") {
			t.Errorf("the finding does not name the action it wanted: %s", f.Found)
		}
	})

	t.Run("installed but not yet observed", func(t *testing.T) {
		policy := boundaryPolicy()
		policy.Generation = 2
		policy.Status.ObservedGeneration = 1
		cs := withReviews(fake.NewClientset(policy, boundaryBinding(admissionv1.Deny)), allowAll)
		f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterEstateBoundary)
		if f.OK {
			t.Fatal("a policy the API server has not observed passed; it is installed and not yet in force")
		}
		if !strings.Contains(f.Found, "not yet in force") {
			t.Errorf("the finding does not distinguish installed from in force: %s", f.Found)
		}
	})

	t.Run("the CEL has type-check warnings", func(t *testing.T) {
		policy := boundaryPolicy()
		policy.Status.TypeChecking = &admissionv1.TypeChecking{
			ExpressionWarnings: []admissionv1.ExpressionWarning{{FieldRef: "spec.validations[0]", Warning: "no such key: labels"}},
		}
		cs := withReviews(fake.NewClientset(policy, boundaryBinding(admissionv1.Deny)), allowAll)
		f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterEstateBoundary)
		if f.OK {
			t.Fatal("a policy whose CEL the server warned about passed")
		}
		if !strings.Contains(f.Found, "no such key: labels") {
			t.Errorf("the finding does not quote the warning: %s", f.Found)
		}
	})

	t.Run("reading the policy is forbidden", func(t *testing.T) {
		cs := forbid(withReviews(fake.NewClientset(), allowAll), "get", "validatingadmissionpolicies")
		f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterEstateBoundary)
		if !f.NotChecked || f.OK {
			t.Fatalf("a denied read was not reported as not checked: OK=%v NotChecked=%v", f.OK, f.NotChecked)
		}
		if !strings.Contains(f.Found, "not readable from here, not checked") {
			t.Errorf("the finding does not use the words the report prints: %s", f.Found)
		}
	})

	t.Run("reading the binding is forbidden", func(t *testing.T) {
		cs := forbid(withReviews(fake.NewClientset(boundaryPolicy()), allowAll), "get", "validatingadmissionpolicybindings")
		f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterEstateBoundary)
		if !f.NotChecked || f.OK {
			t.Fatalf("a denied read of the binding was not reported as not checked: OK=%v NotChecked=%v", f.OK, f.NotChecked)
		}
	})
}

// TestSplitWaivedCluster is #1340's rule on this contract: a waiver reaches
// exactly the settings it names, and a question that could not be answered is
// waived by the same name as one that failed - from the caller's side they
// are one refusal.
func TestSplitWaivedCluster(t *testing.T) {
	findings := []ClusterFinding{
		{Setting: ClusterNamespaceAccess, OK: true},
		{Setting: ClusterReadIsolation, Warning: true},
		{Setting: ClusterEncryptionAtRest, NotChecked: true},
		{Setting: ClusterEstateBoundary},
	}

	refused, warned, waived := SplitWaivedCluster(findings, []string{"encryption_at_rest"})
	if len(waived) != 1 || waived[0].Setting != ClusterEncryptionAtRest {
		t.Fatalf("waived = %v, want the one setting named", waived)
	}
	if len(warned) != 1 || warned[0].Setting != ClusterReadIsolation {
		t.Fatalf("warned = %v, want the one warning finding", warned)
	}
	if len(refused) != 1 || refused[0].Setting != ClusterEstateBoundary {
		t.Fatalf("refused = %v, want only the failing setting the waiver does not name", refused)
	}
	for _, f := range refused {
		if f.OK || f.Warning || f.NotChecked {
			t.Errorf("%q is not a refusal and reached the refused list", f.Setting)
		}
	}

	// A waiver silences a warning as well as a refusal.
	refused, warned, waived = SplitWaivedCluster(findings, []string{"read_isolation"})
	if len(warned) != 1 || len(waived) != 1 || len(refused) != 1 {
		t.Fatalf("waiving the warning: refused=%d warned=%d waived=%d, want 1, 1 and 1", len(refused), len(warned), len(waived))
	}

	refused, warned, waived = SplitWaivedCluster(findings, nil)
	if len(refused) != 1 || len(warned) != 2 || len(waived) != 0 {
		t.Fatalf("with no waiver: refused=%d warned=%d waived=%d, want 1, 2 and 0", len(refused), len(warned), len(waived))
	}
}

// TestAScopedIdentityIsWarnedAndNotRefused is the whole reason a run treats
// NotChecked differently from a failure. The Role the docs recommend holds
// Secrets in one namespace and nothing else, so it cannot list kube-system's
// Pods and cannot get a ValidatingAdmissionPolicy. If those two refused, the
// intended arrangement would carry two waivers from its first day - a gate
// satisfied by a line in the configuration, which protects nothing.
func TestAScopedIdentityIsWarnedAndNotRefused(t *testing.T) {
	cs := forbid(forbid(forbid(withReviews(fake.NewClientset(), func(ns, verb string) bool {
		return ns == contractNamespace
	}), "list", "pods"), "get", "validatingadmissionpolicies"), "list", "namespaces")

	findings := check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true})
	refused, warned, _ := SplitWaivedCluster(findings, nil)
	if len(refused) != 0 {
		t.Fatalf("the recommended arrangement was refused: %v", refused)
	}
	if len(warned) != 2 {
		t.Fatalf("warned about %d properties, want the two it cannot read: %v", len(warned), warned)
	}
	for _, f := range warned {
		if !f.NotChecked {
			t.Errorf("%q warned for some reason other than not being readable: %s", f.Setting, f.Found)
		}
		_, detail := ClusterContractRefusal(contractNamespace, f)
		if !strings.Contains(detail, "this is not a pass") {
			t.Errorf("%q's warning lets the reader take it for a pass: %q", f.Setting, detail)
		}
		if !strings.Contains(detail, "live-cluster") {
			t.Errorf("%q's warning does not say who can answer the question: %q", f.Setting, detail)
		}
	}

	// The same two properties, READ and wrong, still refuse: a fact somebody
	// can act on is not a warning.
	wrong := withReviews(fake.NewClientset(apiServerPod("kube-apiserver-cp", "--advertise-address=10.0.0.1")),
		func(ns, verb string) bool { return ns == contractNamespace })
	refused, _, _ = SplitWaivedCluster(check(t, wrong, ClusterContractOptions{NamespaceKnownToExist: true}), nil)
	if len(refused) != 2 {
		t.Fatalf("a cluster with no encryption and no boundary policy raised %d refusals, want 2: %v", len(refused), refused)
	}
}

// TestClusterContractReadIsolationWarnsWithoutRefusingTheFirstEstate is the
// split #1393 asks for: a warning as the floor for a cluster-wide reader, and
// a refusal where another estate's records namespace is known.
//
// The refusing half has its own test above. This is the half that must NOT
// refuse: a cluster-admin standing up the first estate on a fresh cluster can
// read every namespace and has exposed nothing, because there is nothing else
// there to read.
func TestClusterContractReadIsolationWarnsWithoutRefusingTheFirstEstate(t *testing.T) {
	cs := withReviews(fake.NewClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: contractNamespace}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}},
	), func(ns, verb string) bool { return true })
	f := findingFor(t, check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterReadIsolation)

	if f.OK {
		t.Fatal("a cluster-wide reader passed read isolation outright; the capability is real and has to be said")
	}
	if !f.Warning {
		t.Fatalf("the first estate on a cluster was REFUSED for a capability that has exposed nothing: %s", f.Found)
	}
	if !strings.Contains(f.Found, "nothing is exposed yet") {
		t.Errorf("the warning does not say why it is not a refusal: %s", f.Found)
	}
	summary, detail := ClusterContractRefusal(contractNamespace, f)
	if !strings.Contains(summary, "weaker than it should be") {
		t.Errorf("a warning is headlined as a failure: %q", summary)
	}
	if !strings.Contains(detail, "The run goes on") {
		t.Errorf("the warning does not say the run goes on: %q", detail)
	}

	// The same identity, once a second estate's records namespace exists:
	// now it is a breach and refuses.
	cs2 := withReviews(fake.NewClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: contractNamespace}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tofu-records-bob"}},
	), func(ns, verb string) bool { return true })
	f2 := findingFor(t, check(t, cs2, ClusterContractOptions{NamespaceKnownToExist: true}), ClusterReadIsolation)
	if f2.OK || f2.Warning {
		t.Fatalf("an identity that can read an existing second estate's records only warned: %s", f2.Found)
	}
	if !strings.Contains(f2.Found, "tofu-records-bob") {
		t.Errorf("the refusal does not name the namespace it can read: %s", f2.Found)
	}
}

// TestClusterWaiverCostNamesTheCost is #1340's other half: the cost is in the
// same sentence as the name, never a generic "running with reduced checks".
func TestClusterWaiverCostNamesTheCost(t *testing.T) {
	for _, setting := range ClusterSettings {
		cost := ClusterWaiverCost(setting)
		if cost == "" || cost == ClusterWaiverCost("unknown") {
			t.Errorf("%q has no cost of its own: %q", setting, cost)
		}
	}
}

// TestClusterContractRefusalSaysWhatToDo holds every refusal to the shape
// internal/command prints: a headline, what the assertion protects against,
// and what to do instead. A refusal that only says "failed" makes the reader
// go and find out what it was for.
func TestClusterContractRefusalSaysWhatToDo(t *testing.T) {
	for _, setting := range ClusterSettings {
		for _, shape := range []struct {
			name       string
			notChecked bool
			warning    bool
		}{{"failed", false, false}, {"not checked", true, false}, {"warning", false, true}} {
			f := ClusterFinding{Setting: setting, NotChecked: shape.notChecked, Warning: shape.warning, Found: "something"}
			summary, detail := ClusterContractRefusal(contractNamespace, f)
			if summary == "" || detail == "" {
				t.Errorf("%q (%s) has no text", setting, shape.name)
				continue
			}
			if len(detail) < 200 {
				t.Errorf("%q (%s) says too little: %q", setting, shape.name, detail)
			}
			// Every refusal and every "could not be checked" ends with the
			// exact line to write. A warning does not: nothing is being
			// refused, so there is nothing to get past.
			if !shape.warning {
				want := `allow_insecure = ["` + string(setting) + `"]`
				if !strings.Contains(detail, want) {
					t.Errorf("%q (%s) does not carry the line a reader would write, %s: %q", setting, shape.name, want, detail)
				}
			}
			if shape.warning && !strings.Contains(detail, "The run goes on") {
				t.Errorf("%q is a warning and the text does not say the run goes on: %q", setting, detail)
			}
		}
	}
	if s, _ := ClusterContractRefusal(contractNamespace, ClusterFinding{Setting: ClusterReadIsolation, OK: true}); s != "" {
		t.Errorf("a passing finding produced a refusal: %q", s)
	}
}

// TestKubernetesStoreCheckClusterContractUsesItsOwnNamespace pins the one
// thing a caller must not be able to change: a report about some other
// namespace than the one the records are in is a report about nothing.
func TestKubernetesStoreCheckClusterContractUsesItsOwnNamespace(t *testing.T) {
	cs := withReviews(fake.NewClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: fakeRecordNamespace}}),
		func(ns, verb string) bool { return ns == fakeRecordNamespace })
	store, err := NewKubernetesStore(KubernetesConfig{
		Secrets:   cs.CoreV1().Secrets(fakeRecordNamespace),
		Clientset: cs,
		Namespace: fakeRecordNamespace,
		Estate:    "alice",
	})
	if err != nil {
		t.Fatalf("NewKubernetesStore: %v", err)
	}
	findings, err := store.CheckClusterContract(context.Background(), ClusterContractOptions{Namespace: "somewhere-else"})
	if err != nil {
		t.Fatalf("CheckClusterContract: %v", err)
	}
	f := findingFor(t, findings, ClusterNamespaceAccess)
	if !f.OK {
		t.Fatalf("the store checked a namespace other than its own: %s", f.Found)
	}

	checker, ok := AsClusterContractChecker(NewRunCache(store, ""))
	if !ok {
		t.Fatal("AsClusterContractChecker does not see through the run cache")
	}
	if checker == nil {
		t.Fatal("AsClusterContractChecker returned a nil checker")
	}
	local, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %v", err)
	}
	if _, ok := AsClusterContractChecker(local); ok {
		t.Error("the local store claims a cluster contract; a store with nothing to assert is not a store that failed")
	}
}

// TestKubernetesStoreCheckClusterContractWithNoClientset is the internal
// inconsistency case: a store built from a bare SecretInterface, as the
// conformance suite builds one, has nothing to ask the authorizer with.
func TestKubernetesStoreCheckClusterContractWithNoClientset(t *testing.T) {
	store := newFakeKubernetesStore(t, "alice")
	if _, err := store.CheckClusterContract(context.Background(), ClusterContractOptions{}); err == nil {
		t.Fatal("a store with no clientset reported on the cluster anyway")
	} else if !strings.Contains(err.Error(), "no clientset") {
		t.Errorf("unhelpful error: %v", err)
	}
}

// TestTheTwoRefusalsAPlainKindClusterGives is the first-time reader's case,
// pinned because it is the one a person actually meets. Someone following the
// Kubernetes documentation writes `record_store "kubernetes" {}`, applies as
// cluster-admin on kind, and is refused twice at once: for an API server flag
// kind does not set, and for a policy nobody has told them to install yet.
//
// Each refusal has to carry both ways out - what to change, and the exact
// allow_insecure line - or the reader is left with a message they cannot act
// on.
func TestTheTwoRefusalsAPlainKindClusterGives(t *testing.T) {
	cs := withReviews(fake.NewClientset(
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: contractNamespace}},
		apiServerPod("kube-apiserver-kind-control-plane", "--advertise-address=172.18.0.2"),
	), func(ns, verb string) bool { return true })

	findings := check(t, cs, ClusterContractOptions{NamespaceKnownToExist: true})
	refused, _, _ := SplitWaivedCluster(findings, nil)
	if len(refused) != 2 {
		t.Fatalf("a plain kind cluster raised %d refusals, want encryption_at_rest and estate_boundary: %v", len(refused), refused)
	}

	for _, f := range refused {
		_, detail := ClusterContractRefusal(contractNamespace, f)
		var fixWords []string
		switch f.Setting {
		case ClusterEncryptionAtRest:
			fixWords = []string{"Fix it by starting the API server with --encryption-provider-config", "kind and minikube"}
		case ClusterEstateBoundary:
			fixWords = []string{"kubectl apply -f live/kubernetes/estate-boundary.yaml", "estate-grant.yaml"}
		default:
			t.Fatalf("unexpected refusal on a plain kind cluster: %q", f.Setting)
		}
		for _, want := range fixWords {
			if !strings.Contains(detail, want) {
				t.Errorf("%q does not tell the reader how to fix it (%q missing): %q", f.Setting, want, detail)
			}
		}
		if !strings.Contains(detail, `allow_insecure = ["`+string(f.Setting)+`"]`) {
			t.Errorf("%q does not carry the exact waiver line: %q", f.Setting, detail)
		}
	}
}

// TestClusterWaiverLineIsOneLineForSeveralSettings: a reader who follows two
// refusals' own waiver lines would write allow_insecure twice in one block,
// which is a duplicate argument and does not parse. The combined line is what
// internal/live/projection adds when more than one refuses at once.
func TestClusterWaiverLineIsOneLineForSeveralSettings(t *testing.T) {
	got := ClusterWaiverArgument(ClusterEncryptionAtRest, ClusterEstateBoundary)
	if got != `allow_insecure = ["encryption_at_rest", "estate_boundary"]` {
		t.Errorf("the combined waiver argument is %q, want one allow_insecure with both names", got)
	}
	if one := ClusterWaiverArgument(ClusterEstateBoundary); one != `allow_insecure = ["estate_boundary"]` {
		t.Errorf("the single-setting argument is %q", one)
	}
	if !strings.Contains(ClusterWaiverLine(ClusterEstateBoundary), `allow_insecure = ["estate_boundary"]`) {
		t.Error("the waiver sentence does not carry the argument")
	}
}
