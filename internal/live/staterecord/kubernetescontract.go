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

// maxForeignNamespaceReviews bounds how many OTHER namespaces one
// read-isolation check reviews. A cluster with a thousand namespaces would
// otherwise make two thousand SelfSubjectAccessReviews on every first
// contact. A truncated review is NOT CHECKED and says how many of how many it
// got through, because "the first 25 were fenced" is not an answer to "is
// anything else readable" (GitHub issue #1448, B1).
const maxForeignNamespaceReviews = 25

// maxForeignRecordSecrets bounds a list that only has to answer "is there
// anything here at all, and whose". Nothing reads a record's payload here;
// the list exists to read labels, and the API server sends whole objects, so
// it asks for as few as will name the problem.
const maxForeignRecordSecrets = 5

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

	boundary, err := checkEstateBoundary(ctx, cs, opts)
	if err != nil {
		return nil, err
	}
	findings = append(findings, boundary)

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
// the question is whether this identity can read another estate's records.
//
// # Three ways the boundary can be absent
//
// A records namespace belonging to another estate, readable by this identity.
// Another estate's records INSIDE this namespace, which two blocks configured
// with the same `namespace` produce and which nothing used to ask about
// (GitHub issue #1448, B2): every identity that may list Secrets here reads
// the neighbour's payloads, and no policy is even consulted, because
// admission never sees a get. Either is read, and wrong, and refuses.
//
// The third is a capability rather than a breach: get or list on Secrets
// cluster-wide, on a cluster where no other estate keeps records. It is the
// ordinary state of every cluster-admin standing up the first estate, so it
// warns - loud on every apply, never a refusal. #1393 asks for a warning as
// the floor and the refusal where the other estate's records are known, and a
// gate that refused every first estate would be a gate everyone waives on
// their first day (#1102).
//
// # What is not asked is not a pass
//
// Finding the other estates means listing namespaces, which the recommended
// Role does not carry. This function used to report that identity as PASSED
// in a sentence whose own words were "was not checked" (#1448, B1), which
// made an identity holding one stray RoleBinding into a neighbour's records
// namespace read green. It is NOT CHECKED now, and it says who can answer it.
//
// Nor does the enumeration go by name any more. A records namespace is
// whatever a record_store block's `namespace` says, so `team-prod-records` is
// one and was invisible to a check that only looked at `tofu-records-*`
// (#1448, B3). Every other namespace is reviewed instead, the record-named
// ones first so a truncated review spends its budget where the records
// usually are; one that is readable and not record-named is settled by
// looking for record Secrets in it, and left NOT CHECKED when even that
// cannot be asked.
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

	// The neighbour that is already inside, before the ones outside.
	sharing, sharingAsked, err := estatesSharingNamespace(ctx, cs, opts.Namespace, opts.Estate)
	if err != nil {
		return f, err
	}
	if len(sharing) > 0 {
		f.Found = fmt.Sprintf("namespace %q holds record Secrets belonging to another estate (%s), so it is the records namespace of more than one estate and every identity that may list Secrets here reads the other's payloads; admission is never consulted for a read, so nothing fences this",
			opts.Namespace, strings.Join(sharing, ", "))
		if wideClause != "" {
			f.Found += "; " + wideClause
		}
		return f, nil
	}

	others, enumerated, err := otherNamespaces(ctx, cs, opts.Namespace)
	if err != nil {
		return f, err
	}

	var breaches, undecided []string
	reviewed := 0
	truncated := false
	for _, ns := range others {
		if reviewed >= maxForeignNamespaceReviews {
			truncated = true
			break
		}
		reviewed++
		canGet, _, err := reviewSecrets(ctx, cs, ns, "get")
		if err != nil {
			return f, err
		}
		canList, _, err := reviewSecrets(ctx, cs, ns, "list")
		if err != nil {
			return f, err
		}
		if !canGet && !canList {
			continue
		}
		verb := "get"
		if canList {
			verb = "list"
		}
		if strings.HasPrefix(ns, KubernetesRecordNamespacePrefix) {
			breaches = append(breaches, ns+" ("+verb+")")
			continue
		}
		// Not named like a records namespace, and readable. Whether it IS
		// one is a question about what is in it, which this identity can ask
		// only where it may list.
		if !canList {
			undecided = append(undecided, ns+" (get)")
			continue
		}
		holds, asked, err := namespaceHoldsRecords(ctx, cs, ns)
		if err != nil {
			return f, err
		}
		switch {
		case !asked:
			undecided = append(undecided, ns+" (list)")
		case holds:
			breaches = append(breaches, ns+" (list)")
		}
	}
	if len(breaches) > 0 {
		f.Found = fmt.Sprintf("this identity may read Secrets in another estate's records namespace: %s", strings.Join(breaches, ", "))
		if wideClause != "" {
			f.Found += "; " + wideClause
		}
		return f, nil
	}

	// Nothing was found wrong. What is left is how much of the question was
	// actually asked.
	unchecked := ""
	switch {
	case !enumerated:
		unchecked = "the namespaces of this cluster could not be listed here, so whether another estate keeps records in one this identity can read was not asked at all"
	case truncated:
		unchecked = fmt.Sprintf("%d of this cluster's %d other namespaces were reviewed and the rest were not, so whether one of those holds another estate's records was not asked", reviewed, len(others))
	case len(undecided) > 0:
		unchecked = fmt.Sprintf("this identity may read Secrets in %s, which is not named %s* and could be a records namespace under any other name, and it may not list there, so whether it holds another estate's records was not established",
			strings.Join(undecided, ", "), KubernetesRecordNamespacePrefix)
	}
	if !sharingAsked {
		if unchecked != "" {
			unchecked += "; and "
		}
		unchecked += fmt.Sprintf("this identity may not list Secrets in namespace %q, so whether another estate keeps its records in this one was not established", opts.Namespace)
	}
	if unchecked != "" {
		f.Outcome = NotChecked
		f.Found = "not readable from here, not checked: " + unchecked
		if wideClause != "" {
			f.Found = "not readable from here, not checked: " + wideClause + ", and " + unchecked
		}
		f.Found += fmt.Sprintf(". `choudoufu live-cluster -namespace=%s`, run by an identity that may list namespaces and Secrets across this cluster, asks it and answers it", opts.Namespace)
		return f, nil
	}

	switch {
	case wideClause != "" && len(others) == 0:
		f.Outcome = Warned
		f.Found = wideClause + fmt.Sprintf(", and %q is the only namespace in this cluster, so nothing is exposed yet; the first estate that joins this cluster will be", opts.Namespace)
	case wideClause != "":
		f.Outcome = Warned
		f.Found = wideClause + fmt.Sprintf(", and none of this cluster's %d other namespaces holds a record Secret, so nothing is exposed yet; the first estate that joins this cluster will be", reviewed)
	case len(others) == 0:
		f.Outcome = Passed
		f.Found = fmt.Sprintf("this identity may not get or list secrets outside namespace %q, which is the only namespace in this cluster", opts.Namespace)
	default:
		f.Outcome = Passed
		f.Found = fmt.Sprintf("this identity may not get or list secrets outside namespace %q, and is refused Secrets in all %d other namespace(s) in this cluster, whatever they are named", opts.Namespace, reviewed)
	}
	return f, nil
}

