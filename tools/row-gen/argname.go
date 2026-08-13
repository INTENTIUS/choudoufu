// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"regexp"
	"strings"
)

// The two-pass camel-to-snake conversion: split before a capital that starts
// a lowercase run ("FunctionName" -> "Function_Name"), then split between a
// lowercase-or-digit and a capital that was not already split
// ("IPv6" -> "IP_v6" from the first pass alone would miss the v; this pass
// catches the boundary the first one does not). Standard Go idiom, not
// specific to this package.
var (
	matchFirstCap = regexp.MustCompile("(.)([A-Z][a-z]+)")
	matchAllCap   = regexp.MustCompile("([a-z0-9])([A-Z])")
)

// snakeCase converts a CFN PascalCase property name to a guessed TF argument
// name ("FunctionName" -> "function_name"). It is only ever used as the
// last-resort argument-name source, flagged GUESSED, because a CFN property
// name and a TF argument name are two different providers' vocabularies that
// happen to often agree - not a fact this function can verify.
func snakeCase(s string) string {
	s = matchFirstCap.ReplaceAllString(s, "${1}_${2}")
	s = matchAllCap.ReplaceAllString(s, "${1}_${2}")
	return strings.ToLower(s)
}
