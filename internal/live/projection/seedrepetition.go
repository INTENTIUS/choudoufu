// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/instances"
	"github.com/intentius/choudoufu/internal/live/staticeval"
)

// GitHub issue #1178: the configured seed [builder.prepareRead] hands
// [configuredAttrsSeed] and [configuredTagsSeed] was always built with the
// bare MODULE-level evaluator, which by contract refuses every
// count.index, each.key and each.value reference
// ([configs.StaticEvaluator.WithRepetitionData]'s doc comment says why: a
// plain static evaluation has no notion of a resource instance at all). So
// for an EXPANDED block, any argument whose expression mentions the
// instance's own repetition value simply never made it into the seed - the
// whole argument, not just the count.index inside it, because
// [configs.StaticEvaluator.EvalContext] fails outright the moment one
// reference it was asked to resolve cannot be.
//
// On nearly every type that gap was invisible: the seed is an INPUT to
// [importAndRead], applied to the import stub before ReadResource, and the
// provider's own read overwrites whatever it sources from the live system,
// so a missing seed costs nothing wherever the provider answers for the
// attribute itself.
//
// kubernetes_manifest is where it stops being invisible, and #1178 is the
// report. Its `manifest` argument is the DESIRED object, which no read ever
// answers for - only the computed `object` beside it is read back - so the
// seed is the only thing that can put a manifest in the projected prior.
// The estate's marker lives inside it, at manifest.metadata.labels
// ([markers.ManifestLabelsOf], which [markerSurface.markersOf] reads for
// [surfaceManifest]), so an unseeded manifest reads as an object carrying
// no marker map at all. [builder.checkOwnership] then refuses the instance
// as UNOWNED, the plan proposes CREATING an object choudoufu itself applied
// one command earlier, and the estate sweep - which joins on the natural
// key, not on this - reports that same live object as an undeclared orphan.
// One plan, both halves, exactly as #1178 describes. An UNCOUNTED manifest
// block was never affected, because the module-level evaluator answers its
// arguments in full.
//
// seedRepetition closes the gap at its source: the instance key is the
// authority on which repetition value this instance's own arguments were
// written against, and it is the same authority
// internal/live/dataread's repetitionFor and
// [internal/live/projection/plan.go]'s planCounted already use.
//
//   - an [addrs.IntKey] is count expansion and nothing else, so the key IS
//     count.index;
//   - an [addrs.StringKey] is for_each expansion, so the key IS each.key,
//     and each.value is read from the block's own for_each expression when
//     configuration alone can evaluate it ([staticeval.ForEachElements],
//     the same helper internal/live/discovery uses to build one instance's
//     scope).
//
// A field left at cty.NilVal means "not known for this instance", which
// behaves exactly as an evaluator with no repetition data at all: the
// reference refuses and the argument is not seeded, which is today's
// behaviour for every instance. So this can widen what is seeded and never
// change a value that was already being seeded - an each.value the for_each
// expression does not statically evaluate stays unseeded rather than
// becoming a guess.
func seedRepetition(ctx context.Context, mod *configs.Module, rc *configs.Resource, key addrs.InstanceKey) (instances.RepetitionData, bool) {
	switch k := key.(type) {
	case addrs.IntKey:
		if rc == nil || rc.Count == nil {
			// An IntKey on a block with no count expression is not a shape
			// this package produces. Refusing it keeps the rule "the key is
			// the authority" true of the CONFIGURATION as well as the
			// address, rather than inventing a count.index for a block that
			// never asked for one.
			return instances.RepetitionData{}, false
		}
		return instances.RepetitionData{CountIndex: cty.NumberIntVal(int64(k))}, true
	case addrs.StringKey:
		if rc == nil || rc.ForEach == nil {
			return instances.RepetitionData{}, false
		}
		rd := instances.RepetitionData{EachKey: cty.StringVal(string(k))}
		if elems, ok := staticeval.ForEachElements(ctx, mod, rc.ForEach); ok {
			if val, has := elems[string(k)]; has {
				rd.EachValue = val
			}
		}
		return rd, true
	}
	return instances.RepetitionData{}, false
}