// otherNamespaces is every namespace in this cluster except own, records
// namespaces first and each group sorted, so a review that runs out of budget
// has spent it where another estate's records most likely are. enumerated is
// false when listing namespaces was refused, which is a different answer from
// "there are none".
func otherNamespaces(ctx context.Context, cs kubernetes.Interface, own string) (names []string, enumerated bool, err error) {
	list, err := cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		if k8serrors.IsForbidden(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("staterecord: kubernetes: listing namespaces to find other estates' records: %w", err)
	}
	var records, rest []string
	for i := range list.Items {
		name := list.Items[i].Name
		switch {
		case name == own:
		case strings.HasPrefix(name, KubernetesRecordNamespacePrefix):
			records = append(records, name)
		default:
			rest = append(rest, name)
		}
	}
	sort.Strings(records)
	sort.Strings(rest)
	return append(records, rest...), true, nil
}

// estatesSharingNamespace is every OTHER estate whose record Secrets are in
// this store's own records namespace, sorted. asked is false when the list was
// refused, which is not the same answer as "there are none".
//
// The list selects on the managed-by label alone, which is what makes it
// answerable by an identity scoped to this namespace: it holds list on
// Secrets here, since that is what the store itself needs. Where the estate is
// known the selector also excludes it, so the request carries back a
// neighbour's objects and never this estate's own.
func estatesSharingNamespace(ctx context.Context, cs kubernetes.Interface, namespace, estate string) (names []string, asked bool, err error) {
	selector := KubernetesManagedByLabel + "=" + KubernetesManagedByValue
	if estate != "" {
		selector += "," + KubernetesEstateLabel + "!=" + estate
	}
	list, err := cs.CoreV1().Secrets(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
		Limit:         maxForeignRecordSecrets,
	})
	if err != nil {
		if k8serrors.IsForbidden(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("staterecord: kubernetes: listing the record Secrets in namespace %q to see whose they are: %w", namespace, err)
	}
	seen := map[string]bool{}
	for i := range list.Items {
		other := list.Items[i].Labels[KubernetesEstateLabel]
		if other == "" || other == estate || seen[other] {
			continue
		}
		seen[other] = true
		names = append(names, other)
	}
	// With no estate named there is no "own" to subtract, so one estate's
	// records here are the expected case and two are the finding.
	if estate == "" && len(names) < 2 {
		return nil, true, nil
	}
	sort.Strings(names)
	return names, true, nil
}

