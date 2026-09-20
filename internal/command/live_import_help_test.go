// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"strings"
	"testing"
)

// GitHub issue #1396's first half. live-import migrates a Kubernetes estate
// as well as an AWS one, and its help described only AWS: two markers on
// every resource, a tags argument as the only carrier, and tagging APIs as
// the only thing -parallelism bounds. An operator reading it would conclude
// that no Kubernetes resource can carry a marker at all, which three
// shipped carriers contradict (#1073, #1109).
//
// This pins what the help has to say about the second substrate, and - the
// half that matters more - the three AWS-only sentences it must not go back
// to saying. A test that only looked for the new words would pass with the
// old claims still sitting beside them.
func TestLiveImportHelpDescribesBothSubstrates(t *testing.T) {
	help := (&LiveImportCommand{}).Help()

	for _, want := range []string{
		// The Kubernetes marker is one label and carries no address.
		"one label, tofu-estate, and no address",
		// Where a typed kubernetes_* resource carries it (#1073), and how
		// a kubernetes_manifest gets it (#1109).
		"metadata.labels",
		"kubernetes_manifest",
		"merge patch",
		// -parallelism bounds API server writes too, not tagging calls
		// alone.
		"API server push back",
		// The label-value cap, which is the second defect's user-facing
		// half: an estate name may be longer than a label value.
		"capped at 63",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("live-import's help no longer contains %q, so it does not describe the Kubernetes substrate it migrates", want)
		}
	}

	for _, gone := range []string{
		// Every resource gets both markers.
		"tofu-estate and tofu-address markers onto every resource",
		// A tags argument is the only carrier.
		"whose provider schema carries a tags argument can carry a marker",
		// Tagging APIs are the only thing -parallelism bounds.
		"Lower it if the account's tagging APIs\n                          push back",
	} {
		if strings.Contains(help, gone) {
			t.Errorf("live-import's help says %q again. That is true of an AWS estate and false of a Kubernetes one, which this command also migrates.", gone)
		}
	}
}
