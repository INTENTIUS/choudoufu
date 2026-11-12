// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package cloudcontrol

import (
	"errors"
	"fmt"
)

// The error codes Cloud Control sends that a caller of this package needs to
// tell apart. There are more codes than these in the API, but these are the
// ones the discovery layer branches on: UnsupportedOperation is floci's
// answer for GetResource on some types while ListResources works for the
// same type, the next three separate "this type does not exist here" from
// "the request was malformed" from "slow down", and AccessDenied is the
// caller's own credential being refused the read a type's list handler
// makes (GitHub issue #1052) - the one failure a sweep over hundreds of
// types reports as a group rather than one type at a time. The last two
// below are list refusals; see [IsListRefusalCode].
const (
	CodeUnsupportedOperation  = "UnsupportedOperation"
	CodeResourceNotFoundError = "ResourceNotFoundException"
	CodeValidationError       = "ValidationException"
	CodeThrottlingError       = "ThrottlingException"
	CodeAccessDenied          = "AccessDeniedException"

	// CodeUnsupportedAction is ListResources declining a type outright:
	// real AWS Cloud Control answers it for a type whose handler does not
	// support the action, and floci answers it from lex00/floci 5cf10ece1
	// (upstream #3141, first pinned at 920d475) for a type its read side
	// does not enumerate, where earlier pins returned an empty list.
	CodeUnsupportedAction = "UnsupportedActionException"
	// CodeUnknownOperation is an emulator's router not knowing the
	// operation at all.
	CodeUnknownOperation = "UnknownOperationException"
)

// IsListRefusalCode reports whether a ListResources error code means the
// endpoint declined to list the type at all, as opposed to failing to list
// it this time: [CodeUnsupportedAction], [CodeUnsupportedOperation] or
// [CodeUnknownOperation]. A refusal is a fact about the type at this
// endpoint, the same on every call, so a caller treats it as "this source
// cannot list the type" and never as "nothing of the type exists".
//
// tools/floci-capability-gen classifies its probe rows with this same
// predicate, so a type the manifest records as refused is exactly a type
// discovery treats as refused.
func IsListRefusalCode(code string) bool {
	switch code {
	case CodeUnsupportedAction, CodeUnsupportedOperation, CodeUnknownOperation:
		return true
	}
	return false
}

// IsListRefusal is [IsListRefusalCode] for an error from ListResources.
func IsListRefusal(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && IsListRefusalCode(apiErr.Code)
}

// APIError is a failed Cloud Control call, carrying the HTTP status and the
// API's own error code (its "__type", stripped of the shape-ID prefix Cloud
// Control puts in front of it) so a caller classifies the failure without
// parsing Message.
//
// Code is empty when the response carried no __type at all — an HTTP
// failure Cloud Control did not explain, or a response this client could
// not parse as JSON.
type APIError struct {
	// Op is the operation that failed: "ListResources" or "GetResource".
	Op string
	// StatusCode is the response's HTTP status.
	StatusCode int
	// Code is the API's error code, e.g. CodeResourceNotFoundError.
	Code string
	// Message is the API's own explanation, when it sent one.
	Message string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("cloudcontrol: %s: %s (HTTP %d): %s", e.Op, e.Code, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("cloudcontrol: %s: HTTP %d: %s", e.Op, e.StatusCode, e.Message)
}

// HasCode reports whether err is (or wraps) an *APIError whose Code is
// code, so a caller can write
//
//	if cloudcontrol.HasCode(err, cloudcontrol.CodeUnsupportedOperation) { ... }
//
// instead of its own errors.As boilerplate for the common case of checking
// one specific code.
func HasCode(err error, code string) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Code == code
}
