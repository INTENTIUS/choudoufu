// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"fmt"
	"log"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// SummaryListedNotImportable is GitHub issue #596's refusal: the provider
// listed a live object as this estate's, and then answered that nothing
// exists at the identity its own list call served for it.
const SummaryListedNotImportable = "Live resource listed but not importable"

// refuseListedButAbsent is the guard on [builder.materialize]'s statusAbsent
// branch. It answers one question - has THIS run positively identified a
// live object as this instance's? - and, when the answer is yes, refuses
// instead of letting the ABSENT omission stand and the plan propose a
// create.
//
// # The defect
//
// build.go's statusAbsent branch says "the provider reports no <type> exists
// with identity X, so this resource has not been created yet. The plan will
// propose creating it." The second half is an inference, not a report, and
// there are two situations that produce the same provider answer: the object
// really does not exist, or it exists and the provider cannot import it at
// the identity this run asked with. Applying the plan built from the wrong
// one of those creates a duplicate of live infrastructure.
//
// choudoufu meets this on every plan where stock meets it once. Stock calls
// ImportResourceState at `terraform import` and its state file holds the
// answer forever; [importAndRead] calls it for every instance on every plan,
// so a provider-import defect that costs a stock user one confused afternoon
// is permanent non-convergence here, and it surfaces as a proposed duplicate
// rather than as a refusal. See GitHub issues #572 and #582.
//
// # The discriminator, and why it is this one
//
// "The estate's tag sweep listed it" is NOT proof the object exists: the
// Resource Groups Tagging API keeps deleted ECS clusters and deregistered
// task definitions queryable indefinitely (#578's teardown verification had
// to describe all eight clusters individually rather than trust the tag
// index). A rule built on that would refuse legitimate rebuilds of resources
// that really are gone, which is the opposite failure and just as bad.
//
// [wanted.identity] separates the two, at no cost and with no extra call.
// It is [identity.Resolution.Identity], and across internal/live/discovery
// exactly one construction site ever sets a non-null claimant identity -
// discovery.go's scanTypeCloudControl loop, over the results of the
// provider's own ListResource RPC. Every other source spells it cty.NilVal
// explicitly: the tagging sweep (tagging.go's fileTaggingCandidate), the
// Cloud Control fallback (cloudcontrol.go), content matching
// (contentmatch.go), unique-name matching (uniquename.go) and the located
// fallback (locatedfallback.go). So a non-null identity here means one
// specific thing: the provider's own live enumeration returned this object,
// in this run, carrying this estate's tofu-estate marker and a tofu-address
// marker naming this instance. A tag index cannot produce it, and neither
// can any of the derived paths.
//
// That makes the two situations the issue names distinguishable exactly
// where the danger is provable, and leaves them alone everywhere else: an
// instance whose only sighting was the tag index still takes the ordinary
// ABSENT path, so a genuine rebuild of a genuinely deleted resource is never
// blocked.
//
// # What this deliberately does not cover
//
// A config-identified type whose identity came out of configuration text
// carries no identity object, so it never reaches the refusal. That is
// #572's own aws_ecs_cluster, and it is not an oversight: measured against
// hashicorp/aws 6.59.0, of 1699 managed resource types 195 are listable at
// all and 115 of the 847 marker-capable ones are - aws_ecs_cluster is not
// among them, so no provider list call could sight it even if one were made.
// The only estate-wide sighting available for that population is the tagging
// API, which is the source the counter-argument above disqualifies. See this
// function's tests and the issue for the measurement.
func (b *builder) refuseListedButAbsent(addr addrs.AbsResourceInstance, typeName, importID string, w wanted, declared bool) bool {
	own := b.opts.Ownership
	switch {
	case own == nil || own.Estate == "":
		// No estate concept at all - internal/live/mv reading one resource,
		// or [ReadInstances]' narrow value read. There is no "this estate's
		// marker" for a listing to have carried, so there is nothing here
		// to contradict.
		return false
	case !declared:
		// An undeclared instance is one the estate owns and the
		// configuration no longer declares: absence means the destroy this
		// run would have proposed has already happened, which is the good
		// outcome and never a create.
		return false
	case w.located || w.recordFirst:
		// A record-sourced binding, whose absence has its own answer
		// already: materializeFromRecord falls back to identity.Class's own
		// path rather than reporting anything terminal. Neither ever
		// carries a list-served identity, so this case is unreachable
		// today; it is written down so that a future path which does carry
		// one does not silently acquire this refusal.
		return false
	case w.identity == cty.NilVal || w.identity.IsNull():
		return false
	}

	detail := fmt.Sprintf(
		"The provider's own list call returned a live %s in this run carrying this estate's %s marker and a %s marker naming %s, and the provider has now answered that nothing exists at the import identity %q that same listing served for it. Both answers come from one provider in one run and they contradict each other, so this plan refuses rather than propose creating a second %s alongside the one it just listed. The listing is a live enumeration and not a tag-index lookup, so an object that had already been deleted would not have appeared in it. Two things produce this: the provider cannot import this type at the identity its own list call serves - a defect stock meets once at `terraform import` and caches in its state file, which this fork meets on every plan because it has none (GitHub issues #572, #582) - or the object was destroyed between the two calls, in which case re-running is enough. To disown the listed object instead, remove its %s and %s tags and re-run.",
		typeName, markers.TagEstate, markers.TagAddress, addr, importID, typeName,
		markers.TagEstate, markers.TagAddress,
	)
	cause := fmt.Sprintf("the provider listed a live %s carrying its marker and then reported nothing at that identity.", typeName)

	log.Printf("[WARN] projection: %s was listed live by the provider carrying this estate's marker, and importing it at %q reported absence; refusing rather than proposing a create", addr, importID)

	b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryListedNotImportable, detail))
	b.omit(addr, ReasonListedNotImportable, detail, cause)
	return true
}
