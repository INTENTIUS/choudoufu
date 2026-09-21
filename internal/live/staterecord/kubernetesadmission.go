// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"errors"
	"fmt"
	"strings"

	authzv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AdmissionDeniedError reports that the API server's ADMISSION stage refused a
// write, and that the authorizer would have allowed it. GitHub issue #1448,
// section C.
//
// Both refusals are a 403 with reason Forbidden, and [IsAccessDenied] read
// every 403 as the authorizer's, which is what #1370's reader tolerance is
// for. So a ServiceAccount with full RBAC on Secrets in the records namespace
// and no `use` grant on its estate - the identity
// live/kubernetes/estate-boundary.yaml exists to fence - had the policy's own
// refusal of the provisioning sentinel read as "this run may read and not
// write". An earlier, granted run had left the sentinel, the List in
// internal/live/projection's provisionStoreSentinel found it, and the store
// opened green. The first-contact contract was skipped, the run planned, the
// apply started, and the same policy then refused every record write it made.
// The fence's own refusal was read as a benign read-only identity at the one
// moment walking away was free. Measured on kind.
//
// So this is a refusal rather than an outage or a tolerated denial: the
// cluster was reached and answered, the answer is about a grant no retry
// changes, and every record this run would write meets the same policy.
//
// The grant this refusal asks for is named with [EstateGrantVerb],
// [EstateGrantResource] and [EstateGrantGroup], the same three
// [checkEstateBoundary] puts in its own SelfSubjectAccessReview, so the two
// cannot come to ask an operator for different grants. The rendered sentence
// is unchanged by that: the constants spell what the string used to.
//
// Policy is [EstateBoundaryPolicyName] when the API server named this fork's
// own estate boundary policy, and "" when some other admission controller
// refused the write - another policy, a validating webhook, a built-in plugin.
// Either way it is not reader tolerance: that tolerance is for a denial
// positively identified as the authorizer's.
type AdmissionDeniedError struct {
	Namespace string
	Key       string
	Estate    string

	// Verb is the Secret verb the API server refused: "create", "update" or
	// "delete".
	Verb string

	// Policy is the admission policy the API server named, or "".
	Policy string

	Err error
}

func (e *AdmissionDeniedError) Error() string {
	if e.Policy != "" {
		return fmt.Sprintf(
			"staterecord: kubernetes: the %s policy refused this run's %s of %q in namespace %q, so this identity holds no `%s` on %s.%s/%s and every record this run writes is refused the same way; grant it with `sed -e 's/ESTATE/%s/g' -e 's/PRINCIPAL_NAMESPACE/<namespace>/g' -e 's/PRINCIPAL/<serviceaccount>/g' live/kubernetes/estate-grant.yaml | kubectl apply -f -`: %v",
			e.Policy, e.Verb, e.Key, e.Namespace,
			EstateGrantVerb, EstateGrantResource, EstateGrantGroup, e.Estate,
			e.Estate, e.Err)
	}
	return fmt.Sprintf(
		"staterecord: kubernetes: an admission policy on this cluster refused this run's %s of %q in namespace %q, and the API server's authorizer allows this identity that write, so every record this run writes meets the same policy; read what refused it and either grant this identity what it asks for or take the estate's records elsewhere: %v",
		e.Verb, e.Key, e.Namespace, e.Err)
}

func (e *AdmissionDeniedError) Unwrap() error { return e.Err }

// admissionWriteVerbs are the Secret verbs admission sees. A get or a list
// never reaches admission at all, so a 403 on a read is the authorizer's
// whatever else is installed on the cluster, and [KubernetesStore.classify]
// answers those without asking anything.
const (
	admissionVerbCreate = "create"
	admissionVerbUpdate = "update"
	admissionVerbDelete = "delete"
)

// classifyWrite is [KubernetesStore.classify] for the three calls admission
// sees. It asks whether a 403 was admission's before falling through to the
// classification every other failure gets.
func (s *KubernetesStore) classifyWrite(ctx context.Context, doing, verb, key string, err error) error {
	if fault := s.admissionFault(ctx, verb, key, err); fault != nil {
		return fault
	}
	return s.classify(doing, key, err)
}

