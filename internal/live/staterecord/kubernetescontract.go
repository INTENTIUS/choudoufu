// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"fmt"
	"sort"
	"strings"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	authzv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// The cluster contract. GitHub issue #1393, the Kubernetes half of what
// bucketcontract.go does for a bucket (#1339).
//
// A record Secret can be the only copy of what it says, exactly as a bucket
// object can, so the same question is asked of the cluster: are the things
// the records depend on actually true here. Four of them are, and each one is
// asked in the way that can answer honestly:
//
//   - namespace_access: the records namespace is there and this identity may
//     do to Secrets in it what the store will ask. Asked with
//     SelfSubjectAccessReview, one per verb, never by attempting a write. A
//     write that probes permission has to write something, and the thing it
//     would write is a record.
//   - read_isolation: the namespace IS the read boundary (#1392, decision 2),
//     so an identity that can read Secrets outside it has no boundary. RBAC
//     cannot condition on a label and admission never sees a get, so there is
//     nothing else that could fence a read.
//   - encryption_at_rest: whether the API server was started with an
//     EncryptionConfiguration. Records hold secret material and a Secret is
//     base64, not encryption.
//   - estate_boundary: whether live/kubernetes/estate-boundary.yaml's policy
//     AND its binding are installed and in force, since that is what fences
//     writes to the record Secrets (#1392, decision 3).
//
// # Not readable is not a pass
//
// Two of these four cannot be answered at all from some clusters. The API
// server's --encryption-provider-config is a file on the control plane's
// disk, not an API object: it is readable on kind and on any kubeadm cluster
// whose control-plane Pods this identity can see, and on EKS, GKE and AKS it
// is not readable from inside the cluster at any permission level. Reading
// admissionregistration objects needs cluster-scoped get, which the Role the
// docs recommend for a records namespace does not carry.
//
// A finding that could not be answered carries NotChecked, is never OK, and
// says "not readable from here". It is not a pass and no heuristic is
// substituted for it. The waiver list (see [ClusterWaiverCost]) is how an
// operator acknowledges one, in the configuration, out loud on every run.
//
// # This file reports the cluster, not the run
//
// [CheckClusterContract] knows nothing about waivers, for the reason
// bucketcontract.go gives: `choudoufu live-cluster` asks "is this cluster
// correct", and a report that honoured a waiver would print green for a
// cluster that fails. Whether a run may PROCEED past a finding is the
// caller's decision (#1340).

// The cluster's properties are named in the shared vocabulary [Setting], and
// a finding about one is a [Finding]; see contract.go for what every store's
// contract has in common and what the outcomes mean.
const (
	ClusterNamespaceAccess  Setting = "namespace_access"
	ClusterReadIsolation    Setting = "read_isolation"
	ClusterEncryptionAtRest Setting = "encryption_at_rest"
	ClusterEstateBoundary   Setting = "estate_boundary"
)

// ClusterSettings is every asserted property, in the order findings are
// reported.
var ClusterSettings = []Setting{
	ClusterNamespaceAccess,
	ClusterReadIsolation,
	ClusterEncryptionAtRest,
	ClusterEstateBoundary,
}

// KubernetesRecordVerbs is every verb [KubernetesStore] uses on a Secret, in
// the order a review reports them. A run that applies needs all five.
var KubernetesRecordVerbs = []string{"get", "list", "create", "update", "delete"}

// KubernetesPlanVerbs is what a run that only PLANS needs, measured rather
// than assumed (GitHub issue #1393, under #1370). A plan reads records and
// writes none; the one write on its path is the provisioning sentinel, which
// internal/live/projection carries past a denial when an earlier writing run
// already left the sentinel behind.
//
// So a plan-only identity is reviewed for these two and not refused for
// lacking the other three. What it costs is stated where it is chosen, in
// internal/live/projection: an estate whose sentinel has never been written
// cannot be planned by an identity that may not write one.
var KubernetesPlanVerbs = []string{"get", "list"}

// EstateBoundaryPolicyName is the name live/kubernetes/estate-boundary.yaml
// gives both its ValidatingAdmissionPolicy and its binding.
const EstateBoundaryPolicyName = "choudoufu-estate-boundary"

// KubernetesRecordNamespacePrefix starts the default records namespace of
// every estate ("tofu-records-" and the estate name). The read-isolation
// check uses it to recognise ANOTHER estate's records namespace when it can
// see one. internal/live/projection owns the default itself; this is the
// string, spelled here so the check does not import it.
const KubernetesRecordNamespacePrefix = "tofu-records-"