// namespaceHoldsRecords reports whether ns holds Secrets this store would
// recognise as records. asked is false when the list was refused between the
// authorizer's yes and the request itself.
func namespaceHoldsRecords(ctx context.Context, cs kubernetes.Interface, ns string) (holds, asked bool, err error) {
	list, err := cs.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{
		LabelSelector: KubernetesManagedByLabel + "=" + KubernetesManagedByValue,
		Limit:         1,
	})
	if err != nil {
		if k8serrors.IsForbidden(err) {
			return false, false, nil
		}
		return false, false, fmt.Errorf("staterecord: kubernetes: listing the record Secrets in namespace %q: %w", ns, err)
	}
	return len(list.Items) > 0, true, nil
}

// encryptionProviderFlag is the API server flag that turns on encryption at
// rest for Secrets. Its presence is the configuration itself, not a proxy for
// it: without it the API server writes a Secret's data to etcd base64-encoded
// and otherwise as it came.
const encryptionProviderFlag = "--encryption-provider-config"

// staticPodMirrorAnnotation is on the mirror Pod the kubelet publishes for a
// static Pod, and on nothing else. Together with an ownerReference naming the
// Node it is what tells the real API server apart from any other Pod in
// kube-system that happens to carry component=kube-apiserver, which was all
// the check used to ask for (GitHub issue #1448, B5). Measured on kind
// v1.36.1: the kube-apiserver Pod carries the annotation, a Node
// ownerReference and the tier=control-plane label.
const staticPodMirrorAnnotation = "kubernetes.io/config.mirror"

