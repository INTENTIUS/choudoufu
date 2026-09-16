// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package servicetags

import "context"

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

	// ReadTags returns the tags carried by the object of typeName whose
	// import identity is importID - the same identifier
	// internal/live/discovery composed for the listed object, so the
	// caller needs no second identity notion.
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