// maxForeignNamespaceReviews bounds how many other estates' records
// namespaces one read-isolation check reviews. A cluster with a thousand
// estates would otherwise make a thousand SelfSubjectAccessReviews on every
// first contact. A truncated review says so in its Found text, so the number
// is never silently the answer.
const maxForeignNamespaceReviews = 25

// VerbAccess is one verb's answer from a SelfSubjectAccessReview.
type VerbAccess struct {
	Verb string `json:"verb"`
	// Allowed is the authorizer's own answer.
	Allowed bool `json:"allowed"`
	// Required is whether this run needs the verb. A plan-only identity
	// requires get and list and not the other three (see
	// [KubernetesPlanVerbs]), so a denied verb that is not required is
	// reported and does not fail the finding.
	Required bool `json:"required"`
	// Reason is what the authorizer said, when it said anything.
	Reason string `json:"reason,omitempty"`
}

// CheckContract implements [ContractChecker]. The namespace and the estate
// are the store's own, never the caller's: a report about some other
// namespace than the one the records are in would be a report about nothing.
// NamespaceKnownToExist is set, because a store that got this far has already
// written and listed through that namespace.
func (s *KubernetesStore) CheckContract(ctx context.Context, opts ContractOptions) ([]Finding, error) {
	if s.clientset == nil {
		return nil, fmt.Errorf("staterecord: kubernetes: this store was built with no clientset, so the cluster contract cannot be checked; internal/live/projection always passes one (see KubernetesConfig.Clientset)")
	}
	return s.CheckClusterContract(ctx, ClusterContractOptions{RequiredVerbs: opts.RequiredVerbs})
}

// CheckClusterContract is [KubernetesStore.CheckContract] in this store's own
// vocabulary, for a caller that has one of these in hand and wants to name
// the options itself. The namespace, the estate and NamespaceKnownToExist are
// always the store's own whatever opts says.
func (s *KubernetesStore) CheckClusterContract(ctx context.Context, opts ClusterContractOptions) ([]Finding, error) {
	if s.clientset == nil {
		return nil, fmt.Errorf("staterecord: kubernetes: this store was built with no clientset, so the cluster contract cannot be checked; internal/live/projection always passes one (see KubernetesConfig.Clientset)")
	}
	opts.Namespace = s.namespace
	opts.Estate = s.estate
	opts.NamespaceKnownToExist = true
	return CheckClusterContract(ctx, s.clientset, opts)
}

// ContractSubject implements [ContractChecker]: a cluster store is named by
// the namespace its records live in, which is also its read boundary.
func (s *KubernetesStore) ContractSubject() (label, value string) { return "Namespace", s.namespace }

// ContractRefusal implements [ContractChecker] with this cluster's own words.
func (s *KubernetesStore) ContractRefusal(f Finding) (summary, detail string) {
	return ClusterContractRefusal(s.namespace, f)
}

// ContractCheckFailed implements [ContractChecker].
func (s *KubernetesStore) ContractCheckFailed(err error) (summary, detail string) {
	return ClusterContractCheckFailed(err)
}

// ContractRefusalClosing implements [ContractChecker] with this cluster's
// own words.
func (s *KubernetesStore) ContractRefusalClosing(refused []Setting) string {
	return ClusterContractRefusalClosing(refused)
}

// ClusterContractOptions is what a check needs to know beyond the client.
type ClusterContractOptions struct {
	// Namespace is the records namespace being asserted about. Required.
	Namespace string

	// Estate is the estate whose records live there, for the report's text.
	// May be "".
	Estate string

	// RequiredVerbs is what this run needs on Secrets in Namespace. Nil
	// takes [KubernetesRecordVerbs]; a plan-only identity passes
	// [KubernetesPlanVerbs]. Every verb in [KubernetesRecordVerbs] is
	// reviewed and reported whatever this says - what it changes is which
	// denials fail the finding.
	RequiredVerbs []string

	// NamespaceKnownToExist is set by a caller that has already USED the
	// namespace successfully, which is every caller that reaches this
	// through an open store: the provisioning sentinel's write and List went
	// through it. Such a caller does not re-probe, because the probe needs
	// cluster-scoped get on namespaces that the recommended Role has no
	// reason to hold, and because a namespace the store has just written to
	// being reported absent would be two answers to one question.
	//
	// False, the check probes, and an absent namespace is reported with
	// *[NamespaceMissingError]'s own words, so the contract and the store's
	// refusal say the same thing.
	NamespaceKnownToExist bool
}