// checkEncryptionAtRest is assertion 3, and the one that can never be
// answered from inside.
//
// There is no API object that says whether Secrets are encrypted at rest. The
// EncryptionConfiguration is a FILE named by an API server command-line flag,
// so the only thing readable through the API is the API server's own Pod - a
// static Pod in kube-system on kind and on any kubeadm cluster, and on EKS,
// GKE and AKS not a Pod in the user's cluster at all.
//
// # The flag is half an answer, so it is half a verdict
//
// The flag's ABSENCE settles it: an API server started without it writes
// Secret data to etcd base64-encoded and otherwise as it came. That is read,
// and wrong, and refuses.
//
// Its presence settles nothing. `--encryption-provider-config` names a file
// whose first provider for secrets may be `identity`, which is the
// configuration's own way of spelling no encryption, and that file is not an
// API object at any permission level. This used to be a PASS (#1448, B5); it
// is NOT CHECKED, and the finding carries the command an operator runs on the
// control-plane node to finish the question.
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

	var servers []*corev1.Pod
	labelledOnly := 0
	for i := range pods.Items {
		pod := &pods.Items[i]
		if isAPIServerMirrorPod(pod) {
			servers = append(servers, pod)
			continue
		}
		labelledOnly++
	}
	if len(servers) == 0 {
		f.Outcome = NotChecked
		f.Found = "not readable from here, not checked: no kube-apiserver static Pod is visible in kube-system, which is the normal case on a managed control plane (EKS, GKE, AKS), where " + encryptionProviderFlag + " is set outside the cluster and readable only through that provider's own API"
		if labelledOnly > 0 {
			f.Found += fmt.Sprintf("; %d Pod(s) there carry the component=kube-apiserver label and are not the API server (no %s annotation and no Node owner), so nothing was read off them", labelledOnly, staticPodMirrorAnnotation)
		}
		return f
	}

	var configured []string
	var paths []string
	var bare []string
	for _, pod := range servers {
		if path := encryptionProviderConfigPath(pod); path != "" {
			configured = append(configured, pod.Name+" ("+encryptionProviderFlag+"="+path+")")
			paths = append(paths, path)
		} else {
			bare = append(bare, pod.Name)
		}
	}
	if len(bare) > 0 {
		f.Found = fmt.Sprintf("the API server Pod(s) %s carry no %s, so this cluster writes Secret data to etcd base64-encoded and not encrypted; anything that reads etcd or a backup of it reads every record", strings.Join(bare, ", "), encryptionProviderFlag)
		return f
	}
	f.Outcome = NotChecked
	f.Found = fmt.Sprintf("not readable from here, not checked: the API server runs with %s, and the file that flag names is not an API object, so whether secrets are encrypted was not established: a configuration whose first provider for secrets is `identity` sets the flag and encrypts nothing. Read it on the control-plane node with `sudo cat %s` and check which provider comes first under the resources entry covering secrets",
		strings.Join(configured, ", "), paths[0])
	return f
}

