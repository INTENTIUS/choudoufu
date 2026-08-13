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
// tell apart. There are more codes than these in the API, but these four are
// the ones the discovery layer branches on: UnsupportedOperation is floci's
// answer for GetResource on some types while ListResources works for the
// same type, and the other three separate "this type does not exist here"
// from "the request was malformed" from "slow down".
const (
	CodeUnsupportedOperation  = "UnsupportedOperation"
	CodeResourceNotFoundError = "ResourceNotFoundException"
	CodeValidationError       = "ValidationException"
	CodeThrottlingError       = "ThrottlingException"
)

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