// admissionFault returns the [AdmissionDeniedError] to raise instead of a
// tolerable denial, and nil when the 403 was the authorizer's own or was
// something else entirely.
//
// # How the two are told apart
//
// By asking the authorizer, not by reading English. Measured on kind against
// a v1.36.1 API server, all four of these are `reason: Forbidden, code: 403`:
//
//   - The estate boundary policy's denial carries Details.Name and one
//     Details.Causes entry whose message begins "ValidatingAdmissionPolicy
//     'choudoufu-estate-boundary' with binding ... denied request:".
//   - A validating WEBHOOK's denial carries no Details at all.
//   - A built-in admission plugin's denial (a ResourceQuota, measured)
//     carries Details.Name and no Causes.
//   - RBAC's denial carries Details.Kind and no Details.Name and no Causes.
//
// So Causes tells the estate boundary policy from RBAC, and tells nothing
// about a webhook: a webhook's 403 and RBAC's differ only in prose. What does
// separate them is a SelfSubjectAccessReview for the same verb, resource,
// namespace and object name. The authorizer runs before admission, so a write
// that admission refused is one the authorizer ALLOWED, and a write the
// authorizer refused never reached admission. Measured: the fenced identity's
// review answers allowed=true beside its 403, and the read-only identity's
// answers allowed=false beside its own. The review is granted to
// system:authenticated by the stock system:basic-user role, so asking it needs
// nothing an operator has to grant.
//
// # What this does to #1370, and to a cluster with an authorization webhook
//
// #1370's tolerance is for a run whose identity may read the store and not
// write it. The review asks the WHOLE authorizer chain, so on a cluster whose
// authorization runs through a webhook as well as RBAC, an identity that
// webhook refuses answers allowed=false and is tolerated exactly as an
// RBAC-refused one is. Reading the 403's prose for RBAC's own sentence would
// have been too strict there, and is only ever the fallback below.
//
// The identity that DOES change behaviour is one the authorizer allows to
// create record Secrets and the estate boundary refuses. That identity is not
// #1370's reader - a get/list identity is refused by the authorizer, so
// admission never sees its write - and it is the identity PR #1452's cluster
// contract fails `estate_boundary` for on every run that writes a record, for
// the same reason and with the same remedy. Refusing it when the store opens
// says the same thing at the one moment nothing has been written yet.
//
// # When the review cannot be asked
//
// A store built from a bare SecretInterface has no clientset (the conformance
// suite), and the review itself can fail. Then this falls back to the only
// other signal there is, the policy's name inside the refusal, and a 403 that
// does not name it is left to the authorizer's side. That fallback is narrow
// on purpose: it can recognise this fork's own fence and nothing else, and it
// is the one place here that reads a message rather than asking a question.
// Every caller internal/live/projection builds passes a clientset.
func (s *KubernetesStore) admissionFault(ctx context.Context, verb, key string, err error) error {
	if !k8serrors.IsForbidden(err) {
		return nil
	}
	// A write into a namespace that is being deleted is a 403 too, and it is
	// not about this identity at all. [KubernetesStore.classify] names it.
	if k8serrors.HasStatusCause(err, corev1.NamespaceTerminatingCause) {
		return nil
	}
	policy, named := estateBoundaryNamed(err)
	// The name the real request carried, so the review is the same question
	// RBAC answered. A create is authorized with no name (the name is in the
	// body, not the path), and a resourceNames rule is what a review with the
	// wrong one would get wrong.
	name := ""
	if verb != admissionVerbCreate {
		name = s.SecretName(key)
	}
	allowed, asked := s.authorizerAllowsWrite(ctx, verb, name)
	switch {
	case asked && allowed:
		// The authorizer said yes and the API server said no, so what said
		// no ran after the authorizer: admission.
	case asked:
		// The authorizer's own refusal. #1370's reader tolerance is for
		// exactly this, and nothing here takes it away.
		return nil
	case !named:
		return nil
	}
	return &AdmissionDeniedError{
		Namespace: s.namespace,
		Key:       s.storeKey(key),
		Estate:    s.estate,
		Verb:      verb,
		Policy:    policy,
		Err:       err,
	}
}

// estateBoundaryNamed reports whether the API server named this fork's estate
// boundary policy as what refused the write. It reads Status.Details.Causes,
// which is where a ValidatingAdmissionPolicy's denial puts its message, and
// falls back to the whole message for an API server that puts it elsewhere.
func estateBoundaryNamed(err error) (string, bool) {
	var status k8serrors.APIStatus
	if !errors.As(err, &status) {
		return "", false
	}
	st := status.Status()
	if st.Details != nil {
		for _, c := range st.Details.Causes {
			if strings.Contains(c.Message, EstateBoundaryPolicyName) {
				return EstateBoundaryPolicyName, true
			}
		}
	}
	if strings.Contains(st.Message, EstateBoundaryPolicyName) {
		return EstateBoundaryPolicyName, true
	}
	return "", false
}

// authzAnswer is one cached SelfSubjectAccessReview. asked is false when the
// review could not be made at all, which is not the same as a denial.
type authzAnswer struct{ allowed, asked bool }

// authorizerAllowsWrite asks the API server's own authorizer whether it would
// allow this identity verb on the named Secret in this store's namespace.
//
// Asked once per verb and name and then remembered, for the reason
// [KubernetesStore.namespaceFault] remembers its own answer: a permission does
// not change inside a run, and a write-back whose every write is refused would
// otherwise put one review in front of every record.
func (s *KubernetesStore) authorizerAllowsWrite(ctx context.Context, verb, name string) (allowed, asked bool) {
	if s.clientset == nil {
		return false, false
	}
	cacheKey := verb + "\x00" + name
	if cached, ok := s.writeAuthz.Load(cacheKey); ok {
		a := cached.(authzAnswer)
		return a.allowed, a.asked
	}
	review, err := s.clientset.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authzv1.SelfSubjectAccessReview{
		Spec: authzv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authzv1.ResourceAttributes{
				Namespace: s.namespace,
				Verb:      verb,
				Group:     "",
				Version:   "v1",
				Resource:  "secrets",
				Name:      name,
			},
		},
	}, metav1.CreateOptions{})
	answer := authzAnswer{asked: err == nil}
	if err == nil {
		// Denied is read as well as Allowed: an authorizer can answer both,
		// and an explicit deny that is read as an allow would turn the
		// authorizer's own refusal into an admission one.
		answer.allowed = review.Status.Allowed && !review.Status.Denied
	}
	s.writeAuthz.Store(cacheKey, answer)
	return answer.allowed, answer.asked
}
