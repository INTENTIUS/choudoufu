// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/strict"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is the record store answering a THIRD question (issue #275).
//
// record.go answers "does this thing exist at all", for a logical resource
// with no cloud object: the record IS the object, so the record namespace
// is enumerated and a key with no configuration behind it is proposed for
// destruction.
//
// located.go answers "which object is this", for a cloud object with
// nowhere to carry a marker. Its namespace is never enumerated, because an
// identity is not a permission to delete.
//
// This file answers "what did we send", for a cloud object that has a
// marker, has a perfectly good identity, and some of whose ARGUMENTS the
// provider's Read never gives back. aws_lambda_function.filename is the
// founding case: the local path a zip came from is not a thing AWS ever
// knew, so no amount of reading recovers it. Stock OpenTofu tolerates this
// because the state file remembers what was sent; issue #73 deletes the
// state file, so a cold replan re-derives the object from the cloud alone,
// finds nothing for those arguments, and proposes the identical update
// forever. Applying it does not settle it. That is not drift and it is not
// an emulator gap - it never converges.
//
// The namespace root is a fifth literal beside "tofu-records",
// "tofu-receipts", "tofu-hints" and "tofu-located", and this store is a
// point-lookup type with no List, for exactly located.go's reason: a
// residue key names attributes of a live cloud object, and builder.
// discoverOrphanedRecords materializes every undeclared key it finds under
// the RECORD prefix so the plan proposes destroying it. A residue key that
// listing could reach would be a cloud deletion driven by a note about a
// filename. internal/configs' validateRecordStoreKeyPrefix refuses an
// operator override rooted here, and [decodeRecordPayload] refuses this
// payload shape outright, so the three steps a careless caller would have
// to take are each independently stopped - the same construction
// [LocatedStore] documents at length.
//
// # What is stored, and why it is not the configuration
//
// The value stored is the value the APPLY produced, never the value the
// configuration declares. Filling prior state from the configuration would
// make every argument agree with itself by construction, which suppresses
// the one case that must still plan: someone rebuilds check_links.py.zip,
// source_code_hash changes in the configuration, and the plan has to
// propose sending the new zip. The record holds what was sent, so that
// comparison still has two sides.
//
// # What is NOT stored, and why the classifier is behavioural
//
// There is no schema-level rule for this population, and that was measured
// rather than assumed (issue #275, against hashicorp/aws 6.59.0): the only
// schema field meaning "never returned by a read" is WriteOnly, and every
// type carrying one is credential material already excluded. filename is
// bit-for-bit identical in the schema to description, memory_size, runtime,
// handler and s3_bucket, all of which the provider DOES return.
// aws_route53_record.allow_overwrite carries no schema description at all,
// so the doc prose says nothing either.
//
// So the classifier asks the provider instead, with two reads whose priors
// differ in exactly the attributes under test - see [classifyResidue].

// residueAttrValue is one recorded attribute: the cty type it was recorded
// at, and its value at that type, both in cty's own JSON encoding.
//
// The type travels with the value for [objectFields]'s reason - a value
// with no type is not decodable - but PER ATTRIBUTE rather than once for
// the object, because a residue record is deliberately a handful of named
// attributes and not a copy of the object. A whole-object copy is what
// issue #73 removes; recording one is how this mechanism would quietly
// become a second state file.
type residueAttrValue struct {
	Type  json.RawMessage `json:"attrType"`
	Value json.RawMessage `json:"attrValue"`
}

// encodeResidueFields turns a classified attribute map into the
// [residueFields] a [recordEnvelope]'s Residue member holds, applying the
// same validation [RecordResidueForInstance] and the write-back residue
// path both need: nothing null, nothing unknown, nothing marked - see each
// check's own comment below for why a silent skip would be worse than a
// refusal.
func encodeResidueFields(attrs map[string]cty.Value) (*residueFields, error) {
	if len(attrs) == 0 {
		return nil, fmt.Errorf("refusing to record an empty residue")
	}
	out := &residueFields{Attributes: make(map[string]residueAttrValue, len(attrs))}
	for _, name := range sortedNames(attrs) {
		val := attrs[name]
		if val.IsNull() || !val.IsWhollyKnown() {
			// Neither is storable and neither should have got here: a null
			// carries nothing and an unknown is a plan-time placeholder,
			// not an applied value. Refusing rather than skipping, because
			// a silent skip here writes a record that is missing exactly
			// the attribute the caller believed it had stored.
			return nil, fmt.Errorf("refusing to record attribute %q: its applied value is null or not wholly known", name)
		}
		if val.IsMarked() {
			// Everything this store holds is held unmarked - ctyjson
			// cannot encode a marked value - and the sensitivity is
			// reconstructed from the schema on the way out (see
			// [builder.fillResidueFor]). So a marked value reaching here
			// is a caller that skipped the unmarking, not a policy
			// question, and it is refused under either secrets setting.
			return nil, fmt.Errorf("refusing to record attribute %q: its value is marked sensitive", name)
		}
		ty := val.Type()
		tyRaw, err := ctyjson.MarshalType(ty)
		if err != nil {
			return nil, fmt.Errorf("encoding the type of attribute %q: %w", name, err)
		}
		valRaw, err := ctyjson.Marshal(val, ty)
		if err != nil {
			return nil, fmt.Errorf("encoding attribute %q: %w", name, err)
		}
		out.Attributes[name] = residueAttrValue{Type: tyRaw, Value: valRaw}
	}
	return out, nil
}

func sortedNames(m map[string]cty.Value) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// fillResidueFor is [builder.materialize]'s front door for issue #275: it
// reads addr's residue record, if any, and fills the attributes the
// provider left null from it, in place on obj.
//
// It is called AFTER the ownership check, which is a rule and not an
// ordering accident - see the call site.
//
// importStub is the object [importAndRead] handed ReadResource as
// PriorState, before any live read happened - GitHub issue #393's
// provenance signal. See [fillResidue]'s doc comment for what it is used
// for; it is passed through unread whenever obj did not come from
// [importAndRead] at all, which fillResidue treats the same as never
// having it.
//
// Every failure here is a WARNING and never an error, and the run continues
// with the object exactly as the provider returned it. That is the whole
// difference in stakes between this store and the other two: a located
// record that cannot be read means an instance is bound to no object or to
// the wrong one, so it stops the run; a residue record that cannot be read
// means the estate proposes an update it has proposed on every run since it
// was written. The second is annoying and completely visible. Stopping a
// run over it would be trading a visible nuisance for an outage.
func (b *builder) fillResidueFor(ctx context.Context, addr addrs.AbsResourceInstance, schema providers.Schema, obj *states.ResourceInstanceObject, importStub cty.Value) {
	if b.opts.RecordStore == nil || obj == nil || schema.Block == nil {
		return
	}
	attrs, version, keyExists, residueFound, err := b.opts.RecordStore.GetResidue(ctx, addr)
	if err != nil {
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryResidueUnreadable, fmt.Sprintf(
			"The residue record for %s could not be read: %s. The plan continues from what the provider returned, so any argument the provider does not give back will be proposed for update again on this run and every later one. Nothing was changed and nothing was written.",
			addr, err,
		)))
		return
	}
	if keyExists {
		b.recordEnvelopeVersion(addr, version)
	}
	if !residueFound {
		return
	}

	secrets := identity.SecretsFor(b.cfg)
	filled, n := fillResidue(obj.Value, schema.Block, attrs, secrets, importStub)
	if pathFilled, pn := fillResiduePaths(filled, schema.Block, attrs, secrets); pn > 0 {
		filled, n = pathFilled, n+pn
	}
	if n == 0 {
		return
	}

	// The sensitivity a filled value lost on the way into the store, put
	// back from the provider's own schema.
	//
	// Everything in a residue record is stored unmarked - ctyjson cannot
	// encode a marked value and the plugin channel cannot carry one - and
	// [residueMarkRecoverable] is what guarantees this restores exactly what
	// was taken: a value only becomes a candidate when its marks are the
	// ones this schema produces, or when it has none.
	//
	// Called unconditionally rather than only when a sensitive attribute was
	// filled, because it is a no-op in the other case and a no-op is easier
	// to keep true than a condition. Every mark already on obj.Value survives
	// it: [markSchemaSensitive] combines the paths the value carries with the
	// ones the schema names, which is what upstream's own refresh does.
	obj.Value = markSchemaSensitive(filled, schema.Block)
	log.Printf("[TRACE] projection: filled %d residue attribute(s) of %s from the record store", n, addr)
}

// residueSeedFor is [builder.materialize]'s PRE-READ counterpart to
// [builder.fillResidueFor]'s post-read fill, and the other half of GitHub
// issues #395 and #376's fix: a residue record that
// [residueConfigSourced]'s widening let MIGRATE (or an earlier apply) write
// for a non-Computed, configuration-owned attribute is exactly the value a
// persisted state file's own PriorState would carry into ReadResource, and
// [configuredAttrsSeed] can only ever reconstruct that value when
// configuration itself is statically evaluable - never when the argument
// is a reference to another managed resource's own computed attribute
// (task_definition = aws_ecs_task_definition.this[0].arn is exactly this
// shape, and the config-language subset's static evaluator has no path to
// a managed resource's value at all). The residue record is what a MIGRATE
// or an earlier apply already resolved that reference to, once, for real -
// so seeding from it here is not a guess, it is the same "what would a
// real state file's PriorState already hold" reasoning
// [configuredAttrsSeed]'s own doc comment makes, from a second source.
//
// Returned entries never override one [builder.materialize] already got
// from [configuredAttrsSeed]: static configuration is read fresh on every
// run and a residue record can be stale (written at MIGRATE time, read on
// every later plan), so configuration wins whenever both exist - the
// caller enforces this by only merging in a name absent from its own seed
// map, not by anything here.
//
// This does not widen WHICH attributes are safe to seed beyond
// [residueConfigSourced]'s own population - the residue record may hold
// other, provider-blind attributes (aws_lambda_function.filename and its
// like) that [fillResidueFor] already handles correctly post-read, and
// this function deliberately leaves those to it rather than seeding them
// too. Feeding a non-null value into a Read the provider genuinely never
// consults for that attribute would very likely be harmless (the provider
// would just leave the seed untouched, same as an empty stub), but that
// claim is not exercised or asserted anywhere in this change, so it is not
// made here.
//
// Every failure is silent and best-effort, matching [fillResidueFor]'s own
// stance for the identical store: this run's own later call to
// [builder.fillResidueFor] re-reads the same record and raises
// [SummaryResidueUnreadable] if something is genuinely wrong, so a second
// warning here would only be an echo.
//
// # Staleness (issue #398)
//
// A residue attribute is keyed only by ADDRESS, with nothing tying it to
// the physical object it was captured from - unlike the ownership check
// [builder.checkOwnership] runs for a recordFirst identity, which verifies
// the live object's own tofu-address marker before trusting the record
// ([ownershipStale] in ownership.go). Nothing analogous ever ran here: an
// object destroyed and recreated outside choudoufu, at an address whose
// import path still resolves (a name-based identity, or a marker re-applied
// by something else), would have its OLD residue - a stale ARN reference,
// chiefly - fed straight into the NEW object's read as PriorState. An
// SDKv2 resource that only preserves such an attribute from prior state
// (never reads it from the remote - this file's own [carriesNoInformation]
// is the same shape) round-trips that stale value into the read result,
// which the run then reclassifies and re-records: self-reinforcing, and
// invisible to every verdict-level check, because the plan stays clean.
//
// This does not need a new field to close: [LocatedRecordFrom]'s
// best-effort identity recording (GitHub issue #364 unit A2) already
// writes an Identity member into every instance's record envelope -
// residue's OWN envelope, at the SAME write - for any type it can derive
// one for, taggable or not. That is already "the identity this residue was
// captured against"; using it is reading what write-back already
// committed, not inventing a second store of the same fact the way a
// residue-local import-ID field would.
//
// w's OWN identity for THIS materialize attempt (w.importID / w.values,
// resolved before this call from the marker sweep, the static evaluator or
// a record - never from residue) is what the captured identity is checked
// against. Absence on EITHER side is read permissively, not as evidence of
// staleness: a legacy record written before this check existed, or a type
// [LocatedRecordFrom] cannot derive an identity for, carries no captured
// identity to disagree with, and the seed proceeds exactly as it always
// has - this is what keeps a matching, already-working object's seed
// (corpus-eks-basic's launch-config user_data leg, corpus-ecs-fargate's
// task_definition reference) unchanged. Disagreement between two identities
// that ARE both present is the one case this refuses to seed from.
func (b *builder) residueSeedFor(ctx context.Context, w wanted, schema providers.Schema) map[string]cty.Value {
	if b.opts.RecordStore == nil || schema.Block == nil {
		return nil
	}
	attrs, _, _, residueFound, err := b.opts.RecordStore.GetResidue(ctx, w.addr)
	if err != nil || !residueFound {
		return nil
	}
	if b.residueIdentityStale(ctx, w) {
		return nil
	}
	configSourced := residueConfigSourced(schema)
	var out map[string]cty.Value
	for name, val := range attrs {
		if isResiduePathKey(name) {
			// [residueLeafPathCandidates]' own seeding half: a path-keyed
			// entry is config-sourced under the identical rule
			// [residueConfigSourced] applies at the top level (Required, or
			// Optional and never Computed), asked of the leaf's OWN schema
			// attribute rather than a precomputed top-level map, because a
			// nested leaf has no entry in one. [withSeededAttrs] (build.go)
			// is what actually applies a path-keyed entry once merged in
			// below - [configuredAttrsSeed] never reaches a nested block at
			// all (see its own doc comment), so this is the only source a
			// path-keyed seed can come from.
			path, err := decodeResiduePathKey(name)
			if err != nil {
				continue
			}
			attr, ok := schemaAttrAtPath(schema.Block, path)
			if !ok || attr == nil || attr.WriteOnly {
				continue
			}
			if !attr.Required && !(attr.Optional && !attr.Computed) {
				continue
			}
			if out == nil {
				out = make(map[string]cty.Value, len(attrs))
			}
			out[name] = val
			continue
		}
		if !configSourced[name] {
			continue
		}
		if out == nil {
			out = make(map[string]cty.Value, len(attrs))
		}
		out[name] = val
	}
	return out
}

