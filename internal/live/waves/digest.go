// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package waves

import "github.com/intentius/choudoufu/internal/live/setdigest"

// The set digest, from internal/live/setdigest. See the package doc.
type (
	RootPlan        = setdigest.RootPlan
	SetDocument     = setdigest.SetDocument
	RootDigestEntry = setdigest.RootDigestEntry
)

const (
	DigestPrefix  = setdigest.DigestPrefix
	StatusPlanned = setdigest.StatusPlanned
)

var (
	ParseSetDocument = setdigest.ParseSetDocument
	RootDigest       = setdigest.RootDigest
	SetDigest        = setdigest.SetDigest
	DocumentDigests  = setdigest.DocumentDigests
)
