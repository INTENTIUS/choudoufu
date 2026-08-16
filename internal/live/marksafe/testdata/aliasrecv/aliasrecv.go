// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package aliasrecv is a fixture for TestReceiverIndexFollowsAliasAndEmbedding.
// It lives under testdata so the go tool's "..." patterns skip it: it must be
// type-checked only by the test that names it, never swept as a live package.
//
// Both functions here call cty.Value.AsString, which panics on a marked
// receiver, but write a receiver whose SPELLED type is not cty.Value.
package aliasrecv

import "github.com/zclconf/go-cty/cty"

// Alias is cty.Value under another name. A method call on it is
// cty.Value's own method.
type Alias = cty.Value

// Wrapped promotes every cty.Value method, each running against the
// embedded cty.Value.
type Wrapped struct {
	cty.Value
}

func ViaAlias(v Alias) string { return v.AsString() }

func ViaEmbed(w Wrapped) string { return w.AsString() }

func Direct(v cty.Value) string { return v.AsString() }
