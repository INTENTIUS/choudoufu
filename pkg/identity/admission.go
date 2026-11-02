// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// Admission says what admits a type to the identity table with no
// hand-written row: the provider's schemas on their own, or the schemas
// plus a configuration that answers the one question they leave open.
type Admission string

const (
	// AdmitSchema: the strict rule settles it. Every attribute the provider
	// requires for import is a *required* argument of the type, so no
	// configuration can decline to name the object. See [Derivable].
	AdmitSchema Admission = "schema"

	// AdmitConfigSignal: the schemas leave it open and a configuration
	// closes it. Every attribute required for import is a settable argument
	// of the type, and every instance in the configuration sets every one
	// of them. See [ConfigSignal].
	AdmitConfigSignal Admission = "config-signal"

	// AdmitNeedsConfigSignal: the schemas leave it open and no
	// configuration here answers. The type is a candidate, and the
	// candidate's terms are known: a configuration that sets these
	// arguments admits it, one that does not leaves it to discovery.
	AdmitNeedsConfigSignal Admission = "needs-config-signal"

	// AdmitConfigDeclined: the schemas leave it open and the configuration
	// says no. Some instance sets none of the identity arguments, or only
	// some of them, so the cloud names these objects and marker discovery
	// is what finds them.
	AdmitConfigDeclined Admission = "config-declined"
)