// CheckClusterContract reads the four properties of the cluster cs reaches
// and reports one finding per setting, always all four and always in
// [ClusterSettings] order: a caller that refused on the first bad one would
// make an operator fix them one run at a time.
//
// The error return is for a failure that is not about the cluster's
// properties at all - a cancelled context, an unreachable API server, a
// SelfSubjectAccessReview the server would not accept. A DENIED read is not
// an error: it is a finding with NotChecked set.
func CheckClusterContract(ctx context.Context, cs kubernetes.Interface, opts ClusterContractOptions) ([]Finding, error) {
	if cs == nil {
		return nil, fmt.Errorf("staterecord: kubernetes: the cluster contract needs a clientset and was given none")
	}
	if opts.Namespace == "" {
		return nil, fmt.Errorf("staterecord: kubernetes: the cluster contract needs the records namespace and was given none")
	}

	findings := make([]Finding, 0, len(ClusterSettings))

	access, err := checkNamespaceAccess(ctx, cs, opts)
	if err != nil {
		return nil, err
	}
	findings = append(findings, access)

	isolation, err := checkReadIsolation(ctx, cs, opts)
	if err != nil {
		return nil, err
	}
	findings = append(findings, isolation)

	findings = append(findings, checkEncryptionAtRest(ctx, cs))
	findings = append(findings, checkEstateBoundary(ctx, cs))

	return findings, nil
}

// reviewSecrets asks the API server's own authorizer whether this identity
// may do verb to Secrets in namespace. An empty namespace asks about EVERY
// namespace, which is what a SubjectAccessReview means by one.
//
// SelfSubjectAccessReview is the one permission question that needs no
// permission: the default system:basic-user ClusterRole grants create on it
// to system:authenticated, so this works for the scoped plan identity the
// docs recommend, which can read nothing but its own namespace's Secrets.
func reviewSecrets(ctx context.Context, cs kubernetes.Interface, namespace, verb string) (allowed bool, reason string, err error) {
	review := &authzv1.SelfSubjectAccessReview{
		Spec: authzv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authzv1.ResourceAttributes{
				Namespace: namespace,
				Verb:      verb,
				Group:     "",
				Version:   "v1",
				Resource:  "secrets",
			},
		},
	}
	out, err := cs.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return false, "", fmt.Errorf("staterecord: kubernetes: asking the API server whether this identity may %s secrets in namespace %q: %w", verb, namespace, err)
	}
	reason = strings.TrimSpace(out.Status.Reason)
	if out.Status.EvaluationError != "" {
		if reason != "" {
			reason += "; "
		}
		reason += "evaluation error: " + out.Status.EvaluationError
	}
	return out.Status.Allowed && !out.Status.Denied, reason, nil
}

// checkNamespaceAccess is assertion 1: the records namespace is there, and
// this identity may do to Secrets in it what this run will ask.
//
// Every one of [KubernetesRecordVerbs] is reviewed and reported. Which of
// them have to be ALLOWED is opts.RequiredVerbs, so a plan under a get/list
// Role is not refused for lacking create, update and delete - it is told, in
// the same line, that it has them and does not need them.
func checkNamespaceAccess(ctx context.Context, cs kubernetes.Interface, opts ClusterContractOptions) (Finding, error) {
	f := Finding{Setting: ClusterNamespaceAccess}

	required := opts.RequiredVerbs
	if len(required) == 0 {
		required = KubernetesRecordVerbs
	}
	isRequired := func(verb string) bool {
		for _, r := range required {
			if r == verb {
				return true
			}
		}
		return false
	}

	var missing []string
	for _, verb := range KubernetesRecordVerbs {
		allowed, reason, err := reviewSecrets(ctx, cs, opts.Namespace, verb)
		if err != nil {
			return f, err
		}
		f.Verbs = append(f.Verbs, VerbAccess{Verb: verb, Allowed: allowed, Required: isRequired(verb), Reason: reason})
		if !allowed && isRequired(verb) {
			missing = append(missing, verb)
		}
	}

	// What the verbs came to, said the same way whatever the namespace probe
	// below turns out to be: a finding that dropped the verb summary because
	// the namespace could not be read would leave an operator with a
	// refusal that does not say which verb is missing. That is what the
	// first version of this function did.
	verbs := verbSummary(f.Verbs, required)
	if len(missing) > 0 {
		verbs = fmt.Sprintf("this identity may not %s secrets in namespace %q (%s)",
			strings.Join(missing, ", "), opts.Namespace, verbs)
	}

	// The namespace's existence. A caller that has already used the
	// namespace says so and nothing is re-probed; see
	// [ClusterContractOptions.NamespaceKnownToExist].
	if !opts.NamespaceKnownToExist {
		_, err := cs.CoreV1().Namespaces().Get(ctx, opts.Namespace, metav1.GetOptions{})
		switch {
		case err == nil:
		case k8serrors.IsNotFound(err):
			// The store's own words, so the contract check and the refusal
			// the store raises on use cannot drift apart.
			f.Found = (&NamespaceMissingError{Namespace: opts.Namespace, Err: err}).Error()
			return f, nil
		case k8serrors.IsForbidden(err):
			// The recommended Role has no reason to hold cluster-scoped get
			// on namespaces. Whether the namespace is there is then settled
			// by the store's first use of it, which is where the refusal
			// lives, so this is reported and not asserted.
			f.Found = verbs + fmt.Sprintf("; this identity may not get namespace %q, so whether it exists was not established here, and the store refuses an absent one by name on its first write", opts.Namespace)
			if len(missing) == 0 {
				f.Outcome = Passed
			}
			return f, nil
		default:
			return f, fmt.Errorf("staterecord: kubernetes: reading namespace %q: %w", opts.Namespace, err)
		}
	}

	f.Found = verbs
	if len(missing) == 0 {
		f.Outcome = Passed
	}
	return f, nil
}

