// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package servicetags

import (
	"context"
	"errors"
	"strings"

	"github.com/aws/smithy-go"
)

// Reader reads one live object's tags through the service's own tag API.
//
// The contract is deliberately narrow, because the caller's safety rule is
// narrow: a tag read either produced the object's real tags or it did not,
// and "did not" must never be turned into "the object carries no marker".
// So [Reader.ReadTags] returns an error for every outcome that is not a
// successful read, including an object that has gone away between the
// listing and the read, and the caller keeps whatever refusal it already
// had.
type Reader interface {
	// Route reports whether this reader has a tag-read operation for
	// typeName. A caller asks before it pays for anything else, so that a
	// type with no route costs nothing at all.
	Route(typeName string) bool

	// Action is the IAM action name [ReadTags] needs for typeName, in the
	// form a policy statement names it ("iam:ListRoleTags"), and "" for a
	// type [Route] answers false for. A refused read's diagnostic names it
	// as the thing to grant, so it comes from the route table and is never
	// typed by a caller.
	Action(typeName string) string

	// ReadTags returns the tags carried by the object of typeName whose
	// import identity is importID - the same identifier
	// internal/live/discovery composed for the listed object, so the
	// caller needs no second identity notion. For an object a [Lister]
	// enumerated the identifier is [Listed.ReadKey] instead, which is the
	// import identity except where the service's tag API keys on
	// something else (GitHub issue #1477: a service-linked role imports
	// by ARN and iam:ListRoleTags takes its name).
	//
	// A nil error means the read succeeded and the map is the object's
	// whole tag set, which may legitimately be empty: an object with no
	// tags is an ordinary object, and is not the same fact as an object
	// whose tags could not be read.
	//
	// An error means nothing was established. [ErrNoRoute] is the one
	// error a caller may want to distinguish, for a typeName [Reader.Route]
	// answers false for.
	ReadTags(ctx context.Context, typeName, importID string) (map[string]string, error)
}

// ErrNoRoute is what [Reader.ReadTags] returns for a type the reader has no
// tag-read operation for. It is an error and not an empty map, for the
// reason the interface's own comment gives: an unread object must never
// read as an untagged one.
type noRouteError struct{ typeName string }

func (e noRouteError) Error() string {
	return "no service tag-read operation is wired for " + e.typeName
}

// ErrNoRoute reports whether err is the "no tag-read operation for this
// type" error, for a caller that wants to tell it apart from a read that
// was attempted and failed.
func ErrNoRoute(err error) bool {
	_, ok := err.(noRouteError)
	return ok
}

// ErrorCode is the API error code carried by a failed [Reader.ReadTags],
// for a diagnostic that names it: "AccessDenied", "Throttling". An AWS SDK
// error carries it as smithy.APIError's ErrorCode. For any other error the
// text before the first colon is taken when it is a single token, which is
// how the SDK's own messages and this repository's test fakes both spell
// one ("AccessDenied: User is not authorized ..."); otherwise the whole
// message stands, so an error is never reported as an empty code.
func ErrorCode(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && apiErr.ErrorCode() != "" {
		return apiErr.ErrorCode()
	}
	msg := strings.TrimSpace(err.Error())
	if head, _, ok := strings.Cut(msg, ":"); ok && head != "" && !strings.ContainsAny(head, " \t") {
		return head
	}
	return msg
}