// isAPIServerMirrorPod reports whether pod is the kubelet's mirror of the
// API server's static Pod, rather than any Pod someone labelled
// component=kube-apiserver. Both marks are asked for: the mirror annotation
// the kubelet writes, and the Node that owns what it publishes.
func isAPIServerMirrorPod(pod *corev1.Pod) bool {
	if _, ok := pod.Annotations[staticPodMirrorAnnotation]; !ok {
		return false
	}
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "Node" {
			return true
		}
	}
	return false
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
// installed AND in force AND this identity on the allowed side of it.
//
// Installed is not the same as in force, and the difference has already cost
// this repository a step that measured nothing (claim 39, step 5). The name
// is not the same as the policy either: this used to read the policy's
// status and its binding's validationActions and nothing out of
// policy.Spec, so a policy of the right name whose one validation was `true`
// reported "installed ... and its binding denies" (GitHub issue #1448, B4).
// What is asked:
//
//   - the policy exists and the API server has OBSERVED it
//     (status.observedGeneration matching metadata.generation), which is when
//     its CEL has been type-checked and it can start refusing;
//   - its CEL type-checks clean, because an expression the server warns about
//     is one it may not be able to evaluate;
//   - it fails closed and covers secrets for CREATE, UPDATE and DELETE, and
//     its CEL is the CEL live/kubernetes/estate-boundary.yaml ships; see
//     kubernetesboundary.go for where that expectation comes from and why it
//     is not transcribed into Go;
//   - a binding exists naming it, with Deny among its validationActions, and
//     its matchResources do not scope the policy away from the records
//     namespace. A policy with no binding is inert, and a binding whose
//     action is Warn or Audit logs a cross-estate write and lets it through;
//   - and, once all of that holds, that this identity holds `use` on
//     estates.choudoufu.intentius.io/<estate>, which is the grant the policy's
//     own CEL asks the authorizer for (#1448, B6). Without it the fence is in
//     force and refuses every write this run would make, which is four greens
//     and an apply that cannot write a record.
func checkEstateBoundary(ctx context.Context, cs kubernetes.Interface, opts ClusterContractOptions) (Finding, error) {
	f := Finding{Setting: ClusterEstateBoundary}

	policy, err := cs.AdmissionregistrationV1().ValidatingAdmissionPolicies().Get(ctx, EstateBoundaryPolicyName, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsForbidden(err) {
			f.Outcome = NotChecked
			f.Found = fmt.Sprintf("not readable from here, not checked: reading a ValidatingAdmissionPolicy needs cluster-scoped get on admissionregistration.k8s.io, which this identity does not hold, so whether %q fences writes to the record Secrets was not established", EstateBoundaryPolicyName)
			return f, nil
		}
		if k8serrors.IsNotFound(err) {
			f.Found = fmt.Sprintf("no ValidatingAdmissionPolicy named %q is installed, so nothing refuses a write to this estate's record Secrets by an identity bound to another estate; install it with `kubectl apply -f live/kubernetes/estate-boundary.yaml`", EstateBoundaryPolicyName)
			return f, nil
		}
		f.Outcome = NotChecked
		f.Found = fmt.Sprintf("not readable from here, not checked: reading the %q policy failed (%s)", EstateBoundaryPolicyName, err)
		return f, nil
	}

	var problems, undecided []string
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

	structural, structuralUndecided := policyStructureProblems(policy, opts.Namespace, opts.Estate)
	problems = append(problems, structural...)
	undecided = append(undecided, structuralUndecided...)

	shipped, err := shippedEstateBoundaryPolicy()
	if err != nil {
		return f, err
	}
	if diffs := policyCELDifferences(policy, shipped); len(diffs) > 0 {
		problems = append(problems, fmt.Sprintf("the installed policy's CEL is not the CEL live/kubernetes/estate-boundary.yaml ships (%s), so what it refuses is not what this store asserts is refused", strings.Join(diffs, "; ")))
	}

	binding, err := cs.AdmissionregistrationV1().ValidatingAdmissionPolicyBindings().Get(ctx, EstateBoundaryPolicyName, metav1.GetOptions{})
	switch {
	case err == nil:
		problems = append(problems, bindingProblems(binding)...)
		scope, scopeUndecided := bindingScopeProblems(binding, opts.Namespace, opts.Estate)
		problems = append(problems, scope...)
		undecided = append(undecided, scopeUndecided...)
	case k8serrors.IsNotFound(err):
		problems = append(problems, fmt.Sprintf("no ValidatingAdmissionPolicyBinding named %q exists, and a policy with no binding is inert: it evaluates nothing and refuses nothing", EstateBoundaryPolicyName))
	case k8serrors.IsForbidden(err):
		f.Outcome = NotChecked
		f.Found = "not readable from here, not checked: the policy is installed, and whether a binding puts it in force could not be read, because this identity may not get a ValidatingAdmissionPolicyBinding"
		return f, nil
	default:
		f.Outcome = NotChecked
		f.Found = fmt.Sprintf("not readable from here, not checked: the policy is installed, and reading its binding failed (%s)", err)
		return f, nil
	}

	if len(problems) > 0 {
		f.Found = strings.Join(append(problems, undecided...), "; ")
		return f, nil
	}
	if len(undecided) > 0 {
		f.Outcome = NotChecked
		f.Found = "not readable from here, not checked: " + strings.Join(undecided, "; ")
		return f, nil
	}

	inForce := fmt.Sprintf("the %q policy is installed, observed at generation %d, type-checks clean, carries the CEL live/kubernetes/estate-boundary.yaml ships, and its binding denies", EstateBoundaryPolicyName, policy.Generation)

	// The fence is up. Whether this identity is inside it is the other half,
	// and it is only the other half for a run that WRITES: a plan identity
	// makes no write for the policy to refuse.
	switch {
	case opts.Estate == "":
		f.Outcome = Passed
		f.Found = inForce + fmt.Sprintf("; no estate was named, so whether an identity holds `%s` on %s.%s/<estate> was not asked (`choudoufu live-cluster -estate=<name>` asks it)", EstateGrantVerb, EstateGrantResource, EstateGrantGroup)
		return f, nil
	case !runWritesRecords(opts.RequiredVerbs):
		f.Outcome = Passed
		f.Found = inForce + fmt.Sprintf("; this run writes no record, so the `%s` grant on %s.%s/%s that the policy asks the authorizer for is reported and not required", EstateGrantVerb, EstateGrantResource, EstateGrantGroup, opts.Estate)
		return f, nil
	}

	allowed, reason, err := reviewEstateUse(ctx, cs, opts.Estate)
	if err != nil {
		return f, err
	}
	if !allowed {
		detail := ""
		if reason != "" {
			detail = " (" + reason + ")"
		}
		f.Found = inForce + fmt.Sprintf("; and this identity does not hold `%s` on %s.%s/%s%s, which is exactly what that policy's CEL asks the authorizer for, so it refuses every write this run would make to a record Secret. Grant it with:\n\n  sed -e 's/ESTATE/%s/g' -e 's/PRINCIPAL_NAMESPACE/<namespace>/g' -e 's/PRINCIPAL/<serviceaccount>/g' live/kubernetes/estate-grant.yaml | kubectl apply -f -",
			EstateGrantVerb, EstateGrantResource, EstateGrantGroup, opts.Estate, detail, opts.Estate)
		return f, nil
	}

	f.Outcome = Passed
	f.Found = inForce + fmt.Sprintf("; and this identity holds `%s` on %s.%s/%s, so its writes to the record Secrets are on the allowed side of it", EstateGrantVerb, EstateGrantResource, EstateGrantGroup, opts.Estate)
	return f, nil
}