// verbSummary is the one clause a namespace_access finding reports: what the
// authorizer said about each verb, and which of them this run needs.
func verbSummary(verbs []VerbAccess, required []string) string {
	var allowed, denied []string
	for _, v := range verbs {
		if v.Allowed {
			allowed = append(allowed, v.Verb)
		} else {
			denied = append(denied, v.Verb)
		}
	}
	s := "allowed: " + joinOrNone(allowed) + "; denied: " + joinOrNone(denied)
	return s + "; this run needs " + joinOrNone(required)
}

func joinOrNone(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}

// checkReadIsolation is assertion 2. The namespace is the read boundary, so
// the question is whether this identity can read Secrets outside it.
//
// # What refuses and what only warns
//
// The refusal is a records namespace that EXISTS, belongs to another estate,
// and is readable by this identity. That is the boundary being absent, and
// what makes it findable without cluster-wide permission is that the other
// estate's namespace is asked about by name.
//
// Being able to get or list Secrets cluster-wide, on a cluster where no other
// estate keeps records, is a capability and not yet a breach, and it is the
// ordinary state of every cluster-admin standing up the first estate. So it
// warns: loud on every apply, never a refusal. GitHub issue #1393 asks for a
// warning as the floor here and the refusal where the other namespace is
// known, and a gate that refused every first estate would be a gate everyone
// waives on their first day (#1102).
//
// Enumerating the other namespaces needs list on namespaces, which the
// recommended Role does not carry. When it cannot be done the finding says so
// in as many words rather than implying both questions were asked.
func checkReadIsolation(ctx context.Context, cs kubernetes.Interface, opts ClusterContractOptions) (Finding, error) {
	f := Finding{Setting: ClusterReadIsolation}

	var wide []string
	for _, verb := range []string{"get", "list"} {
		allowed, _, err := reviewSecrets(ctx, cs, "", verb)
		if err != nil {
			return f, err
		}
		if allowed {
			wide = append(wide, verb)
		}
	}
	wideClause := ""
	if len(wide) > 0 {
		wideClause = fmt.Sprintf("this identity may %s secrets in EVERY namespace, so the records namespace fences nothing against it", strings.Join(wide, " and "))
	}

	foreign, enumerated, err := foreignRecordNamespaces(ctx, cs, opts.Namespace)
	if err != nil {
		return f, err
	}

	var readable []string
	reviewed := 0
	truncated := false
	for _, ns := range foreign {
		if reviewed >= maxForeignNamespaceReviews {
			truncated = true
			break
		}
		reviewed++
		for _, verb := range []string{"get", "list"} {
			allowed, _, err := reviewSecrets(ctx, cs, ns, verb)
			if err != nil {
				return f, err
			}
			if allowed {
				readable = append(readable, ns+" ("+verb+")")
				break
			}
		}
	}
	if len(readable) > 0 {
		f.Found = fmt.Sprintf("this identity may read Secrets in another estate's records namespace: %s", strings.Join(readable, ", "))
		if wideClause != "" {
			f.Found += "; " + wideClause
		}
		return f, nil
	}

	switch {
	case !enumerated && wideClause != "":
		f.Outcome = Warned
		f.Found = wideClause + ", and other estates' records namespaces could not be enumerated here (listing namespaces is not permitted), so whether any exist was not checked"
	case !enumerated:
		f.Outcome = Passed
		f.Found = fmt.Sprintf("this identity may not get or list secrets outside namespace %q; other estates' records namespaces could not be enumerated, because listing namespaces is not permitted here, so whether a RoleBinding elsewhere reaches one was not checked", opts.Namespace)
	case wideClause != "":
		f.Outcome = Warned
		f.Found = wideClause + fmt.Sprintf(", and this cluster holds no other %s* namespace today, so nothing is exposed yet; the first estate that joins this cluster will be", KubernetesRecordNamespacePrefix)
	case reviewed == 0:
		f.Outcome = Passed
		f.Found = fmt.Sprintf("this identity may not get or list secrets outside namespace %q, and this cluster holds no other %s* namespace to check", opts.Namespace, KubernetesRecordNamespacePrefix)
	case truncated:
		f.Outcome = Passed
		f.Found = fmt.Sprintf("this identity may not get or list secrets outside namespace %q, and is refused Secrets in the first %d of %d other %s* namespaces", opts.Namespace, reviewed, len(foreign), KubernetesRecordNamespacePrefix)
	default:
		f.Outcome = Passed
		f.Found = fmt.Sprintf("this identity may not get or list secrets outside namespace %q, and is refused Secrets in all %d other %s* namespace(s)", opts.Namespace, reviewed, KubernetesRecordNamespacePrefix)
	}
	return f, nil
}