// residueIdentityStale reports whether w's record-envelope identity - the
// same envelope [b.opts.RecordStore.GetResidue] just read residue from,
// written at the same apply or migrate by [LocatedRecordFrom]'s
// best-effort recording - disagrees with w's own identity for THIS
// materialize attempt. See [residueSeedFor]'s doc comment for why this is
// the check and not a new field, and why absence on either side answers
// false (not stale) rather than true.
//
// Every failure is read the same permissive way: [RecordStore.GetIdentity]
// treats a malformed Identity member as an error (an empty component,
// specifically) precisely so a caller that TRUSTS the identity - the
// recordFirst binding path - never silently uses a broken one. This
// caller does not trust it, only compares it, so a read it cannot use is
// exactly like one that was never captured: no disagreement is provable,
// and residueSeedFor keeps seeding rather than refusing a nicety over a
// record it cannot even parse.
func (b *builder) residueIdentityStale(ctx context.Context, w wanted) bool {
	if b.opts.RecordStore == nil {
		return false
	}
	captured, _, _, identityFound, err := b.opts.RecordStore.GetIdentity(ctx, w.addr)
	if err != nil || !identityFound {
		return false
	}
	current := LocatedRecord{ImportID: w.importID, Components: w.values}
	return !identitiesAgree(current, captured)
}

// identitiesAgree reports whether two [LocatedRecord] values name the same
// object, read permissively: either side being [LocatedRecord.Empty] means
// there is nothing to compare, so they are read as agreeing rather than as
// disagreeing. A single-string identity is compared by that string; a
// composite identity is compared component by component, exactly the
// values [identity.LocatedIdentity] and its siblings produce, so an
// instance whose captured form is a Components map is never coerced
// through ImportID's empty-string case and mistaken for a real mismatch.
func identitiesAgree(current, captured LocatedRecord) bool {
	if current.Empty() || captured.Empty() {
		return true
	}
	if current.ImportID != "" || captured.ImportID != "" {
		return current.ImportID == captured.ImportID
	}
	if len(current.Components) != len(captured.Components) {
		return false
	}
	for name, val := range current.Components {
		if captured.Components[name] != val {
			return false
		}
	}
	return true
}

// SummaryResidueUnreadable is the summary of the warning
// [builder.fillResidueFor] raises when an estate's residue record exists but
// cannot be used. Named for [SummaryLocatedNoStore]'s reason:
// internal/live/refusalscan requires every diagnostic this fork raises to
// have a registry entry, and the entry and the diagnostic have to name one
// string.
const SummaryResidueUnreadable = "Residue record could not be read"