// runWritesRecords reports whether the verbs this run needs include one that
// changes a record Secret, which is what the estate boundary policy is
// consulted for. Nil is every verb; see [ClusterContractOptions.RequiredVerbs].
func runWritesRecords(required []string) bool {
	if len(required) == 0 {
		return true
	}
	for _, verb := range required {
		switch verb {
		case "create", "update", "patch", "delete", "deletecollection":
			return true
		}
	}
	return false
}

// reviewEstateUse asks the authorizer the question the boundary policy's own
// CEL asks: may this identity `use` the virtual estate resource. It is a
// SelfSubjectAccessReview like every other question this file asks, so it
// needs no permission and changes nothing.
func reviewEstateUse(ctx context.Context, cs kubernetes.Interface, estate string) (allowed bool, reason string, err error) {
	review := &authzv1.SelfSubjectAccessReview{
		Spec: authzv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authzv1.ResourceAttributes{
				Group:    EstateGrantGroup,
				Resource: EstateGrantResource,
				Name:     estate,
				Verb:     EstateGrantVerb,
			},
		},
	}
	out, err := cs.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return false, "", fmt.Errorf("staterecord: kubernetes: asking the API server whether this identity may %s the estate %q: %w", EstateGrantVerb, estate, err)
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
		fix = fmt.Sprintf("Bind this identity to a Role in %s rather than a ClusterRole, and take away any cluster-wide read of secrets it holds. `kubectl auth can-i list secrets --all-namespaces` under this identity is the same question this check asked. If the finding names another ESTATE rather than another namespace, two record_store blocks are configured with this one namespace: give each estate its own and move its records into it, because a Role here cannot fence one estate's Secrets from the other's.", namespace)
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
