// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"errors"
	"io/fs"
	"net/http"
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
	if accessDenied(err) {
		return true
	}
	return errors.Is(err, fs.ErrPermission)
}
