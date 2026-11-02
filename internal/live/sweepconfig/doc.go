// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package sweepconfig is what choudoufu hands the public, engine-neutral
// sweeps under pkg/ (GitHub issue #2281): its marker vocabulary
// (tofu-estate, tofu-address), and the credential and HTTP client each
// cloud's sweep calls with, built from a provider block the way the
// provider builds its own.
//
// The packages under pkg/ build no credential. This one does, for
// choudoufu's callers only, with one rule on top of the providers' chains:
// a block pointed at local endpoints (an emulator run) never authenticates
// through a credential whose token would come from a public cloud. For GCP
// that is [gcpsweep.HTTPClient]'s rule (no credentials file, no
// Application Default Credentials); for Azure it is [AzureCredential]'s (no
// Azure CLI, managed identity or workload identity, and the authority host
// local too). Against a real cloud both chains are what they were.
package sweepconfig

import (
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/pkg/markerkeys"
)

// Markers is choudoufu's marker vocabulary as the public sweeps take it.
// Values are carried as given: choudoufu's callers already pass the
// spelling each label holds (markers.GoogleAddressValue for an address on
// GCP).
var Markers = markerkeys.Keys{Owner: markers.TagEstate, Address: markers.TagAddress}
