// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"errors"
	"io/fs"
	"net/http"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
)

// accessDenied reports whether err is S3 refusing the request by policy:
// one of the three codes AWS uses for it, or a bare 403 from a store that
// sent no code this package recognises.
//
// It is the classification [settingReadFailure] has always made on the
// bucket contract's reads, factored out so the store's own operations can
// make the same one and cannot drift from it.
func accessDenied(err error) bool {
	switch apiErrorCode(err) {
	case "AccessDenied", "AccessDeniedException", "Forbidden":
		return true
	}
	status, ok := httpStatus(err)
	return ok && status == http.StatusForbidden
}

// IsAccessDenied reports whether err is this run's own identity being
// refused permission by the store, as opposed to the store failing, being
// unreachable, or answering something about the record itself.
//
// It covers both backends, because the distinction a caller draws on it is
// about the run's credentials and not about which backend carries them.
// [S3Store] surfaces a refusal as AccessDenied or a bare 403; [LocalStore]
// surfaces one as the filesystem's EACCES, which reaches here as
// fs.ErrPermission through whichever os call met it first. A directory an
// operator mounted or chmodded read-only is the local equivalent of a role
// with s3:GetObject and no s3:PutObject, and a caller that tolerated one
// and not the other would be making a distinction neither backend's users
// would recognise.
//
// A KMS refusal is deliberately NOT one of these, although S3 relays it as
// AccessDenied and a 403. [KMSDeniedError] and [KMSKeyUnusableError] are
// the bucket refusing the run outright: every object in the store is
// unreadable while either lasts, so a caller that read one as "this
// identity may read but not write" would carry on with a store it cannot
// read at all. GitHub issue #1376 makes both of those refusals, and #1370's
// reader tolerance must not undo that.
func IsAccessDenied(err error) bool {
	var denied *KMSDeniedError
	var unusable *KMSKeyUnusableError
	if errors.As(err, &denied) || errors.As(err, &unusable) {
		return false
	}
	// An admission denial is deliberately NOT one of these either, for the
	// reason the KMS refusals are not. It is a 403 the cluster's own policy
	// raised about a write the authorizer had already allowed, so a caller
	// that read it as "this identity may read but not write" would carry on
	// with a store that will refuse every record it writes. GitHub issue
	// #1448 measured that on kind: the fenced identity opened the store
	// green and was refused mid-apply. See [AdmissionDeniedError].
	var admission *AdmissionDeniedError
	if errors.As(err, &admission) {
		return false
	}
	if accessDenied(err) {
		return true
	}
	// [KubernetesStore] surfaces a refusal as the API server's own 403,
	// which arrives as a *k8serrors.StatusError with reason Forbidden and
	// carries none of the smithy shapes accessDenied looks for. Without this
	// leg the reader tolerance #1370 built for the bucket reached neither
	// the Kubernetes store nor a plan against it: a CI plan identity with
	// get and list on the records namespace and no create had its sentinel
	// write read as an outage and the run stopped. GitHub issue #1393
	// measured that on kind before this line existed.
	if k8serrors.IsForbidden(err) {
		return true
	}
	return errors.Is(err, fs.ErrPermission)
}
