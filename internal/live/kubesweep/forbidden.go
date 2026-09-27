// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package kubesweep

import (
	"regexp"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// GitHub issue #1582. A least-privilege role that cannot `list` one kind
// the estate sweep asks for gets Forbidden (403), and until this the sweep
// turned that into the same LIST_FAILED gap as a throttled call or an
// unreachable cluster: no verb, no resource, no hint that the fix is a
// grant rather than a retry. AWS already names the IAM action a denial
// needs (sweepdenied.go, issues #1052/#1513); this is the same class for
// the other substrate.
//
// The RBAC authorizer's own Forbidden message already names what was
// refused - "User \"x\" cannot list resource \"secrets\" in API group \"\"
// at the cluster scope" or "... in the namespace \"default\"" - so
// [Forbidden] reads it back rather than asking the caller to reconstruct
// it from the request it made. A message this does not recognise still
// classes as forbidden (Detail's fields are then empty; the caller falls
// back to the raw error).

// ForbiddenDetail is what the API server's own Forbidden message says was
// refused.
type ForbiddenDetail struct {
	// Verb is the RBAC verb denied ("list").
	Verb string
	// Resource is the API resource name denied, plural and lower-case as
	// RBAC spells it ("secrets", "configmaps").
	Resource string
	// Namespace is the namespace the check was scoped to, or "" for a
	// cluster-scoped check - which is what an estate sweep's cluster-wide
	// list (no namespace on the request) asks the authorizer for.
	Namespace string
}

// forbiddenRE matches the RBAC authorizer's own wording
// (k8s.io/apiserver/pkg/authorization/authorizer), which every Forbidden
// this package sees was built from by k8s.io/apiserver's
// errors.NewForbidden.
var forbiddenRE = regexp.MustCompile(`cannot (\S+) resource "([^"]+)" in API group "[^"]*"(?: in the namespace "([^"]+)"| at the cluster scope)?`)

// Forbidden reports whether err is the API server refusing a request as
// RBAC-forbidden (HTTP 403), and what its own message says was refused.
// False for anything else, including a cluster that never answered
// (unreachable) or answered but would not authenticate the credential at
// all (401, [Credentials.Diagnose]'s CauseRejected) - a 403 is the one
// case where the cluster's own words name the fix better than any
// wrapper here could.
func Forbidden(err error) (ForbiddenDetail, bool) {
	if err == nil || !apierrors.IsForbidden(err) {
		return ForbiddenDetail{}, false
	}
	m := forbiddenRE.FindStringSubmatch(err.Error())
	if m == nil {
		return ForbiddenDetail{}, true
	}
	return ForbiddenDetail{Verb: m[1], Resource: m[2], Namespace: m[3]}, true
}
