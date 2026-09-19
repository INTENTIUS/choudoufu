// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"net/url"
	"sort"
	"strings"
)

// Object tags. GitHub issue #1337, part of the bucket backend (#1332).
//
// Every object the bucket-backed store writes carries the estate's own
// markers, the same pair every managed resource carries. In descending order
// of how much it matters: it lets an IAM policy condition on the tag, which
// is half of the estate-isolation model; it makes the bucket say which estate
// owns an object without anyone knowing the key layout; and it keeps the
// store's own objects from being the exception to the product's thesis that
// the tag on the object is the record of ownership.
//
// NOT discovery. Objects are found by LIST on a known bucket, and the
// Resource Groups Tagging API does not index S3 objects.
//
// # Why the tags travel in the context
//
// [Store] is keys and payloads, and three backends implement it. Only one
// has anywhere to put a tag, and the caller that knows the address is two
// wrappers above it. A context value reaches the one backend that wants it
// without widening an interface the other two would have to ignore, and
// without this package deriving an address back out of a key - which would
// be the second derivation #1337 rules out.

type objectTagsKey struct{}

// WithObjectTags returns ctx carrying tags for the next write made with it.
// A backend with nowhere to put tags ignores them. Tags already in ctx are
// kept, with the new ones winning on a shared key.
func WithObjectTags(ctx context.Context, tags map[string]string) context.Context {
	merged := map[string]string{}
	for k, v := range ObjectTags(ctx) {
		merged[k] = v
	}
	for k, v := range tags {
		merged[k] = v
	}
	return context.WithValue(ctx, objectTagsKey{}, merged)
}

// ObjectTags is what [WithObjectTags] put in ctx, nil when nothing did.
func ObjectTags(ctx context.Context) map[string]string {
	tags, _ := ctx.Value(objectTagsKey{}).(map[string]string)
	return tags
}

// encodeObjectTagging renders tag sets as PutObject's Tagging value, which
// is a URL query string. Later sets win on a shared key. Keys are sorted so
// the header is the same bytes for the same tags.
func encodeObjectTagging(sets ...map[string]string) string {
	merged := map[string]string{}
	for _, set := range sets {
		for k, v := range set {
			merged[k] = v
		}
	}
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(merged[k]))
	}
	return strings.Join(parts, "&")
}