// foreignRecordNamespaces is every OTHER estate's records namespace this
// identity can see, sorted. enumerated is false when listing namespaces was
// refused, which is a different answer from "there are none".
func foreignRecordNamespaces(ctx context.Context, cs kubernetes.Interface, own string) (names []string, enumerated bool, err error) {
	list, err := cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		if k8serrors.IsForbidden(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("staterecord: kubernetes: listing namespaces to find other estates' records: %w", err)
	}
	for i := range list.Items {
		name := list.Items[i].Name
		if name == own || !strings.HasPrefix(name, KubernetesRecordNamespacePrefix) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, true, nil
}

// encryptionProviderFlag is the API server flag that turns on encryption at
// rest for Secrets. Its presence is the configuration itself, not a proxy for
// it: without it the API server writes a Secret's data to etcd base64-encoded
// and otherwise as it came.
const encryptionProviderFlag = "--encryption-provider-config"

// checkEncryptionAtRest is assertion 3, and the one that most often cannot be
// answered.
//
// There is no API object that says whether Secrets are encrypted at rest. The
// EncryptionConfiguration is a file named by an API server command-line flag,
// so the only thing readable through the API is the API server's own Pod - a
// static Pod in kube-system on kind and on any kubeadm cluster, and on EKS,
// GKE and AKS not a Pod in the user's cluster at all. Where the Pod is
// visible this reads the actual flag. Where it is not, the finding says "not
// readable from here" and is not a pass.
//
// Deliberately NOT done: inferring encryption from the distribution's name,
// from a StorageClass, from a KMS plugin Pod, or from anything else that
// correlates with the setting without being it. This repository does not
// count a check that cannot fail, and a green that is a guess is worse than
// an honest "not checked" - an operator acts on the first and investigates
// the second.
func checkEncryptionAtRest(ctx context.Context, cs kubernetes.Interface) Finding {
	f := Finding{Setting: ClusterEncryptionAtRest}

	pods, err := cs.CoreV1().Pods("kube-system").List(ctx, metav1.ListOptions{
		LabelSelector: "component=kube-apiserver",
	})
	if err != nil {
		f.Outcome = NotChecked
		if k8serrors.IsForbidden(err) {
			f.Found = "not readable from here, not checked: whether Secrets are encrypted at rest is an API server flag (" + encryptionProviderFlag + ") and the only thing that carries it through the API is the API server's own Pod, which this identity may not list in kube-system"
			return f
		}
		f.Found = fmt.Sprintf("not readable from here, not checked: listing the API server's Pod in kube-system failed (%s)", err)
		return f
	}
	if len(pods.Items) == 0 {
		f.Outcome = NotChecked
		f.Found = "not readable from here, not checked: no kube-apiserver Pod is visible in kube-system, which is the normal case on a managed control plane (EKS, GKE, AKS), where " + encryptionProviderFlag + " is set outside the cluster and readable only through that provider's own API"
		return f
	}

	var configured []string
	var bare []string
	for i := range pods.Items {
		pod := &pods.Items[i]
		if path := encryptionProviderConfigPath(pod); path != "" {
			configured = append(configured, pod.Name+" ("+encryptionProviderFlag+"="+path+")")
		} else {
			bare = append(bare, pod.Name)
		}
	}
	if len(bare) > 0 {
		f.Found = fmt.Sprintf("the API server Pod(s) %s carry no %s, so this cluster writes Secret data to etcd base64-encoded and not encrypted; anything that reads etcd or a backup of it reads every record", strings.Join(bare, ", "), encryptionProviderFlag)
		return f
	}
	f.Outcome = Passed
	f.Found = "the API server runs with " + strings.Join(configured, ", ")
	return f
}

// encryptionProviderConfigPath is the value of [encryptionProviderFlag] on
// any container of pod, or "" when no container carries it. Both spellings
// the flag takes are read: "--flag=value" and "--flag value".
func encryptionProviderConfigPath(pod *corev1.Pod) string {
	for i := range pod.Spec.Containers {
		argv := append(append([]string{}, pod.Spec.Containers[i].Command...), pod.Spec.Containers[i].Args...)
		for j, arg := range argv {
			if v, ok := strings.CutPrefix(arg, encryptionProviderFlag+"="); ok && v != "" {
				return v
			}
			if arg == encryptionProviderFlag && j+1 < len(argv) {
				return argv[j+1]
			}
		}
	}
	return ""
}

// checkEstateBoundary is assertion 4: is live/kubernetes/estate-boundary.yaml
// installed AND in force.
//
// Installed is not the same as in force, and the difference has already cost
// this repository a step that measured nothing (claim 39, step 5). Three
// things are asked, because any one of them alone would pass for a fence that
// refuses nothing:
//
//   - the policy exists and the API server has OBSERVED it
//     (status.observedGeneration matching metadata.generation), which is when
//     its CEL has been type-checked and it can start refusing;
//   - its CEL type-checks clean, because an expression the server warns about
//     is one it may not be able to evaluate;
//   - a binding exists naming it, with Deny among its validationActions. A
//     policy with no binding is inert, and a binding whose action is Warn or
//     Audit logs a cross-estate write and lets it through.
func checkEstateBoundary(ctx context.Context, cs kubernetes.Interface) Finding {
	f := Finding{Setting: ClusterEstateBoundary}

	policy, err := cs.AdmissionregistrationV1().ValidatingAdmissionPolicies().Get(ctx, EstateBoundaryPolicyName, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsForbidden(err) {
			f.Outcome = NotChecked
			f.Found = fmt.Sprintf("not readable from here, not checked: reading a ValidatingAdmissionPolicy needs cluster-scoped get on admissionregistration.k8s.io, which this identity does not hold, so whether %q fences writes to the record Secrets was not established", EstateBoundaryPolicyName)
			return f
		}
		if k8serrors.IsNotFound(err) {
			f.Found = fmt.Sprintf("no ValidatingAdmissionPolicy named %q is installed, so nothing refuses a write to this estate's record Secrets by an identity bound to another estate; install it with `kubectl apply -f live/kubernetes/estate-boundary.yaml`", EstateBoundaryPolicyName)
			return f
		}
		f.Outcome = NotChecked
		f.Found = fmt.Sprintf("not readable from here, not checked: reading the %q policy failed (%s)", EstateBoundaryPolicyName, err)
		return f
	}

	var problems []string
	if policy.Status.ObservedGeneration != policy.Generation {
		problems = append(problems, fmt.Sprintf("the API server has not observed generation %d of the policy yet (it has observed %d), so the policy is installed and not yet in force", policy.Generation, policy.Status.ObservedGeneration))
	}
	if tc := policy.Status.TypeChecking; tc != nil && len(tc.ExpressionWarnings) > 0 {
		var warnings []string
		for _, w := range tc.ExpressionWarnings {
			warnings = append(warnings, w.FieldRef+": "+w.Warning)
		}
		problems = append(problems, "the policy's CEL has type-check warnings ("+strings.Join(warnings, "; ")+")")
	}

	binding, err := cs.AdmissionregistrationV1().ValidatingAdmissionPolicyBindings().Get(ctx, EstateBoundaryPolicyName, metav1.GetOptions{})
	switch {
	case err == nil:
		problems = append(problems, bindingProblems(binding)...)
	case k8serrors.IsNotFound(err):
		problems = append(problems, fmt.Sprintf("no ValidatingAdmissionPolicyBinding named %q exists, and a policy with no binding is inert: it evaluates nothing and refuses nothing", EstateBoundaryPolicyName))
	case k8serrors.IsForbidden(err):
		f.Outcome = NotChecked
		f.Found = "not readable from here, not checked: the policy is installed, and whether a binding puts it in force could not be read, because this identity may not get a ValidatingAdmissionPolicyBinding"
		return f
	default:
		f.Outcome = NotChecked
		f.Found = fmt.Sprintf("not readable from here, not checked: the policy is installed, and reading its binding failed (%s)", err)
		return f
	}

	if len(problems) > 0 {
		f.Found = strings.Join(problems, "; ")
		return f
	}
	f.Outcome = Passed
	f.Found = fmt.Sprintf("the %q policy is installed, observed at generation %d, type-checks clean, and its binding denies", EstateBoundaryPolicyName, policy.Generation)
	return f
}

func bindingProblems(binding *admissionv1.ValidatingAdmissionPolicyBinding) []string {
	var problems []string
	if binding.Spec.PolicyName != EstateBoundaryPolicyName {
		problems = append(problems, fmt.Sprintf("the binding %q names policy %q and not %q, so it puts some other policy in force", binding.Name, binding.Spec.PolicyName, EstateBoundaryPolicyName))
	}
	denies := false
	var actions []string
	for _, a := range binding.Spec.ValidationActions {
		actions = append(actions, string(a))
		if a == admissionv1.Deny {
			denies = true
		}
	}
	if !denies {
		problems = append(problems, fmt.Sprintf("the binding's validationActions are [%s] and do not include Deny, so a cross-estate write to a record Secret is logged and then allowed", strings.Join(actions, ", ")))
	}
	return problems
}

// ClusterContractRefusal is the headline and the paragraph for one failed
// finding, in internal/command's statelessCommandRefusals shape: what was
// refused, then what it protects against and what to do instead. Empty for a
// finding that passed.
func ClusterContractRefusal(namespace string, f Finding) (summary, detail string) {
	if f.OK() {
		return "", ""
	}
	why, fix := "", ""
	switch f.Setting {
	case ClusterNamespaceAccess:
		why = "Every record this estate keeps is a Secret in that namespace. A verb the store needs and does not have stops a run part-way through writing records, which leaves the estate half-recorded, and a records namespace that is not there reads as an estate with no records at all."
		fix = fmt.Sprintf("Create the namespace if it is missing (`kubectl create namespace %s`) and grant this identity the verbs it lacks on secrets in it:\n\n  kubectl create role records-rw -n %s --verb=%s --resource=secrets\n  kubectl create rolebinding <name> -n %s --role=records-rw --serviceaccount=<ns>:<name>",
			namespace, namespace, strings.Join(KubernetesRecordVerbs, ","), namespace)
	case ClusterReadIsolation:
		why = "The namespace is the read boundary and there is no other one. RBAC cannot condition on a label, and admission is never consulted for a get or a list, so an identity that may read Secrets outside this namespace reads every other estate's records in this cluster, and every record holds whatever the resource it records holds."
		fix = fmt.Sprintf("Bind this identity to a Role in %s rather than a ClusterRole, and take away any cluster-wide read of secrets it holds. `kubectl auth can-i list secrets --all-namespaces` under this identity is the same question this check asked.", namespace)
	case ClusterEncryptionAtRest:
		why = "A Secret is base64, not encryption. Without an EncryptionConfiguration the API server writes each record's payload into etcd as it came, so anything that reads etcd or an etcd backup reads every record in the estate."
		fix = "Fix it by starting the API server with " + encryptionProviderFlag + " and an EncryptionConfiguration covering secrets, then rewriting the existing Secrets so they are stored encrypted (`kubectl get secrets -A -o json | kubectl replace -f -`); on a managed control plane it is that provider's own setting instead (EKS envelope encryption, GKE application-layer secrets encryption, AKS KMS etcd encryption), and on kind and minikube there is no flag set at all, which is what this is telling you."
	case ClusterEstateBoundary:
		why = "The record Secrets carry the estate's tofu-estate label, and that policy is what stops an identity bound to another estate from writing them. Without it in force, any identity with write access to this namespace can overwrite or delete another estate's records, and a record can be the only copy of what it says."
		fix = "Fix it, as a cluster admin, by installing the policy and its binding with `kubectl apply -f live/kubernetes/estate-boundary.yaml` and then granting this estate to the identity that runs it with live/kubernetes/estate-grant.yaml, which is one ClusterRole and one binding per estate."
	}
	if f.Outcome == Warned {
		summary = fmt.Sprintf("The record store cluster's %s is weaker than it should be", f.Setting)
		detail = fmt.Sprintf("Namespace %q: %s.\n\n%s\n\nThe run goes on, because nothing is exposed by this yet. %s", namespace, f.Found, why, fix)
		return summary, detail
	}
	if f.Outcome == NotChecked {
		summary = fmt.Sprintf("The record store cluster's %s could not be checked", f.Setting)
		detail = fmt.Sprintf("Namespace %q: %s.\n\n%s\n\nThe run goes on and this is not a pass: nothing here says the property holds, and this says so on every run for as long as it cannot be read. %s\n\n%s `choudoufu live-cluster`, run by an identity that can read what this one cannot, answers the question properly and exits non-zero until it does.", namespace, f.Found, why, fix, ClusterWaiverLine(f.Setting))
		return summary, detail
	}
	summary = fmt.Sprintf("The record store cluster fails its %s assertion", f.Setting)
	detail = fmt.Sprintf("Namespace %q: %s.\n\n%s\n\n%s\n\n%s", namespace, f.Found, why, fix, ClusterWaiverLine(f.Setting))
	return summary, detail
}

// ClusterWaiverLine is the other way out of a refusal, as one sentence
// carrying the exact line to write. Every refusal ends with it.
//
// It is spelled out rather than described because of who reads it: someone
// following the Kubernetes documentation on kind, who writes
// `record_store "kubernetes" {}`, applies as cluster-admin and is refused
// twice - for an API server flag kind does not set and a policy nobody told
// them to install. Both of those have a real fix and both have a legitimate
// "not on this cluster, and I know". A refusal that names only the fix
// leaves that reader with a message they cannot act on, and a refusal that
// says "waive it" without the line leaves them guessing at the spelling.
func ClusterWaiverLine(settings ...Setting) string {
	return fmt.Sprintf("Or accept it on purpose: put `%s` in this record_store \"kubernetes\" block, which lets every run proceed and makes each run say what the waiver costs.", ClusterWaiverArgument(settings...))
}

// ClusterWaiverArgument is the argument itself, for a caller writing its own
// sentence around it - internal/live/projection's closing line when more than
// one assertion refuses at once.
func ClusterWaiverArgument(settings ...Setting) string {
	quoted := make([]string, 0, len(settings))
	for _, s := range settings {
		quoted = append(quoted, `"`+string(s)+`"`)
	}
	return fmt.Sprintf("allow_insecure = [%s]", strings.Join(quoted, ", "))
}

// ClusterWaiverCost says what an estate gives up by waiving setting, as a
// clause that completes "is waived, so ...". GitHub issue #1340's rule, on
// this store: the warning names the setting and its cost in the same
// sentence, never a generic "running with reduced checks".
func ClusterWaiverCost(setting Setting) string {
	switch setting {
	case ClusterNamespaceAccess:
		return "nothing has checked that this identity can do what the store will ask of it, so a run may stop part-way through writing records"
	case ClusterReadIsolation:
		return "nothing is known to stop this identity from reading another estate's records, and a record holds whatever the resource it records holds"
	case ClusterEncryptionAtRest:
		return "nothing is known to encrypt the records in etcd, so anything that reads etcd or a backup of it reads every record in the estate"
	case ClusterEstateBoundary:
		return "nothing is known to stop an identity bound to another estate from overwriting or deleting this estate's records, and a record can be the only copy of what it says"
	}
	return "that assertion is not made"
}

// ClusterContractRefusalClosing is the line that follows two or more
// refusals in one message.
//
// A plain kind cluster refuses an estate's first contact twice at once - no
// --encryption-provider-config and no estate boundary policy - and each
// refusal's own waiver line names only itself, so a reader following them
// both would write allow_insecure twice in one block, which is a duplicate
// argument and does not parse. This is the line they can paste.
func ClusterContractRefusalClosing(refused []Setting) string {
	return fmt.Sprintf("To accept all %d on purpose, the waiver is one line and not %d: `%s`",
		len(refused), len(refused), ClusterWaiverArgument(refused...))
}

// ClusterContractCheckFailed is what an apply says when the contract read
// itself could not be made - not a finding about the cluster, but nothing
// known about it at all.
func ClusterContractCheckFailed(err error) (summary, detail string) {
	return "Cannot check the record store cluster",
		fmt.Sprintf("Before applying, the cluster this estate keeps its records in is checked for the properties those records depend on, and that check could not be made: %s. Nothing has been applied.", err)
}