// residueCandidates is the set of attribute names on one resource type that
// this mechanism is even allowed to consider, given the applied object.
//
// It is a filter, not a verdict. Everything it lets through is still put to
// the provider by [classifyResidue]; nothing is stored on the strength of
// this function alone. The filter exists to keep the question small and to
// keep three populations out of it entirely:
//
//   - Credential material, by [identity.CredentialMaterial] - the whole-type
//     form, re-asked here rather than restated, so that a type whose schema
//     grows a secret drops out the day it does. Under
//     `strict { secrets = "store" }`, which is the default, this one does
//     NOT apply: see "What the secrets setting moves here" below.
//   - Sensitive attributes individually, which is the same rule at attribute
//     granularity, and which the secrets setting moves with the whole-type
//     form.
//   - Write-only attributes, ALWAYS, whatever the secrets setting says. See
//     below.
//   - The identity: "id" and every attribute the provider's identity schema
//     names. Those are what say WHICH object this is, they are answered by
//     the marker and the import, and a residue record must never be in a
//     position to move an instance onto a different object.
//
// # What the secrets setting moves here, and what it cannot
//
// GitHub issue #365's `strict { secrets = ... }`, HANDOFF.md's first
// principle expressed as a toggle. The default is [strict.Store], which is
// what stock OpenTofu does: an argument the provider never gives back is
// remembered whether or not the provider marks it sensitive, because stock's
// state file remembers it either way. [strict.Refuse] is what this file did
// before the toggle existed, and it is exactly the two sensitivity
// exclusions above.
//
// Two things the setting does not reach, and they are not the same kind of
// thing.
//
// A WRITE-ONLY attribute, which is a protocol rule. The plugin protocol
// forbids a provider ever returning a write-only value - internal/plugin6/
// validation/write_only.go refuses a response that does - so a recorded one
// could never be checked against the object it claims to describe, and
// stock does not keep one either: it nulls them out before the state is
// written. This is not a stricter or a laxer choice, it is a wrong one, and
// [fillResidue] re-checks it on the way back out for the same reason.
//
// A mark this schema cannot put back. Everything stored here is stored
// UNMARKED - a marked value cannot be JSON-encoded and cannot cross the
// plugin channel - so the sensitivity has to be reconstructed when the
// record is read, and [builder.fillResidueFor] reconstructs it from the
// provider's schema with [markSchemaSensitive]. That is exact for a mark the
// schema itself produced and for nothing else, so a value carrying any OTHER
// mark stays out however the setting reads. See [residueMarkRecoverable],
// which is the predicate, and note what it protects: an attribute whose
// value picked up sensitivity from a `sensitive = true` VARIABLE rather than
// from the schema would come back from the record unmarked, and an unmarked
// prior against a marked planned value is the perpetual sensitivity-only
// update sensitivepaths.go's header describes.
//
// # Nested blocks, and which of them are in scope
//
// Nested object ATTRIBUTES (attr.NestedType) are out of scope: an object
// attribute may itself carry a Sensitive path this file has no per-path
// recoverability proof for (see [residueMarkRecoverable]), and nothing
// below needs to reopen that question to close this one.
//
// Every BLOCK type is in scope except [configschema.NestingGroup] - see
// [residueEligibleBlock]'s own doc comment for the one nesting mode that
// stays out and why. This widened from NestingSingle alone (GitHub issue
// #275's original slice) to also admit NestingList, NestingSet and
// NestingMap once corpus-sumaform-aws's crossing needed it: aws_instance's
// `ephemeral_block_device` ([configschema.NestingSet]) and
// `root_block_device` ([configschema.NestingList], one element) are both
// documented by the provider itself as creation-only - set from
// configuration, never sourced from a live read - which is the identical
// shape `timeouts` already proved out for NestingSingle below. Nothing
// about the classifier changes to admit them: [classifyResidue] already
// compares ANY attribute's value - object, list, set or map - as one whole
// cty.Value via RawEquals, and [carriesNoInformation] already answers
// "does this reads-back value carry no information" for a list, a set and a
// map by asking whether it is empty, the identical question it asks a
// string by asking whether it is "". Widening [residueEligibleBlock] is
// therefore the whole of the change; nothing in the classifier or the
// filler needed to learn a new shape.
//
// [configschema.NestingSingle] blocks were the FIRST admitted, because the
// bound (before this widened) reached only the modes whose absence is
// ambiguous, and a NestingSingle block is one value in the implied object
// type, exactly like a flat attribute - "compare the whole value before and
// after" was already well defined for it. The crossing that demanded it is
// corpus-rds-complete-postgres: terraform-aws-modules writes
// `timeouts { create = "10m" delete = "15m" }` on its security group and
// its default route table, a block the provider's Read never sources from
// the remote and only ever preserves from the prior it was handed. A stock
// state file holds it; a stateless prior state had nowhere to hold it, so
// every replan after a clean migrate proposed `+ timeouts {...}` on those
// instances forever, against a stock plan that shows the same block
// unchanged. Nothing about the rule names a type or a block: `timeouts` is
// simply the single-nested block hashicorp/aws puts on most of its
// resources, and any other config-only single-nested block rides the same
// path - and now any config-only list-, set- or map-nested block does too.
//
// Safety is unchanged and still comes from [classifyResidue], not from
// here. A nested block the provider really does source from the remote
// fails read A's test; one the provider does not preserve fails read B's.
// aws_default_network_acl's `egress`/`ingress` are the worked example of
// why widening the filter does not silence real drift: they are
// [configschema.NestingSet], so before this widening they never reached
// this list at all - and now that they do, floci returning no rules on a
// bare-identity read still makes read A disagree with the pattern read A
// must match for a candidate that WOULD have been recorded (a rule set the
// live system genuinely tracks answers with real rules, not with an empty
// or identical-to-applied set), so they are still correctly excluded. A set
// of rules the provider truly manages is drift, exactly as before; only a
// set the provider never looks at converts to residue.
//
// # Why the block half does not take the secrets setting
//
// [residueEligibleBlock] refuses a block with anything sensitive or
// write-only anywhere inside it, and it refuses it under BOTH settings.
// That is not an oversight and it is not a stricter reading of the toggle:
// both of its reasons are the two things named just above as the ones the
// setting cannot reach.
//
// Write-only is the protocol rule. A block is refused for containing one
// anywhere exactly as an attribute is refused for being one, and for the
// identical reason - the provider may never return the value, so no stored
// copy could ever be checked against the object it claims to describe.
//
// Sensitive INSIDE a block is the mark question, not the policy question. A
// sensitive flat attribute may be recorded under [strict.Store] only
// because there is an exact proof that its mark comes back: one mark, on
// the whole attribute value, which [markSchemaSensitive] reproduces from
// the schema and which [residueMarkRecoverable] checks for by value. A
// sensitive attribute inside a nested block puts its mark at a path
// INSIDE the block's value, which is the one shape residueMarkRecoverable
// names as unrecoverable, and there is no per-path equivalent of that
// proof. So such a block stays out under `secrets = "store"` for the mark
// reason and under `secrets = "refuse"` for the policy reason: one verdict
// with two independent supports, which is why no setting is threaded into
// the predicate. It fails in the safe direction - the estate keeps
// proposing that block on every plan, which is loud - rather than filling a
// value back without the sensitivity the planned value carries, which is
// the perpetual sensitivity-only update sensitivepaths.go's header
// describes.
//
// # Required, Optional and Computed are not asked here
//
// An earlier version of this filter also required the attribute to be
// Required or Optional, on the reasoning that a purely Computed attribute
// "cannot be set in configuration, so there is nothing to remember". The
// corpus-xancloud-iac crossing refuted that: aws_nat_gateway's
// regional_nat_gateway_address is Computed only (AWS populates it for
// regional NAT gateways, which this one is not, so the live value is an
// empty set - confirmed directly against the emulator's own
// DescribeNatGateways response), yet the provider's Read does not
// re-derive it from a bare identity-only prior - it leaves whatever the
// prior held, which [identityOnly] set to null, and OpenTofu's plan then
// marks a null Computed attribute "known after apply" forever. That is
// exactly the shape [classifyResidue] exists to catch; only the schema
// population was too narrow to let it try. There IS something to remember
// for a Computed attribute: the value the last successful read produced,
// which is exactly what "the value the apply produced" (this file's own
// framing, above) already means for an attribute nothing ever sets.
//
// Safety does not come from this population restriction and never did: it
// comes from [classifyResidue]'s two-read discriminator (a candidate is
// only ever recorded when read A proves the provider does not source it
// from the remote AND read B proves the provider merely preserves the
// prior) and from [fillResidue]'s own rule that a record may only ever
// fill a read that currently carries no information. A Required attribute
// cannot be Computed at the same time (the protocol forbids the
// combination) and reaches here harmlessly; the identity stays excluded by
// [residueIdentityAttrs] regardless of Required/Optional/Computed, which is
// why removing this restriction does not reopen the identity question.
func residueCandidates(schema providers.Schema, applied cty.Value, secrets strict.Secrets) []string {
	if schema.Block == nil || applied == cty.NilVal || applied.IsNull() || !applied.Type().IsObjectType() {
		return nil
	}
	storing := strict.StoresSecrets(secrets)
	if !storing && identity.CredentialMaterial(schema.Block) {
		return nil
	}
	identityAttrs := residueIdentityAttrs(schema)

	var out []string
	for name, attr := range schema.Block.Attributes {
		if attr == nil || identityAttrs[name] {
			continue
		}
		if attr.WriteOnly {
			// Never, under any setting. See this function's doc comment.
			continue
		}
		if attr.Sensitive && !storing {
			continue
		}
		if attr.NestedType != nil {
			continue
		}
		if !applied.Type().HasAttribute(name) {
			continue
		}
		v := applied.GetAttr(name)
		if v.IsNull() || !v.IsWhollyKnown() {
			continue
		}
		if !residueMarkRecoverable(attr, v) {
			continue
		}
		out = append(out, name)
	}
	for name := range schema.Block.BlockTypes {
		if identityAttrs[name] || !residueEligibleBlock(schema.Block, name) {
			continue
		}
		if !applied.Type().HasAttribute(name) {
			continue
		}
		v := applied.GetAttr(name)
		if v.IsNull() || !v.IsWhollyKnown() {
			continue
		}
		// The same recoverability predicate the flat attributes are put to,
		// with nil for "no attribute to read a Sensitive flag off". For a
		// block that means unmarked or nothing, DEEPLY - see
		// [residueMarkRecoverable]'s own note on why a shallow IsMarked is
		// the wrong question about a block value.
		if !residueMarkRecoverable(nil, v) {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// residuePathCandidate is one flat, sensitive-or-not settable argument
// found beneath a block boundary, together with the schema [configschema.Attribute]
// that governs it and whether configuration is the only thing that can ever
// set it (see [residueConfigSourced]'s reasoning, asked here per-leaf
// because a leaf's own Required/Optional/Computed flags are all that
// question needs).
type residuePathCandidate struct {
	Path          cty.Path
	Attr          *configschema.Attribute
	ConfigSourced bool
}

// residueLeafPathCandidates is [residueCandidates]'s generalization to any
// path depth (GitHub issue #401 family 2, and the same gap named in issue
// #275's own original design sketch: "This walk only reaches the schema's
// own top level").
//
// [residueCandidates]'s block loop treats a whole nested block as ONE
// candidate and [residueEligibleBlock] refuses it outright the moment
// ANYTHING inside is sensitive or write-only, because
// [residueMarkRecoverable] can only ever prove a WHOLE value's marks are
// reconstructible, and a lone sensitive leaf beside ordinary siblings
// leaves the block's own value carrying a mark at a path INSIDE it - the
// one shape that predicate names as unrecoverable at block granularity
// (see [TestResidueRefusesASingleNestedBlockHoldingASecret]'s own doc
// comment, which is the test this function's existence does not
// contradict: that test is about the WHOLE-BLOCK candidate
// [residueCandidates] produces, and it still refuses one, unchanged).
//
// A single leaf's sensitivity IS recoverable, at leaf granularity, because
// [markSchemaSensitive] already restores it generically at any depth from
// the schema alone - every call site of it descends the whole block tree
// through [configschema.Block.ValueMarks], not just the top level. So this
// walks every nested block, at any depth, through every nesting mode
// including [configschema.NestingGroup] ([residueEligibleBlock] excludes
// Group because a WHOLE block's absence cannot be told apart from its
// presence-but-empty there; a flat leaf's own [carriesNoInformation] test
// never has to ask that question, so the exclusion has nothing to answer
// for at this granularity), applying the identical per-attribute filter
// [residueCandidates]'s own flat-attribute loop applies: WriteOnly refused
// outright, Sensitive gated on secrets, NestedType left out of scope (the
// same scope [residueCandidates] itself leaves it, for the same reason -
// a nested object attribute needs its own decode and nothing measured here
// needs it yet), and [residueMarkRecoverable] as the final word on whether
// the leaf's own marks are reconstructible - a `sensitive = true` VARIABLE
// feeding an otherwise-ordinary nested argument is refused here exactly as
// it is at the top level, for the identical reason.
//
// aws_lb_listener.default_action.authenticate_oidc.client_secret (issue
// #401 family 2) is the confirmed case: default_action is NestingList,
// authenticate_oidc is NestingList nested inside it (max_items 1), and
// client_secret is Required and Sensitive two levels down - a shape
// [residueCandidates]'s own top-level BlockTypes loop never reaches at
// all, and which would refuse default_action wholesale even if it did,
// because default_action's OTHER arguments (target_group_arn, type, order)
// are all real, provider-echoed values that a whole-block candidate would
// incorrectly try to freeze.
func residueLeafPathCandidates(schema providers.Schema, applied cty.Value, secrets strict.Secrets) []residuePathCandidate {
	if schema.Block == nil || applied == cty.NilVal || applied.IsNull() || !applied.Type().IsObjectType() {
		return nil
	}
	storing := strict.StoresSecrets(secrets)
	if !storing && identity.CredentialMaterial(schema.Block) {
		return nil
	}
	var out []residuePathCandidate
	for name, blk := range schema.Block.BlockTypes {
		if blk == nil || !applied.Type().HasAttribute(name) {
			continue
		}
		walkResidueBlockType(blk, applied.GetAttr(name), residuePathAppend(nil, cty.GetAttrStep{Name: name}), storing, &out)
	}
	sort.Slice(out, func(i, j int) bool {
		return tfdiags.FormatCtyPath(out[i].Path) < tfdiags.FormatCtyPath(out[j].Path)
	})
	return out
}

// residuePathAppend returns a fresh path with step appended, never sharing
// prefix's backing array with any sibling call - the same hazard
// [cty.Path]'s own append-based construction has everywhere else in this
// codebase, and the reason every caller below goes through this rather
// than a bare `append(prefix, step)`.
func residuePathAppend(prefix cty.Path, step cty.PathStep) cty.Path {
	out := make(cty.Path, len(prefix)+1)
	copy(out, prefix)
	out[len(prefix)] = step
	return out
}

// walkResidueBlockType is [residueLeafPathCandidates]' per-nesting-mode
// dispatch: a [configschema.NestingSingle] or [configschema.NestingGroup]
// block is one object at prefix, and every other admitted mode is a
// collection walked element by element, each element's own path carrying
// an extra [cty.IndexStep] - a list index, a set element's own value, or a
// map key, exactly the step [cty.Path.Apply] already knows how to resolve
// back through the identical collection kind.
func walkResidueBlockType(blk *configschema.NestedBlock, v cty.Value, prefix cty.Path, storing bool, out *[]residuePathCandidate) {
	if blk == nil || v == cty.NilVal || v.IsNull() || !v.IsWhollyKnown() {
		return
	}
	if v.IsMarked() {
		// A whole block-type value carrying a mark is not this walk's
		// ordinary case (residueMarkRecoverable's own per-leaf marks are
		// how a candidate leaf inside an ordinary, unmarked block gets
		// found), but cty.Value.ElementIterator panics on a marked
		// receiver, and marksafe's own rule applies here exactly as
		// everywhere else: refuse rather than unmark. Skipping this
		// subtree only means a candidate inside it is not found, which is
		// the same safe direction residueCandidates' own filters already
		// fail in.
		return
	}
	switch blk.Nesting {
	case configschema.NestingSingle, configschema.NestingGroup:
		walkResidueBlockBody(&blk.Block, v, prefix, storing, out)
	case configschema.NestingList, configschema.NestingSet, configschema.NestingMap:
		if !v.CanIterateElements() {
			return
		}
		for it := v.ElementIterator(); it.Next(); {
			kv, ev := it.Element()
			walkResidueBlockBody(&blk.Block, ev, residuePathAppend(prefix, cty.IndexStep{Key: kv}), storing, out)
		}
	}
}

// walkResidueBlockBody is [residueLeafPathCandidates]'s per-block-instance
// walk: every flat attribute the same filter [residueCandidates]'s own
// loop applies, plus recursion into every further nested block type at
// prefix, so a leaf at any depth is reached the same way client_secret is
// reached two levels down.
func walkResidueBlockBody(b *configschema.Block, v cty.Value, prefix cty.Path, storing bool, out *[]residuePathCandidate) {
	if b == nil || v == cty.NilVal || v.IsNull() || !v.IsWhollyKnown() || !v.Type().IsObjectType() {
		return
	}
	for name, attr := range b.Attributes {
		if attr == nil || attr.WriteOnly || attr.NestedType != nil {
			continue
		}
		if attr.Sensitive && !storing {
			continue
		}
		if !v.Type().HasAttribute(name) {
			continue
		}
		leaf := v.GetAttr(name)
		if leaf.IsNull() || !leaf.IsWhollyKnown() {
			continue
		}
		if !residueMarkRecoverable(attr, leaf) {
			continue
		}
		*out = append(*out, residuePathCandidate{
			Path:          residuePathAppend(prefix, cty.GetAttrStep{Name: name}),
			Attr:          attr,
			ConfigSourced: attr.Required || (attr.Optional && !attr.Computed),
		})
	}
	for name, nblk := range b.BlockTypes {
		if nblk == nil || !v.Type().HasAttribute(name) {
			continue
		}
		walkResidueBlockType(nblk, v.GetAttr(name), residuePathAppend(prefix, cty.GetAttrStep{Name: name}), storing, out)
	}
}

// residuePathKeyPrefix marks a residue [residueFields.Attributes] key as a
// canonically-encoded [cty.Path] rather than a bare top-level attribute
// name - a JSON array always opens with '[', and a bare attribute name is
// a Go/HCL identifier, which never does, so the two key spaces can never
// collide and a record written before path-keyed residue existed decodes
// exactly as it always did.
const residuePathKeyPrefix = '['

// isResiduePathKey reports whether a [residueFields.Attributes] key is a
// [residueLeafPathCandidates] path rather than a flat attribute name.
func isResiduePathKey(key string) bool {
	return len(key) > 0 && key[0] == residuePathKeyPrefix
}

// encodeResiduePathKey and [decodeResiduePathKey] are this file's own
// [marshalSensitivePaths]/[unmarshalSensitivePaths], reusing the identical
// step encoding (sensitivepaths.go's own [pathStepJSON]) for a single path
// used as a map key rather than a member of a stored list - the same shape
// for the same reason: the state file's own paths encoding is already
// proven to round-trip every step [cty.Path] can hold.
func encodeResiduePathKey(path cty.Path) (string, error) {
	steps := make([]pathStepJSON, 0, len(path))
	for _, step := range path {
		switch s := step.(type) {
		case cty.GetAttrStep:
			name, err := json.Marshal(s.Name)
			if err != nil {
				return "", fmt.Errorf("encoding the attribute step %q of a residue path: %w", s.Name, err)
			}
			steps = append(steps, pathStepJSON{Type: getAttrPathStepType, Value: name})
		case cty.IndexStep:
			key, err := ctyjson.Marshal(s.Key, cty.DynamicPseudoType)
			if err != nil {
				return "", fmt.Errorf("encoding an index step of a residue path: %w", err)
			}
			steps = append(steps, pathStepJSON{Type: indexPathStepType, Value: key})
		default:
			return "", fmt.Errorf("a residue path contains a %T step, which cannot be encoded", step)
		}
	}
	out, err := json.Marshal(steps)
	if err != nil {
		return "", fmt.Errorf("encoding a residue path: %w", err)
	}
	return string(out), nil
}

// decodeResiduePathKey reverses [encodeResiduePathKey].
func decodeResiduePathKey(key string) (cty.Path, error) {
	var steps []pathStepJSON
	if err := json.Unmarshal([]byte(key), &steps); err != nil {
		return nil, fmt.Errorf("a residue path key is not valid JSON: %w", err)
	}
	var path cty.Path
	for _, step := range steps {
		switch step.Type {
		case getAttrPathStepType:
			var name string
			if err := json.Unmarshal(step.Value, &name); err != nil {
				return nil, fmt.Errorf("a residue path key's attribute step could not be read: %w", err)
			}
			path = append(path, cty.GetAttrStep{Name: name})
		case indexPathStepType:
			key, err := ctyjson.Unmarshal(step.Value, cty.DynamicPseudoType)
			if err != nil {
				return nil, fmt.Errorf("a residue path key's index step could not be read: %w", err)
			}
			path = append(path, cty.IndexStep{Key: key})
		default:
			return nil, fmt.Errorf("a residue path key contains an unsupported step type %q", step.Type)
		}
	}
	return path, nil
}

// schemaAttrAtPath resolves path against block the same way
// [walkResidueBlockBody]/[walkResidueBlockType] constructed it, and is
// [fillResiduePaths]' and [builder.residueSeedFor]'s way of re-asking
// today's schema what governs a path a record was written against,
// possibly a schema version or two ago - the identical re-check
// [fillResidue]'s own doc comment explains for the flat case ("a record
// written months ago against a schema where an attribute was ordinary must
// not be applied after a provider release marks it"). A path whose shape
// no longer matches today's schema (a block renamed, a nesting mode
// changed, an attribute that became a block) answers false rather than
// guessing.
func schemaAttrAtPath(block *configschema.Block, path cty.Path) (*configschema.Attribute, bool) {
	b := block
	for i := 0; i < len(path); i++ {
		step, ok := path[i].(cty.GetAttrStep)
		if !ok {
			return nil, false
		}
		if attr, ok := b.Attributes[step.Name]; ok {
			if i != len(path)-1 {
				// An attribute has no further children; a path continuing
				// past one names something this schema does not have.
				return nil, false
			}
			return attr, true
		}
		blk, ok := b.BlockTypes[step.Name]
		if !ok || blk == nil {
			return nil, false
		}
		b = &blk.Block
		switch blk.Nesting {
		case configschema.NestingList, configschema.NestingSet, configschema.NestingMap:
			i++
			if i >= len(path) {
				return nil, false
			}
			if _, ok := path[i].(cty.IndexStep); !ok {
				return nil, false
			}
		}
	}
	return nil, false
}

// residueEligibleBlock reports whether name is a block type on this
// schema that residue may carry, and it is the ONE place that question is
// answered - [residueCandidates] asks it to decide what may be recorded and
// [fillResidue] asks it to decide what may be filled, so the two populations
// cannot drift apart.
//
// Two conditions, both from [residueCandidates]'s doc comment:
//
//   - The nesting mode is one whose ABSENCE reads back as a value
//     [carriesNoInformation] can tell apart from a real, present-but-empty
//     answer: [configschema.NestingSingle] (absent is null, present is an
//     object - the classifier's whole-value discriminator was written for
//     exactly this shape), or [configschema.NestingList],
//     [configschema.NestingSet] or [configschema.NestingMap] (absent is an
//     empty collection, present is a non-empty one - [carriesNoInformation]
//     already answers this for every flat list/set/map attribute, and nesting
//     a block instead of a scalar inside the collection changes nothing about
//     whether it is empty). [configschema.NestingGroup] is the one mode this
//     excludes, and it is excluded for a reason none of the other four share:
//     an absent group reads back as a block full of zero-valued attributes
//     rather than as null or as empty, so "read A carries no information"
//     cannot be told apart from "the group is really all zeroes" - there is
//     no reading of a NestingGroup value that means "not present".
//   - Nothing sensitive or write-only anywhere inside it. That is the same
//     rule the flat-attribute filter applies through attr.Sensitive and
//     attr.WriteOnly, asked over a whole nested schema because a block has
//     no single flag to read.
//
// Neither condition, nor anything else in [classifyResidue], [fillResidue] or
// [carriesNoInformation], reads a nesting mode's ELEMENT count or asks
// whether an element within a list or set individually came from the remote:
// the whole collection is one candidate, classified and filled as one value,
// exactly the way a NestingSingle block already was. A live read that
// returns SOME real elements and omits others for the same block does not
// match either classifier pattern (it is neither "identical to what read A's
// null-prior identity-only stub produced" nor "empty"), so it is correctly
// left out rather than partially recorded - the same safety
// [residueMarkRecoverable]'s deep, no-partial-marks rule already gives a
// block with a mark on only one of its arguments.
func residueEligibleBlock(block *configschema.Block, name string) bool {
	if block == nil {
		return false
	}
	blk := block.BlockTypes[name]
	if blk == nil {
		return false
	}
	switch blk.Nesting {
	case configschema.NestingSingle, configschema.NestingList, configschema.NestingSet, configschema.NestingMap:
	default:
		return false
	}
	return !blk.ContainsSensitive() && !containsWriteOnly(&blk.Block)
}

// containsWriteOnly reports whether a block schema carries a write-only
// attribute anywhere inside it. [configschema.Block.ContainsSensitive] is
// the same walk for the sensitive flag and already exists; there is no
// value-free equivalent for write-only ([configschema.Block.WriteOnlyPaths]
// needs a value), so this is it.
func containsWriteOnly(b *configschema.Block) bool {
	if b == nil {
		return false
	}
	for _, attr := range b.Attributes {
		if attr == nil {
			continue
		}
		if attr.WriteOnly {
			return true
		}
		if attr.NestedType != nil && nestedContainsWriteOnly(attr.NestedType) {
			return true
		}
	}
	for _, blk := range b.BlockTypes {
		if blk == nil {
			continue
		}
		if containsWriteOnly(&blk.Block) {
			return true
		}
	}
	return false
}

// nestedContainsWriteOnly is [containsWriteOnly] over a nested object
// attribute's own schema.
func nestedContainsWriteOnly(o *configschema.Object) bool {
	if o == nil {
		return false
	}
	for _, attr := range o.Attributes {
		if attr == nil {
			continue
		}
		if attr.WriteOnly {
			return true
		}
		if attr.NestedType != nil && nestedContainsWriteOnly(attr.NestedType) {
			return true
		}
	}
	return false
}

// residueMarkRecoverable reports whether v's marks are exactly the ones
// [markSchemaSensitive] would put back on it from attr alone.
//
// A residue record stores an unmarked value, so every mark on the way in has
// to be reconstructible on the way out or it is lost. Three answers:
//
//   - No marks at all: nothing to reconstruct, and this is every value that
//     reached this function before the secrets setting existed.
//   - One [marks.Sensitive] mark on the whole attribute value, where the
//     schema marks the attribute Sensitive: exactly what
//     [configschema.Block.ValueMarks] produces for such an attribute, so
//     [markSchemaSensitive] restores it identically.
//   - Anything else: refused. A mark at a path INSIDE the value, a second
//     mark of any kind, or a Sensitive mark on an attribute the schema does
//     not call sensitive - which is the sensitive-VARIABLE case, where the
//     sensitivity is a fact about this configuration and not about the type,
//     and no schema read can bring it back.
//
// The third answer is the one worth being strict about, and it is strict in
// the safe direction: refusing a candidate leaves the estate proposing an
// update to that argument, which is visible on every plan. Recording it and
// filling it back unmarked would produce a prior that disagrees with the
// planned value on sensitivity alone - the perpetual "The value is
// unchanged" update sensitivepaths.go's header describes - which is the same
// nuisance wearing a disguise.
//
// Nested attribute types never reach here ([residueCandidates] excludes
// them), so "the whole attribute value" is the only path a schema mark can
// land on.
//
// # attr may be nil, and that is the BLOCK case (any admitted nesting mode)
//
// [residueCandidates]'s second loop asks this question about a block value,
// where there is no [configschema.Attribute] to read a Sensitive flag off.
// nil is the honest way to say so, and the answer it produces is the right
// one: only the first of the three answers above is available, so any mark
// on or inside a block value refuses the candidate.
//
// That is not a conservative default, it is the exact rule.
// [residueEligibleBlock] has already established that nothing inside
// this block is Sensitive by the schema, so [configschema.Block.ValueMarks]
// puts nothing back inside it and [markSchemaSensitive] cannot restore any
// mark it carries. A mark reaching here therefore came from somewhere the
// schema cannot be read back out of - a `sensitive = true` variable feeding
// one of the block's arguments is the case that produces it - which is the
// third answer's own reasoning arriving by a different door.
//
// The deep walk matters here in a way it does not for a flat attribute.
// cty's Value.IsMarked is shallow: it reports a mark on the value itself
// and says nothing about a mark on an argument inside a block. An earlier
// form of the block loop asked IsMarked and would have recorded a block
// whose inner argument carried a variable's sensitivity, then filled it
// back unmarked. UnmarkDeepWithPaths is what closes that, and it is why
// both populations ask this one function rather than two spellings of it.
func residueMarkRecoverable(attr *configschema.Attribute, v cty.Value) bool {
	_, pvms := v.UnmarkDeepWithPaths()
	if len(pvms) == 0 {
		return true
	}
	if attr == nil {
		return false
	}
	if !attr.Sensitive || len(pvms) != 1 {
		return false
	}
	if len(pvms[0].Path) != 0 || len(pvms[0].Marks) != 1 {
		return false
	}
	_, sensitive := pvms[0].Marks[marks.Sensitive]
	return sensitive
}

// RecordResidueForInstance is [classifyResidue]'s classify-and-record step
// for ONE instance, exported so a second write path can populate the same
// residue store [writeBackResidue] does, from ITS OWN real applied value
// rather than waiting for a choudoufu apply to observe one.
//
// GitHub issue #327 is why this exists: live-import's Approve ratifies every
// eligible instance against the live system with a real, non-null prior -
// the migrated state file's own recorded object - which is exactly the
// [classifyResidue] "read B" a migrate never otherwise produces. Without
// this, an attribute an SDKv2 resource's Read only preserves from whatever
// prior it was given - never reads from the remote, see [carriesNoInformation]'s
// doc comment - comes back null on the FIRST live-plan after a clean
// migrate, because that plan's own prior (built from
// [providers.Configured.ImportResourceState]'s bare stub) has nothing to
// preserve. For an ordinary argument this is a phantom update, already
// covered once a first choudoufu apply classifies it; for a ForceNew
// argument it is a phantom REPLACE of an object that is not actually
// different, on every plan until that first apply happens.
//
// secrets is the operator's `strict { secrets = ... }` setting, which this
// path takes as an argument rather than reading from a configuration for the
// one reason the other two do not: internal/live/liveimport's Request is
// built from a state file and a provider set, and the migrate command reads
// its configuration for exactly two facts (the estate name and the record
// store) which it passes the same way. Threading a third is the smaller
// change, and identity.SecretsFor is still the one place the omitted
// argument resolves - the caller calls it.
//
// read is the caller's ReadResource wrapper, called twice by
// [classifyResidue] exactly as [writeBackResidue] calls it - once with an
// identity-only stub, once with applied itself. recorded reports whether
// anything was classified and stored; a false with a nil error is the
// ordinary "this type has nothing residue-shaped" or "the provider proved it
// reads everything from the remote" answer, not a failure.
//
// Every failure is closed the same way [writeBackResidue] closes one: the
// caller is expected to turn a non-nil error into a warning, never into a
// reason to fail the migration over a residue nicety.
func RecordResidueForInstance(ctx context.Context, store *RecordStore, addr addrs.AbsResourceInstance, provider addrs.AbsProviderConfig, schema providers.Schema, applied cty.Value, secrets strict.Secrets, read func(prior cty.Value) (cty.Value, error), identityObj cty.Value) (recorded bool, err error) {
	if store == nil || schema.Block == nil || applied == cty.NilVal || applied.IsNull() {
		return false, nil
	}
	attrs, ok := classifyResidueAll(schema, applied, secrets, read, identityObj)
	if !ok {
		return false, nil
	}
	rf, err := encodeResidueFields(attrs)
	if err != nil {
		return false, fmt.Errorf("encoding residue for %s: %w", addr, err)
	}
	// Read-before-write rather than a version this call was handed: unlike
	// the apply write-back path, which already tracked a prior-plan version
	// through [builder.fillResidueFor], a migrate has never read this
	// estate's record store before, so there is nothing to have tracked. A
	// second live-import run over the same state (documented as idempotent)
	// is what makes this matter - without a fresh read, the write would
	// always assert "nothing recorded yet" and fail every time but the
	// first.
	_, version, _, _, getErr := store.GetResidue(ctx, addr)
	if getErr != nil {
		return false, fmt.Errorf("reading the existing residue record for %s before writing: %w", addr, getErr)
	}
	if _, err := store.mergeEnvelope(ctx, addr, version, func(env *recordEnvelope) {
		env.Residue = rf
		env.Provider = providerString(provider)
	}); err != nil {
		return false, fmt.Errorf("recording residue for %s: %w", addr, err)
	}
	return true, nil
}

// SummaryResidueNotClassified is the summary of the warning
// [writeBackRecordEnvelopes] raises when an apply could not record what it
// sent. Named for [SummaryLocatedNoStore]'s reason.
const SummaryResidueNotClassified = "Argument values could not be recorded"

// residueProvider resolves one provider configuration once per write-back,
// so an estate of sixty instances through one provider opens one connection
// rather than sixty.
func residueProvider(ctx context.Context, provs Providers, cache map[string]providers.Interface, addr addrs.AbsProviderConfig) (providers.Interface, error) {
	key := addr.String()
	if p, ok := cache[key]; ok {
		if p == nil {
			return nil, fmt.Errorf("provider %s was already found to be unavailable during this write-back", addr)
		}
		return p, nil
	}
	p, err := provs.ConfiguredProvider(ctx, addr)
	if err != nil || p == nil {
		cache[key] = nil
		if err == nil {
			err = fmt.Errorf("no provider instance available for %s", addr)
		}
		return nil, err
	}
	cache[key] = p
	return p, nil
}

// residueIdentityAttrs is the set of attribute names that say WHICH object
// this is: "id", which is the universal SDKv2 identity and the attribute
// OpenTofu's own import path round-trips, plus every attribute the
// provider's resource identity schema names.
//
// It is the one set [classifyResidue] keeps real in both of its priors, and
// the one set that is never a candidate. Both follow from the same fact: an
// identity is what addresses the object, so nulling it would read a
// different object (or none), and recording it would put a residue record
// in a position to move an instance onto a different object.
func residueIdentityAttrs(schema providers.Schema) map[string]bool {
	out := map[string]bool{"id": true}
	if schema.IdentitySchema != nil {
		for name := range schema.IdentitySchema.Attributes {
			out[name] = true
		}
	}
	return out
}

// residueConfigSourced reports, for every flat attribute a schema names,
// whether configuration is the ONLY thing that can ever set its value: the
// plugin protocol's own Required/Optional/Computed contract (see
// [configschema.Attribute]'s own doc comment) says a Required or a plain
// Optional (never Computed) attribute's true value can never differ from
// what was last configured - Computed is the ONE flag that lets a provider
// answer independently of configuration at all. [classifyResidue] uses this
// to widen its own read-A/read-B test for GitHub issues #395 and #376: a
// non-Computed attribute's read A can never be showing REAL, independent
// drift, so a real-looking answer there can only be a representation
// artifact (a legacy-SDK Read choosing between two equivalent SPELLINGS of
// the identical value based on what PriorState happened to hold), never a
// genuinely different underlying value - see [classifyResidue]'s own
// widened branch for the safety condition that still catches actual drift.
//
// NestedType and block-typed names are deliberately absent from the
// returned map (as opposed to present and false): [classifyResidue] only
// ever looks up a flat candidate here, so the two are indistinguishable to
// every caller and there is no need to manufacture an answer for a
// question nothing asks.
func residueConfigSourced(schema providers.Schema) map[string]bool {
	out := map[string]bool{}
	if schema.Block == nil {
		return out
	}
	for name, attr := range schema.Block.Attributes {
		if attr == nil || attr.WriteOnly || attr.NestedType != nil {
			continue
		}
		if attr.Required || (attr.Optional && !attr.Computed) {
			out[name] = true
		}
	}
	return out
}

// ambientIdentityValues is GitHub issue #402's escape hatch: the values a
// resource's OWN identity object reports for the two attribute names
// hashicorp/aws's Resource Identity feature uses, provider-wide, for the
// caller's own account and region - "account_id" and "region", the same two
// literal names live/identity's generated table already keys every
// {Cloud: "account-id"} / {Cloud: "region"} component on (see
// internal/live/identity/table_generated.go). Nil whenever identityObj
// carries neither: every read against a provider or provider version that
// never served a resource identity, and every type whose identity schema
// does not name either.
//
// This is deliberately NOT [identity.CloudContext] plumbed in from outside.
// The value this run is authenticated as is already sitting in the exact
// response classifyResidue's own two reads (or [builder.materialize]'s
// single one) just received - hashicorp/aws's SDK resolves it once at
// Configure time and attaches it to every ReadResource/ImportResourceState
// response that carries a native identity, unconditionally, before it has
// looked at PriorState at all (confirmed directly against floci with
// TF_LOG=trace: the x-amz-expected-bucket-owner header on
// aws_s3_bucket_cors_configuration's GetBucketCors call already carries the
// caller's own account id before the request is even sent). Reading it back
// off the SAME response this package already holds needs no new plumbing
// and cannot go stale relative to the run it describes.
func ambientIdentityValues(schema providers.Schema, identityObj cty.Value) map[string]cty.Value {
	if schema.IdentitySchema == nil || identityObj == cty.NilVal || identityObj.IsNull() || !identityObj.Type().IsObjectType() {
		return nil
	}
	var out map[string]cty.Value
	for _, name := range [...]string{"account_id", "region"} {
		if _, ok := schema.IdentitySchema.Attributes[name]; !ok {
			continue
		}
		if !identityObj.Type().HasAttribute(name) {
			continue
		}
		v := identityObj.GetAttr(name)
		if v.IsNull() || !v.IsWhollyKnown() {
			continue
		}
		if out == nil {
			out = make(map[string]cty.Value, 2)
		}
		out[name] = v
	}
	return out
}

// isAmbientEcho reports whether v is exactly one of ambient's values - see
// [ambientIdentityValues]. A candidate whose only source is the run's own
// ambient cloud context is not something any configuration chose and is
// never preservable residue, however the two-read discriminator below
// would otherwise have classified it: the account a run is authenticated as
// cannot "drift" relative to itself, so a real-looking answer here is
// exactly the format-only-widening branch's premise turned on its head -
// not a different spelling of a configured value, but a value with no
// configured spelling at all.
func isAmbientEcho(v cty.Value, ambient map[string]cty.Value) bool {
	if v == cty.NilVal || v.IsNull() || v.IsMarked() || len(ambient) == 0 {
		return false
	}
	for _, av := range ambient {
		if v.RawEquals(av) {
			return true
		}
	}
	return false
}

// scrubAmbientEcho is GitHub issue #402's fix for the population
// [classifyResidue]'s own guard cannot reach: a Required-or-plain-Optional
// (never Computed) flat attribute whose provider Read returns the run's own
// ambient account id or region UNCONDITIONALLY - identically whether the
// prior it was handed carried nothing, an identity-only stub, or the fully
// applied object - so the two-read discriminator's own first branch
// ("read A already equals applied, so the provider reads this from the
// remote") reads as ordinary live drift-tracking and never asks the
// question [isAmbientEcho] answers at all.
//
// Confirmed the source of corpus-s3-bucket-complete's forced-replacement
// regression directly against a live floci + hashicorp/aws 6.59.0, no
// residue store or classify path in the loop: aws_s3_bucket_cors_
// configuration's (and its _object_lock_configuration, _server_side_
// encryption_configuration and _versioning siblings') deprecated
// expected_bucket_owner argument comes back "000000000000" from
// ReadResource even when PriorState carries nothing for it at all - the
// AWS SDK attaches x-amz-expected-bucket-owner from its own resolved
// session to the underlying API call before the request is even sent,
// unconditionally, and the emitted state simply reflects that header back.
// [builder.materialize] takes that raw read as an untaggable, record-first
// instance's entire prior state on every single plan (issue #364 unit B's
// own new read path, which never went through a real ReadResource for this
// population before), so a real, non-null value for an argument nothing in
// configuration sets - and which the plugin protocol's own contract says
// can ONLY ever be what configuration sets, because it is neither Computed
// nor anything else - turns into a forced replacement (ForceNew) every
// time configuration omits the argument, which never converges: the next
// plan reads the identical unconditional echo right back.
//
// # Why this is not the same fix as [classifyResidue]'s guard
//
// That guard stops an ambient echo from being CAPTURED and REPLAYED by the
// residue store; it never had anything to capture here; classifyResidue's
// own read-A/read-B pair, run against this exact provider behavior,
// already declines to record these four types' expected_bucket_owner
// (confirmed by trace: read A already equals applied even from a bare
// stub, which is [classifyResidue]'s "the provider reads this from the
// remote, not ours to remember" branch - correct, as far as it goes). The
// defect is upstream of residue entirely: the raw value [importAndRead]
// returns becomes the plan's prior VERBATIM, with nothing else in the
// pipeline ever asking whether a config-only argument's live answer could
// be real.
//
// # Why configuredSeed decides "config-absent" here
//
// attrsSeed (this function's configuredSeed) is [configuredAttrsSeed]'s and
// [builder.residueSeedFor]'s already-merged answer to "what does this run's
// own configuration - statically, or through a residue record a prior
// choudoufu apply already resolved a reference to - genuinely supply for
// this instance", the identical seed [importAndRead] itself just used to
// build the read's own PriorState. A name present there is left exactly as
// read: configuration set it, honestly, whether or not the value it
// resolved to happens to equal this run's own ambient account (a
// deliberate same-account echo is not a guess) or legitimately differs (a
// real cross-account expected_bucket_owner is not this function's business
// either way - see [isAmbientEcho]). Only a name ABSENT from configuredSeed
// - nothing this run's configuration could produce for it, by any means
// this package already trusts - and whose value nonetheless equals the
// run's own ambient identity is scrubbed.
//
// Scoped to [residueConfigSourced]'s population for the identical protocol
// reason [classifyResidue]'s own format-only widening cites: Computed is
// the one flag that lets a provider answer independently of configuration
// at all, so a Computed candidate is left untouched here - a real,
// provider-managed value is exactly what Computed promises, and nulling
// one out would manufacture a diff a plain refresh would never show.
// ambient is [ambientIdentityValues]'s output, computed by the caller
// rather than by this function directly - [builder.ambientContext] widens
// what a single instance's own read would give
// ([classifyResidue]/[classifyResiduePaths] use the narrower, per-instance
// form directly, having no comparable cross-instance memory to draw on).
func scrubAmbientEcho(schema providers.Schema, obj cty.Value, ambient map[string]cty.Value, configuredSeed map[string]cty.Value) cty.Value {
	if schema.Block == nil || obj == cty.NilVal || obj.IsNull() || !obj.Type().IsObjectType() || obj.IsMarked() {
		return obj
	}
	if len(ambient) == 0 {
		return obj
	}
	var seed map[string]cty.Value
	for name := range residueConfigSourced(schema) {
		if _, configured := configuredSeed[name]; configured {
			continue
		}
		if !obj.Type().HasAttribute(name) {
			continue
		}
		v := obj.GetAttr(name)
		if !isAmbientEcho(v, ambient) {
			continue
		}
		if seed == nil {
			seed = make(map[string]cty.Value)
		}
		seed[name] = cty.NullVal(v.Type())
	}
	if len(seed) == 0 {
		return obj
	}
	scrubbed, ok := withSeededAttrs(obj, seed)
	if !ok {
		return obj
	}
	return scrubbed
}

// residueReader is the one provider call [classifyResidue] makes, narrowed
// to what it needs so a test can supply two answers without a provider.
type residueReader func(prior cty.Value) (cty.Value, error)

// classifyResidue decides which of candidates the provider's Read does not
// manage, by asking the provider twice with priors that differ in exactly
// those attributes.
//
// # The two reads
//
// Read A's prior is the applied object with everything but the IDENTITY set
// to null - the shape the plan path's own prior has, see [identityOnly].
// Read B's prior is the applied object unchanged. For one candidate c:
//
//	A returns null for c   =>  the provider does not source c from the
//	                           remote. If it did, it would have produced a
//	                           value here regardless of what the prior held.
//	B returns applied[c]   =>  the provider preserves whatever the prior
//	                           held for c rather than clearing it.
//
// Both together mean c's value in prior state is only ever whatever we put
// there, sourced from nothing live. That is residue, and it is the only
// combination that is.
//
// The ambiguity this resolves is the one that makes a null answer unusable
// on its own. An SDKv2 resource leaves an untouched attribute at whatever
// the prior held; a plugin-framework resource sets it to null because the
// remote genuinely lacks it. Both give null from read A. They differ in
// read B: the framework resource sets c to null there too, so B != applied,
// and c is correctly NOT recorded. Filling that one from a record is what
// would mask real drift.
//
// # No sentinel, and why that is a change from the issue's sketch
//
// Issue #275's design sketch reached for a sentinel: perturb the prior with
// an unmistakable bogus value and see whether it comes back. This does the
// same job with two priors that are both legitimate. Read B's prior is the
// object the apply just produced, which is precisely what an ordinary
// refresh passes. Read A's prior is the identity and nothing else, which is
// precisely what the plan path's own import stub passes ([importAndRead]
// reads with the bare imported object). Neither is
// a value a provider could not have been handed by an ordinary run, so
// there is no sentinel to leak into a plan or a record, no question of what
// a provider does when handed a bogus region or a bogus ARN, and no
// coincidence budget to reason about - a boolean sentinel can collide with
// the real answer, and the applied value cannot collide with itself.
//
// # Unexpected answers
//
// Every failure is closed, and nothing partial is stored:
//
//   - Either read erroring returns ok=false. Nothing is recorded; the
//     estate keeps the perpetual diff it had before, which is visible.
//   - Either read returning a null object, a non-object, or an object that
//     does not have the attribute, returns ok=false for the same reason:
//     the object under test was not there to answer, so no answer was
//     given.
//   - A candidate whose two answers do not match either pattern is simply
//     not in the result. It is not stored, and no other candidate's verdict
//     is affected by it.
//
// The caller is expected to treat ok=false as "record nothing for this
// instance", never as "record what we have".
//
// # The format-only widening (GitHub issues #395 and #376)
//
// configSourced (see [residueConfigSourced]) names the candidates whose
// value the plugin protocol's own contract says configuration is the ONLY
// thing that can ever set - Required, or Optional and never Computed. For
// exactly this population, read A producing something real (failing the
// carriesNoInformation test below) is widened to still count as residue
// PROVIDED read B - given the correct, applied value as its prior - echoes
// that value back exactly. hashicorp/aws's aws_ecs_service is the
// confirmed case: task_definition's Read reformats between the short
// "family:revision" form and the full ARN depending on whether PriorState
// already looks like an ARN, defaulting to the short form when PriorState
// carries nothing - so an identity-only prior (read A) answers with the
// short form, a real, non-null, DIFFERENT-looking value that used to fail
// the unwidened test below and be treated as unrecordable drift.
//
// The safety argument is the same one [configuredAttrsSeed] makes for
// seeding, from the opposite direction: a non-Computed attribute's TRUE
// value can never differ from configuration by the protocol's own
// contract, so read A's different-looking answer cannot be reporting real,
// independent drift - there is nothing else it could be reporting except a
// different SPELLING of the identical value. And the read-B leg is what
// keeps genuine drift from slipping through anyway: if the live object's
// task_definition had actually changed out of band, feeding it its own
// applied value as PriorState would make the provider echo the CURRENT
// wire value in that format - which is the NEW value, not applied - so
// `bv.RawEquals(want)` correctly fails and the candidate falls through to
// "real drift, do not record" exactly as before. Confirmed directly
// against a live floci + hashicorp/aws 6.59.0 standalone repro (see the
// PR description for the reproduce command): seeding PriorState with the
// correct ARN before any read at all makes the provider echo it back
// unchanged, matching this reasoning's own prediction.
func classifyResidue(applied cty.Value, candidates []string, identityAttrs map[string]bool, configSourced map[string]bool, read residueReader, ambient map[string]cty.Value) (map[string]cty.Value, bool) {
	if len(candidates) == 0 || applied == cty.NilVal || applied.IsNull() || !applied.Type().IsObjectType() {
		return nil, false
	}

	stub, err := identityOnly(applied, identityAttrs)
	if err != nil {
		return nil, false
	}

	a, err := read(stub)
	if err != nil || !usableReadResult(a) {
		return nil, false
	}
	b, err := read(applied)
	if err != nil || !usableReadResult(b) {
		return nil, false
	}

	out := make(map[string]cty.Value)
	for _, name := range candidates {
		if !a.Type().HasAttribute(name) || !b.Type().HasAttribute(name) {
			continue
		}
		av := a.GetAttr(name)
		bv := b.GetAttr(name)
		want := applied.GetAttr(name)
		log.Printf("[TRACE] projection: residue candidate %q: readA=%#v readB=%#v applied=%#v", name, av, bv, want)
		if wty := want.Type(); (wty.IsListType() || wty.IsSetType() || wty.IsMapType()) && carriesNoInformation(want) {
			// A collection-nested block (list, set or map - what
			// [residueEligibleBlock]'s NestingList/NestingSet/NestingMap
			// widening newly reaches) declared zero times has no OTHER
			// spelling: unlike a flat attribute, where the zero value (""
			// false, 0) can be a deliberately configured answer no less
			// real than any other, there is no HCL you can write that
			// "explicitly configures" a block to be present-but-empty -
			// zero occurrences and "not configured" are the same fact. So
			// an empty want here is never the case the flat-attribute
			// bypass below exists for ("an empty answer is only ever read
			// as nothing when the applied value was something", per
			// [carriesNoInformation]'s own doc comment); it is always
			// "nothing was configured", and every one of
			// aws_route53_record's six routing-policy blocks hits exactly
			// this on a plain record with none of them set. Recording an
			// empty want as residue is not wrong - filling it back
			// reproduces the same empty value a bare read already would -
			// only useless, at the cost of an IAM write and a silent
			// breach of "nothing is recorded where the provider does not
			// need it", which is this function's own stated bound. Scalar
			// and NestingSingle candidates (want is a string, number, bool
			// or object) do not take this branch: their own zero value can
			// be exactly the thing worth preserving, the way
			// aws_lambda_function.publish = false and
			// aws_security_group.revoke_rules_on_delete = false already
			// are below.
			continue
		}
		if !av.IsNull() && av.RawEquals(want) {
			// Read A produced the applied value from a prior that did not
			// carry it, so the provider reads this attribute from the
			// remote. Whatever the live answer is, it is the provider's to
			// give and not ours to remember.
			continue
		}
		if !carriesNoInformation(av) {
			// Read A produced something else real. Ordinarily that means
			// the provider manages the attribute and currently disagrees
			// with what we applied - drift, which belongs in the plan
			// rather than in a record. The one exception is the format-only
			// widening this function's own doc comment describes: for a
			// candidate configSourced names, a real-looking answer here
			// cannot be independent drift at all (the protocol's own
			// contract forbids it), so it is provisionally still eligible,
			// and the read-B check right below is what tells a genuine
			// format artifact apart from every other case that could
			// produce this shape.
			if !configSourced[name] {
				continue
			}
		}
		if bv.IsNull() || bv.IsMarked() || !bv.RawEquals(want) {
			// The provider did not preserve what the prior held, so a
			// record would not survive a read either - and, for the
			// widened branch above, this is also what proves the live
			// object has not actually drifted: fed the correct value, the
			// provider echoed something else.
			continue
		}
		if isAmbientEcho(want, ambient) {
			// GitHub issue #402. Read A returning null (the ordinary shape
			// this whole function exists to capture) usually means "an
			// SDKv2 resource only ever preserves this from whatever the
			// prior held" - true residue, worth remembering. It can ALSO
			// mean "this run's own account or region, which the provider
			// derives from its own session and would derive identically
			// from ANY prior, including an empty one" - aws_s3_bucket_
			// lifecycle_configuration's own expected_bucket_owner is the
			// confirmed case (its schema marks it Computed, so the format-
			// only widening above never even applies, and read A still
			// comes back null because that type's Read only fills the
			// field in from a genuinely-held prior, exactly as an SDKv2
			// resource's format-only case does - the two are
			// indistinguishable from read A and read B alone). Recording
			// the second shape as residue would replay a value nothing
			// ever configured on every future plan, exactly the "wrong
			// marker" this package's own header warns a stored value must
			// never be able to cause - not to identity here, but to
			// whether a plan the operator did not ask for keeps looking
			// clean. want.RawEquals(ambient) is the one signal that tells
			// the two apart without guessing what the account or region
			// IS: an account or region a run's own configuration genuinely
			// set to the SAME value (a deliberate same-account
			// expected_bucket_owner, or a genuine cross-account one that
			// simply differs from ambient) is untouched by this check -
			// see [ambientIdentityValues] and [isAmbientEcho].
			continue
		}
		out[name] = want
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// classifyResiduePaths is [classifyResidue]'s generalization to
// [residueLeafPathCandidates]' output: the identical read-A/read-B
// discriminator, asked with [cty.Path.Apply] in place of [cty.Value.GetAttr]
// so a candidate need not sit at the schema's own top level.
//
// It performs its own two reads rather than sharing [classifyResidue]'s -
// simpler to keep independent and independently testable, at the cost of
// two extra provider RPCs, and only ever paid by an instance
// [residueLeafPathCandidates] actually found something in (aws_lb_listener
// and aws_lb_listener_rule with an OIDC action configured, so far - not
// the general population).
//
// See [classifyResidue]'s own doc comment for what each side of the
// discriminator proves; nothing about the reasoning changes at a deeper
// path, only the accessor.
func classifyResiduePaths(applied cty.Value, candidates []residuePathCandidate, identityAttrs map[string]bool, read residueReader, ambient map[string]cty.Value) (map[string]cty.Value, bool) {
	if len(candidates) == 0 || applied == cty.NilVal || applied.IsNull() || !applied.Type().IsObjectType() {
		return nil, false
	}

	stub, err := identityOnly(applied, identityAttrs)
	if err != nil {
		return nil, false
	}

	a, err := read(stub)
	if err != nil || !usableReadResult(a) {
		return nil, false
	}
	b, err := read(applied)
	if err != nil || !usableReadResult(b) {
		return nil, false
	}

	out := make(map[string]cty.Value)
	for _, c := range candidates {
		want, err := c.Path.Apply(applied)
		if err != nil || want == cty.NilVal || want.IsNull() || !want.IsWhollyKnown() {
			continue
		}
		av, aerr := c.Path.Apply(a)
		if aerr != nil {
			continue
		}
		bv, berr := c.Path.Apply(b)
		if berr != nil {
			continue
		}
		log.Printf("[TRACE] projection: residue path candidate %q: readA=%#v readB=%#v applied=%#v", tfdiags.FormatCtyPath(c.Path), av, bv, want)
		if !av.IsNull() && av.RawEquals(want) {
			// Read A produced the applied value from a prior that did not
			// carry it, so the provider reads this leaf from the remote.
			continue
		}
		if !carriesNoInformation(av) {
			// Same format-only widening [classifyResidue] makes for a
			// non-Computed candidate: a real-looking answer here cannot be
			// independent drift (the protocol forbids it for a leaf
			// configuration alone can set), so it stays provisionally
			// eligible and the read-B check below tells a format artifact
			// apart from every other case that could produce this shape.
			if !c.ConfigSourced {
				continue
			}
		}
		if bv.IsNull() || bv.IsMarked() || !bv.RawEquals(want) {
			continue
		}
		if isAmbientEcho(want, ambient) {
			// GitHub issue #402's guard, at path granularity - see
			// [classifyResidue]'s identical check for the reasoning. A
			// nested leaf equal to the run's own ambient account or region
			// is exactly as unpreservable as a flat one.
			continue
		}
		key, keyErr := encodeResiduePathKey(c.Path)
		if keyErr != nil {
			continue
		}
		out[key] = want
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// classifyResidueAll is [RecordResidueForInstance]'s and
// [writeBackRecordEnvelopes]'s shared entry point: run both classifiers
// against the same schema, applied value and read closure, and merge their
// results into one map keyed by flat attribute name and by
// [encodeResiduePathKey]'s canonical path string, the input
// [encodeResidueFields] already accepts without caring which kind of key
// it was given (a bare name is never a valid JSON array, and a path key
// always is, so the two can never collide).
// identityObj is the resource's own resource-identity object - obj.Identity
// off the same read/import this instance's applied value came from, or
// cty.NilVal for a caller with none (every pre-identity provider, and every
// type an identity schema does not name) - which [ambientIdentityValues]
// turns into this run's own account and region, GitHub issue #402's guard
// against capturing either one as residue. See that function's doc comment
// for why this is not [identity.CloudContext] plumbed in from outside.
func classifyResidueAll(schema providers.Schema, applied cty.Value, secrets strict.Secrets, read residueReader, identityObj cty.Value) (map[string]cty.Value, bool) {
	if schema.Block == nil || applied == cty.NilVal || applied.IsNull() {
		return nil, false
	}
	candidates := residueCandidates(schema, applied, secrets)
	pathCandidates := residueLeafPathCandidates(schema, applied, secrets)
	if len(candidates) == 0 && len(pathCandidates) == 0 {
		return nil, false
	}
	ambient := ambientIdentityValues(schema, identityObj)
	merged := make(map[string]cty.Value)
	if len(candidates) > 0 {
		if attrs, ok := classifyResidue(applied, candidates, residueIdentityAttrs(schema), residueConfigSourced(schema), read, ambient); ok {
			for k, v := range attrs {
				merged[k] = v
			}
		}
	}
	if len(pathCandidates) > 0 {
		if attrs, ok := classifyResiduePaths(applied, pathCandidates, residueIdentityAttrs(schema), read, ambient); ok {
			for k, v := range attrs {
				merged[k] = v
			}
		}
	}
	if len(merged) == 0 {
		return nil, false
	}
	return merged, true
}

// carriesNoInformation reports whether a value is the provider's way of
// saying nothing: a null, or - for a provider built on the legacy SDK - the
// zero value of its type, which is the closest that SDK can come to a null.
//
// This is not a nicety. The floci crossing found it: with an identity-only
// prior, hashicorp/aws answers aws_lambda_function.source_code_hash with
// the EMPTY STRING rather than a null, because the legacy SDK's shim cannot
// represent an absent string. Testing only for null recorded filename and
// publish (which do come back null) and silently dropped source_code_hash,
// so the cold replan still proposed one of the three and still never
// converged. A rule that covers two thirds of a defect is a rule that has
// not been measured.
//
// The rule is applied only AFTER the "read A already produced the applied
// value" test, and that ordering is what keeps it from over-reading. An
// attribute whose applied value IS the zero value - the estate's own
// description = "" - is caught by that first test and never reaches here,
// so an empty answer is only ever read as "nothing" when the applied value
// was something.
func carriesNoInformation(v cty.Value) bool {
	if v == cty.NilVal || v.IsNull() {
		return true
	}
	if !v.IsWhollyKnown() || v.IsMarked() {
		// Neither is an answer this classifier may draw a conclusion from.
		// Treating them as "nothing" would let an unknown become a stored
		// value; the caller's own checks would then refuse the write, but
		// refusing here is where it belongs.
		return false
	}
	ty := v.Type()
	switch {
	case ty == cty.String:
		return v.AsString() == ""
	case ty == cty.Bool:
		return v.False()
	case ty == cty.Number:
		return v.RawEquals(cty.Zero)
	case ty.IsListType(), ty.IsSetType(), ty.IsMapType():
		return v.LengthInt() == 0
	}
	// Object and tuple types are deliberately not reduced to "empty". They
	// have no single zero value the legacy SDK collapses to, so an answer
	// of that shape is a real answer.
	return false
}

// usableReadResult reports whether a read's answer is an object this
// classifier may draw a conclusion from. A null answer is the provider
// saying the object is gone, which is a perfectly good answer to a
// different question and no answer at all to this one.
func usableReadResult(v cty.Value) bool {
	return v != cty.NilVal && !v.IsNull() && v.Type().IsObjectType()
}

// identityOnly returns obj with every attribute EXCEPT the identity
// replaced by a null of its own type. It is read A's prior.
//
// # Why the whole object and not just the candidates
//
// The first version of this nulled only the candidate attributes and kept
// everything else real, on the reasoning that the narrowest difference
// supports the narrowest claim. The floci crossing refuted it in one run:
// aws_lambda_function.source_code_hash came back POPULATED from that prior
// and was therefore classified as provider-managed, while the actual cold
// replan - whose prior is the bare import stub - got null for it and
// proposed the update anyway. The classifier was answering a question the
// plan does not ask.
//
// So read A's prior is the shape the plan path's own prior has. [importAndRead]
// reads with `PriorState: obj.Value` where obj is what ImportResourceState
// returned, which for an ordinary type is the identity and very little
// else. Matching it is what makes "read A returned null" mean "the cold
// replan will see null", which is the only condition the fill has to cover.
//
// It is also the SAFEST prior available rather than a riskier one, which is
// the part worth stating plainly: this is not an invented value handed to a
// provider to see what it does. It is the value every single plan this fork
// runs already hands that provider, on every resource, on every run. A
// provider that mishandled it would be failing on the ordinary path first.
//
// A provider that needs more than the identity to read - one whose Read
// consults fields the import stub does not carry - returns absent here, and
// absent is a failure the caller closes on. That under-covers such a type
// rather than misclassifying it.
func identityOnly(obj cty.Value, identityAttrs map[string]bool) (cty.Value, error) {
	if obj == cty.NilVal || obj.IsNull() || !obj.Type().IsObjectType() {
		return cty.NilVal, fmt.Errorf("cannot build an identity-only prior from a non-object value")
	}
	attrTypes := obj.Type().AttributeTypes()
	out := make(map[string]cty.Value, len(attrTypes))
	held := false
	for name, ty := range attrTypes {
		if identityAttrs[name] {
			v := obj.GetAttr(name)
			out[name] = v
			if !v.IsNull() {
				held = true
			}
			continue
		}
		out[name] = cty.NullVal(ty)
	}
	if !held {
		// Every identity attribute is null, so this prior addresses no
		// object at all. Handing it to a provider is exactly the bogus
		// input this design is built to never produce - a provider that
		// dereferences the id would panic, and one that did not would
		// answer about nothing. The caller closes on the error and records
		// nothing for the instance.
		//
		// Reached by internal/command's stateless test provider, whose
		// caricature objects carry no id, and it would be reached by any
		// real type whose applied object does not either. Such a type could
		// not be imported by OpenTofu's own import path, so nothing is lost
		// by declining to classify it.
		return cty.NilVal, fmt.Errorf("the applied object carries no identity to read by")
	}
	return cty.ObjectVal(out), nil
}

// fillResidue returns obj with every attribute recorded in attrs that the
// provider ANSWERED NOTHING FOR filled in from the record, and reports how
// many it filled.
//
// "Answered nothing" is [carriesNoInformation], the same test the
// classifier applies to read A, and the symmetry is load-bearing rather
// than tidy. The crossing found the asymmetry the expensive way: with the
// classifier relaxed and this side still testing only for null,
// source_code_hash was correctly RECORDED and then never filled, because
// the plan-time read answers it with the legacy SDK's empty string. Two
// thirds of the arguments settled and the estate still never converged.
//
// That check is the whole safety rule and it runs on every attribute, every
// time. A record never overwrites a value the provider gave: the day a
// provider release starts returning filename, or the day the value
// genuinely changes in the cloud, the live answer wins and the record is
// ignored. A record can only ever speak where the cloud said nothing.
//
// The test is applied to the READ value and never to the recorded one, and
// that asymmetry is deliberate. aws_lambda_function.publish is recorded as
// FALSE - the whole reason issue #275's plan output shows "+ publish =
// false" is that a "+" on a bool whose configuration value is false is only
// possible from a null prior - and a rule that skipped a zero-valued record
// would drop it. What "carries no information" means is "this is how a
// provider says nothing", which is a claim about a provider's answer, not
// about a value in the abstract.
//
// An attribute the current schema no longer has, or whose recorded value
// does not fit the attribute's current type, is skipped rather than
// converted. Converting would be guessing at what an older provider version
// meant, and a wrong prior-state value produces an empty plan that is
// wrong.
//
// This switch no longer asks whether schemaAttr is Required or Optional,
// symmetrically with [residueCandidates] dropping the same question: a
// record for a purely Computed attribute (aws_nat_gateway's
// regional_nat_gateway_address is the case that found this) is exactly as
// safe to fill as one for an Optional+Computed attribute, because the
// safety is the "current read carries no information" test two lines
// above, not the schema's Required/Optional/Computed shape.
//
// # The two schema flags this re-checks, and why only one of them moves
//
// Both are re-asked here rather than trusted from the classifier, because
// the two are separated in time: a record written months ago against a
// schema where an attribute was ordinary must not be applied after a
// provider release marks it.
//
// WriteOnly is refused whatever secrets says, for [residueCandidates]'
// stated reason - the protocol forbids the provider returning it, so no
// stored value could be right.
//
// Sensitive follows the setting, and it follows the setting THIS run holds
// rather than the one the record was written under. That direction is the
// safe one and it is worth saying which way it fails. An estate that
// recorded a sensitive argument and then turned secrets to "refuse" stops
// filling it, so the argument is proposed for update again - visible,
// annoying, and correctable by deleting the record. The other direction, an
// estate that turns secrets to "store" and starts filling from a record
// written when the attribute was ordinary, is filling a value this fork
// wrote for the same attribute of the same instance; nothing about the
// record's age makes it a different value.
//
// Neither flag reaches the nested-BLOCK half of the switch, and it
// is the same asymmetry [residueCandidates]' doc comment sets out: a block
// is admitted by [residueEligibleBlock], which refuses one containing
// anything sensitive or write-only under EITHER setting, so there is no
// remaining flag here for the setting to move. The re-asking is the point
// that survives - residueEligibleBlock is asked again on the way out,
// against today's schema, so a block recorded before a provider release put
// a sensitive argument inside it stops being filled that day.
//
// The caller is what puts the sensitivity mark back - see
// [builder.fillResidueFor], which re-marks from the schema after this
// returns. This function deliberately deals in unmarked values on both
// sides, which is why rec.IsMarked() is still a refusal: a marked value in
// the record store is a record this package did not write.
//
// # importStub, and GitHub issue #393
//
// importStub is [importAndRead]'s PriorState going INTO ReadResource -
// before any live read happened - or cty.NilVal when obj did not come from
// an import at all (the write-back path has no such call). It exists to
// answer one narrow question the schema cannot: for a legacy-SDK provider,
// is cur the value ReadResource actually produced, or is it merely the SDK's
// own internal schema Default that ImportResourceState seeded and
// ReadResource never touched?
//
// aws_db_instance.skip_final_snapshot is the confirmed case. It is Optional,
// not Computed, and AWS's DescribeDBInstances genuinely never returns it -
// exactly the shape [residueCandidates] exists to admit. But SDKv2's own
// schema carries `Default: true`, the opposite of the type's zero value that
// [carriesNoInformation]'s legacy-SDK convention treats as "nothing", so
// ImportResourceState's stub answers `true` before any read runs.
// [carriesNoInformation] correctly refuses to call that "nothing" on its
// own - a real, configured `true` must never be treated as absent - and
// with no provenance signal the record this estate correctly wrote (`false`)
// is outranked forever by a value the provider never actually spoke.
//
// The schema itself has nothing to say here: the plugin protocol's
// [configschema.Block] carries no Default field at all, so there is no
// third population to ask instead of the value. The one thing that IS
// available is provenance - what importAndRead handed ReadResource before
// the call, compared against what came back - and a legacy-SDK Read that
// does not source an attribute from the remote leaves the prior state's
// value for it completely alone, which means the two are bit-for-bit
// identical. A Read that DOES source the attribute has no reason to
// reproduce its input by coincidence on every single instance of the type,
// which is the population [classifyResidue] already proved this attribute
// belongs to before a record for it could exist at all - see
// [residueCandidates]'s note on why this only ever matters for a name a
// record already speaks for.
//
// This is deliberately not folded into [carriesNoInformation] itself:
// that function is also [classifyResidue]'s read-A/read-B test, whose own
// priors are two independently constructed reads with no import stub in
// the loop at all, and weakening its general zero-value rule would blur a
// case it already gets right - a genuinely-read, non-default bool must
// keep outranking a stored record. The check below is scoped to exactly
// the population this file already trusts: a name [attrs] has a record
// for, which only exists because [classifyResidue] separately proved the
// provider does not source it from the remote.
func fillResidue(obj cty.Value, block *configschema.Block, attrs map[string]cty.Value, secrets strict.Secrets, importStub cty.Value) (cty.Value, int) {
	if obj == cty.NilVal || obj.IsNull() || !obj.Type().IsObjectType() || block == nil || len(attrs) == 0 {
		return obj, 0
	}
	refusesSecrets := !strict.StoresSecrets(secrets)
	stubUsable := importStub != cty.NilVal && !importStub.IsNull() && importStub.Type().IsObjectType()
	attrTypes := obj.Type().AttributeTypes()
	out := make(map[string]cty.Value, len(attrTypes))
	filled := 0
	for name, ty := range attrTypes {
		cur := obj.GetAttr(name)
		// The information test runs on the UNMARKED value, and that is not
		// tidiness - it is what makes the whole sensitive half of the
		// secrets setting work.
		//
		// obj reaches here already carrying the schema's own sensitivity
		// marks ([importAndRead] applies them to the provider's wire answer),
		// so a Sensitive attribute's current value is marked. And
		// [carriesNoInformation] answers FALSE for any marked value on
		// purpose - it must not draw a conclusion from one - which for a
		// legacy-SDK provider means the empty string it returns for an
		// unset argument reads as "the provider answered something" instead
		// of "the provider answered nothing". The record would be written by
		// the classifier, which works on an unmarked copy, and never filled
		// by this function: the perpetual update the mechanism exists to
		// remove, surviving for exactly the population the setting was added
		// to cover.
		//
		// Unmarking for the test is safe because the test is about the
		// SHAPE of a provider's answer - null, or its type's zero value -
		// and a mark says nothing about that. cur itself, marks and all, is
		// what goes back into out below when nothing is filled.
		curPlain, _ := cur.UnmarkDeep()
		rec, recorded := attrs[name]
		schemaAttr := block.Attributes[name]
		// A name the schema carries as a nested BLOCK (any admitted nesting mode) rather than an
		// attribute is fillable on exactly the terms [residueCandidates]
		// let it be recorded on, and on no others. The two populations are
		// derived from the same schema here rather than trusted to match,
		// because the record is a file on disk that outlives the run that
		// wrote it: a record written when a name was in scope must stop
		// being filled the day the schema moves it out of scope.
		fillableBlock := schemaAttr == nil && residueEligibleBlock(block, name)
		// noInfo starts as the general zero-value/null rule and is widened,
		// ONLY for a name a record already exists for, by the provenance
		// check above - see this function's own doc comment for why the
		// widening is safe precisely because it never reaches an attribute
		// [recorded] is false for.
		noInfo := carriesNoInformation(curPlain)
		if !noInfo && recorded && stubUsable && importStub.Type().HasAttribute(name) {
			stubPlain, _ := importStub.GetAttr(name).UnmarkDeep()
			if !stubPlain.IsNull() && stubPlain.IsWhollyKnown() && stubPlain.RawEquals(curPlain) {
				noInfo = true
			}
		}
		switch {
		case !recorded, !noInfo,
			schemaAttr == nil && !fillableBlock,
			schemaAttr != nil && ((schemaAttr.Sensitive && refusesSecrets) || schemaAttr.WriteOnly),
			rec.IsNull(), !rec.IsWhollyKnown(), rec.IsMarked(),
			!rec.Type().Equals(ty):
			out[name] = cur
		default:
			out[name] = rec
			filled++
		}
	}
	if filled == 0 {
		return obj, 0
	}
	return cty.ObjectVal(out), filled
}

// fillResiduePaths is [fillResidue]'s generalization to a path-keyed
// [residueLeafPathCandidates] entry: the identical safety rule ("a record
// never overwrites a value the provider gave", [fillResidue]'s own doc
// comment) applied at the leaf a [classifyResiduePaths] candidate names,
// through [setResiduePathValues]'s single [cty.Transform] pass rather than
// a flat `for name, ty := range obj.Type().AttributeTypes()` loop, because
// the leaf this fills is not one of obj's own top-level attributes.
//
// Every attrs entry that is NOT a path key ([isResiduePathKey]) is left for
// [fillResidue] to handle, exactly as it always has - the two loops
// partition attrs by key shape and neither one's caller needs to know
// which candidates went where.
//
// The schema is re-asked at fill time through [schemaAttrAtPath], the same
// re-check [fillResidue]'s own doc comment explains for the flat case: a
// record written when a leaf was ordinary must stop being filled the day a
// provider release marks it Sensitive under `secrets = "refuse"`, or moves
// it behind WriteOnly.
//
// The mark this fills back UNMARKED is restored, deeply and for the whole
// object, by [builder.fillResidueFor]'s own trailing
// [markSchemaSensitive] call - not by anything in this function, which
// deals in unmarked values on both sides exactly as [fillResidue] does.
func fillResiduePaths(obj cty.Value, block *configschema.Block, attrs map[string]cty.Value, secrets strict.Secrets) (cty.Value, int) {
	if obj == cty.NilVal || obj.IsNull() || !obj.Type().IsObjectType() || block == nil || len(attrs) == 0 {
		return obj, 0
	}
	refusesSecrets := !strict.StoresSecrets(secrets)
	entries := make(map[string]cty.Value)
	for key, rec := range attrs {
		if !isResiduePathKey(key) {
			continue
		}
		if rec.IsNull() || !rec.IsWhollyKnown() || rec.IsMarked() {
			continue
		}
		path, err := decodeResiduePathKey(key)
		if err != nil {
			continue
		}
		attr, ok := schemaAttrAtPath(block, path)
		if !ok || attr == nil {
			continue
		}
		if attr.WriteOnly || (attr.Sensitive && refusesSecrets) {
			continue
		}
		if !rec.Type().Equals(attr.Type) {
			continue
		}
		entries[key] = rec
	}
	if len(entries) == 0 {
		return obj, 0
	}
	return setResiduePathValues(obj, entries, true)
}

// setResiduePathValues is the one [cty.Transform] pass [fillResiduePaths]
// and [withSeededAttrs] both drive, matching a node's own canonical
// [encodeResiduePathKey] against entries and replacing it when found.
//
// requireEmpty is [fillResiduePaths]' "never overwrite a value the
// provider gave" rule ([carriesNoInformation] on the CURRENT, unmarked
// node): true for the post-read fill, because obj there is a live read a
// record must never shadow. [withSeededAttrs]'s pre-read seed passes
// false, matching that function's own flat half: attrsSeed already
// overwrites unconditionally (subject only to the type check every path
// here still keeps), because obj there is an import stub built with no
// configuration in hand, not a live answer to protect.
//
// cty.Transform's own contract - visit every node bottom-up, offering each
// one a replacement - is what makes a single pass correct for however many
// path entries land in the same object: a container is rebuilt from its
// own already-transformed children before this callback ever sees it, so
// entries at unrelated paths never interact, and [cty.Transform]'s own
// unmark-before-descend, remark-after-rebuild discipline is what makes it
// safe to call directly on a value this package has already schema-marked
// (see [markSchemaSensitive]'s own doc comment on why obj reaches here
// carrying marks at all) - never [cty.Value.GetAttr] or
// [cty.Value.ElementIterator] on a marked receiver directly, which is what
// [cty.Transform] itself does the unmarking for.
func setResiduePathValues(obj cty.Value, entries map[string]cty.Value, requireEmpty bool) (cty.Value, int) {
	if obj == cty.NilVal || len(entries) == 0 {
		return obj, 0
	}
	filled := 0
	result, err := cty.Transform(obj, func(p cty.Path, v cty.Value) (cty.Value, error) {
		key, keyErr := encodeResiduePathKey(p)
		if keyErr != nil {
			return v, nil
		}
		val, ok := entries[key]
		if !ok {
			return v, nil
		}
		if !val.Type().Equals(v.Type()) {
			return v, nil
		}
		if requireEmpty {
			curPlain, _ := v.UnmarkDeep()
			if !carriesNoInformation(curPlain) {
				return v, nil
			}
		}
		filled++
		return val, nil
	})
	if err != nil {
		return obj, 0
	}
	if filled == 0 {
		return obj, 0
	}
	return result, filled
}
