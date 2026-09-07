// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"fmt"
	"strings"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/lint"
)

// UnmarkedDiscoveryDetail is the sentence that makes an unstamped
// marker-discovered resource an error rather than a warning.
//
// The severity is the same for every cause and is not this function's to
// decide: an untaggable resource whose identity this run cannot compute is
// unfindable afterwards however it got that way. What differs is what the
// operator can do about it, and that is what the cause selects. Three of the
// four causes have a next step, and before [identity.DiscoveryCause] existed
// all four were reported with the first one's sentence - which asserted "the
// provider assigns this identity at create time" over resources whose table
// row is not ServerAssigned at all, and offered no step to the two thirds of
// affected configurations that had one.
//
// It is exported because more than one caller renders it, and the callers
// saying different things is how the fork's own documentation came to
// describe a guarantee it did not hold (#111). Today those callers are
// internal/live/check's node-path LayerStamp report (nodestamp.go, which
// appends this sentence to its [SummaryUnmarkedApply] diagnostic) and
// tools/refusal-probe, which renders it per cause to classify a corpus
// site. GitHub issue #644 removed the third, this package's own HCL
// rewrite, along with the rest of that engine.
func UnmarkedDiscoveryDetail(addr addrs.ConfigResource, disco identity.BlockDiscovery) string {
	const lost = "Applying it unmarked " + lint.UnfindableClause

	arg := func(i int) string {
		if i < len(disco.Args) {
			return disco.Args[i]
		}
		return ""
	}

	switch disco.Cause.Normalize() {
	case identity.DiscoveryCloudUnknown:
		prop := identity.CloudValue(arg(0)).Describe()
		if prop == "" {
			prop = "a property of the cloud it is pointed at"
		}
		// Entries past the first are the arguments the provider documents
		// as defaulting to that cloud property, so setting one is a real
		// next step and this cause is not the dead end the single sentence
		// below implies. See [identity.DiscoveryCloudUnknown]; before #250
		// the subjects stopped at the property and the sentence could not
		// name catalog_id even where catalog_id was the entire fix.
		if len(disco.Args) > 1 {
			return fmt.Sprintf(
				"%s has an identity this configuration would determine on its own except for the %s, which is a property of the cloud this run is pointed at and which nothing has told this run. The ownership marker is the only handle left. Setting %s in the resource block names the same thing from the configuration, which makes the identity computable and needs no marker at all; leaving it out and applying "+lint.UnfindableClause,
				addr, prop, orListBare(disco.Args[1:]))
		}
		return fmt.Sprintf(
			"%s has an identity this configuration would determine on its own except for the %s, which is a property of the cloud this run is pointed at and which nothing has told this run. The ownership marker is the only handle left. %s",
			addr, prop, lost)

	case identity.DiscoveryNameOmitted:
		if name := arg(0); name != "" {
			return fmt.Sprintf(
				"%s sets no %s, and the provider invents one at create time when it is omitted, so this run cannot say what the object will be called. The ownership marker is the only handle left. Setting %s to a value this configuration chooses makes the identity computable and needs no marker at all; leaving it out and applying "+lint.UnfindableClause,
				addr, name, name)
		}

	case identity.DiscoverySiblingApply:
		// CauseArgs[0] is the sibling block, the rest are the arguments
		// waiting on it. See [identity.DiscoverySiblingApply]. The next step
		// this offers is unlike every other cause's: it is not an edit to the
		// configuration, it is the order the two resources are applied in,
		// and the configuration is already correct as written.
		if sibling := arg(0); sibling != "" {
			waiting := "one of its identity arguments"
			if len(disco.Args) > 1 {
				waiting = orListBare(disco.Args[1:])
			}
			return fmt.Sprintf(
				"%s takes %s from %s, which the provider does not fill in until %s has been applied, so this run cannot say what the object will be called. The ownership marker is the only handle left. Applying %s first makes the identity computable from a read of it and needs no marker at all; applying both together and "+lint.UnfindableClause,
				addr, waiting, sibling, sibling, sibling)
		}

	case identity.DiscoveryNamePrefix:
		if base, prefix := arg(0), arg(1); base != "" && prefix != "" {
			return fmt.Sprintf(
				"%s is named through %s, so the provider appends a random suffix at create time and this run cannot say what the object will be called. The ownership marker is the only handle left. Naming it with %s instead of %s makes the identity computable and needs no marker at all; applying as written "+lint.UnfindableClause,
				addr, prefix, base, prefix)
		}

	case identity.DiscoveryMarkerFallback:
		// Unlike every case above, there is no configuration edit to
		// offer: whatever this instance's own expression does - a
		// sensitive value, a circular reference, an argument left unset -
		// is a fact about THIS instance, not a defect a rewrite is
		// guaranteed to fix, and CauseArgs carries none of the specific
		// shapes [resolver.markerFallback] answers. See
		// [identity.DiscoveryMarkerFallback].
		return fmt.Sprintf(
			"%s's identity does not fold from its own configuration, but the type is tagged and listable, so a migrated estate's ownership marker is the only thing any later run can find it by. %s",
			addr, lost)
	}

	// DiscoveryServerAssigned, and every cause whose subjects did not
	// arrive: the wording every cause carried before they were told apart.
	// There is no configuration edit that resolves a server-assigned
	// identity, so this sentence deliberately offers no next step.
	return fmt.Sprintf(
		"%s has an identity the provider assigns at create time, so the ownership marker is the only thing any later run can find it by. %s",
		addr, lost)
}

// orListBare joins argument names the way the other sentences in
// [UnmarkedDiscoveryDetail] name a single one: unquoted, because the
// surrounding prose already reads as a reference to an argument, and with
// "or" rather than "and" because any one of them settles the component.
// Callers only reach it with a non-empty slice.
func orListBare(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	case 2:
		return names[0] + " or " + names[1]
	default:
		return strings.Join(names[:len(names)-1], ", ") + ", or " + names[len(names)-1]
	}
}
