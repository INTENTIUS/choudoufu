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
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/staterecord"
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

// residueNamespaceRoot is the literal segment every residue key lives under.
//
// A different literal from [recordNamespaceRoot] ("tofu-records"),
// hint_store.go's "tofu-hints", [locatedNamespaceRoot] ("tofu-located") and
// live/RECEIPTS.md's "tofu-receipts", for the reason located.go gives about
// its own root: the enumeration that feeds the destroy path lists the
// RECORD root, so anything that must never be destroyable has to live
// somewhere that listing physically cannot reach.
//
// Like the located root it is NOT derived from a record_store block's
// key_prefix override, and internal/configs' validateRecordStoreKeyPrefix
// closes the other direction by refusing an override rooted here.
const residueNamespaceRoot = "tofu-residue"

// ResidueKeyPrefix is the key namespace one estate's residue records live
// under. Exported for the same reason [RecordKeyPrefix], [HintKey] and
// [LocatedKeyPrefix] are: internal/command's store construction and this
// package's own namespace-safety tests both have to name one definition.
func ResidueKeyPrefix(estate string) string {
	return residueNamespaceRoot + "/" + estate
}

// ResidueKey is the store key one resource instance's residue lives at, for
// estate.
//
// The address is encoded exactly as [RecordKey] and [LocatedKey] encode it,
// by [recordKeyEncoding], because the constraint is the same: a for_each key
// can carry characters SSM parameter names and S3 object keys forbid.
// Sharing the encoding is safe where sharing the ROOT would not be.
//
// There is deliberately no reverse of this function, for [LocatedKey]'s
// reason: a reverse exists so that a LISTING can recover an address, and
// building one here would be building the first half of a destroy path this
// file exists to keep unbuilt.
func ResidueKey(estate string, addr addrs.AbsResourceInstance) string {
	return ResidueKeyPrefix(estate) + "/" + addr.Resource.Resource.Type + "/" + recordKeyEncoding.EncodeToString([]byte(addr.String()))
}

// residueFormatVersion identifies the JSON shape [residuePayload] writes.
// An unrecognized version is refused rather than guessed at, and a refused
// residue record simply leaves the attributes null - the plan proposes the
// update it proposed before this mechanism existed, which is visible and
// annoying rather than wrong.
const residueFormatVersion = "tofu-live-residue-v1"

// residueAttrValue is one recorded attribute: the cty type it was recorded
// at, and its value at that type, both in cty's own JSON encoding.
//
// The type travels with the value for [recordPayload]'s reason - a value
// with no type is not decodable - but PER ATTRIBUTE rather than once for
// the object, because a residue record is deliberately a handful of named
// attributes and not a copy of the object. A whole-object copy is what
// issue #73 removes; recording one is how this mechanism would quietly
// become a second state file.
type residueAttrValue struct {
	Type  json.RawMessage `json:"attrType"`
	Value json.RawMessage `json:"attrValue"`
}

// residuePayload is what one instance's residue key holds.
//
// Note that it shares no field name with [recordPayload]: it has no
// value_type, no attrs, no private and no status. That is what makes
// [decodeRecordPayload] refuse it outright rather than half-read it, which
// is the third of the three independent stops between a residue key and the
// destroy path.
type residuePayload struct {
	// FormatVersion is always [residueFormatVersion].
	FormatVersion string `json:"formatVersion"`

	// Address is the instance address this residue is for, written out in
	// full. Redundant with the key, and deliberately so: [ResidueStore.Get]
	// checks the two agree, so a key copied or hand-edited into pointing at
	// another resource is refused rather than answering with another
	// resource's argument values.
	Address string `json:"address"`

	// Attributes maps a top-level attribute name to the value this estate
	// last sent for it. Only attributes [classifyResidue] proved the
	// provider's Read does not manage are ever in here.
	Attributes map[string]residueAttrValue `json:"attributes"`
}

// ResidueStore is the point-lookup view of an estate's residue records.
//
// It is NOT a [staterecord.Store] and does not embed one, for the reason
// [LocatedStore]'s doc comment sets out in full: no List, no Keys, no
// iteration of any kind, and every method keyed by an
// [addrs.AbsResourceInstance] the caller must already hold. "What else is
// in here" is not a question this type can be asked, so the enumeration
// that feeds the destroy path cannot be handed one and cannot be given a
// residue key by one.
type ResidueStore struct {
	store  staterecord.Store
	estate string
}

// NewResidueStore wraps store as estate's residue store. store is
// ordinarily the same [staterecord.Store] the run's record-backed and
// record-located resources use - one store, three disjoint namespaces in
// it.
//
// A nil store yields a nil ResidueStore, so a run with no record_store
// declared has nowhere to keep residue and keeps none. That estate still
// works exactly as it did before this mechanism existed: the perpetual
// update is proposed on every cold replan, which is visible.
func NewResidueStore(store staterecord.Store, estate string) *ResidueStore {
	if store == nil || estate == "" {
		return nil
	}
	return &ResidueStore{store: store, estate: estate}
}

// Get reads the residue recorded for addr, as a map from attribute name to
// the value this estate last sent. exists is false when nothing has been
// recorded, which is the ordinary "not created yet, or created before this
// mechanism existed" answer and is not an error.
//
// version is the store's version for the key, for a later conditional Put
// or Delete.
func (s *ResidueStore) Get(ctx context.Context, addr addrs.AbsResourceInstance) (attrs map[string]cty.Value, version string, exists bool, err error) {
	if s == nil {
		return nil, "", false, nil
	}
	payload, version, exists, err := s.store.Get(ctx, ResidueKey(s.estate, addr))
	if err != nil {
		return nil, "", false, fmt.Errorf("reading the residue record for %s: %w", addr, err)
	}
	if !exists {
		return nil, "", false, nil
	}
	var rec residuePayload
	if err := json.Unmarshal(payload, &rec); err != nil {
		return nil, "", false, fmt.Errorf("decoding the residue record for %s: %w", addr, err)
	}
	if rec.FormatVersion != residueFormatVersion {
		return nil, "", false, fmt.Errorf("the residue record for %s names format %q, which this version of choudoufu does not understand", addr, rec.FormatVersion)
	}
	if rec.Address != addr.String() {
		// The key said one address and the payload says another. Refusing
		// is the only safe answer, for [LocatedStore.Get]'s reason: filling
		// this instance's prior state from another resource's arguments
		// would produce an empty plan that is wrong, and an empty plan that
		// is wrong is invisible to every verdict-level check.
		return nil, "", false, fmt.Errorf("the residue record stored for %s says it is for %s; refusing to fill one resource's prior state from another's", addr, rec.Address)
	}
	out := make(map[string]cty.Value, len(rec.Attributes))
	for name, raw := range rec.Attributes {
		ty, err := ctyjson.UnmarshalType(raw.Type)
		if err != nil {
			return nil, "", false, fmt.Errorf("the residue record for %s records attribute %q at a type that could not be read: %w", addr, name, err)
		}
		val, err := ctyjson.Unmarshal(raw.Value, ty)
		if err != nil {
			return nil, "", false, fmt.Errorf("the residue record for %s records attribute %q at a value that could not be read: %w", addr, name, err)
		}
		out[name] = val
	}
	return out, version, true, nil
}

// Put records attrs as addr's residue, conditional on the key's current
// version - expectedVersion "" asserting that nothing is recorded yet, the
// same convention [staterecord.Store.PutIfVersion] uses. It returns the new
// version.
//
// An empty attrs is refused rather than written: the classifier returning
// nothing means it proved nothing, and a key holding nothing is
// indistinguishable from a key holding "this instance has no residue",
// which is a claim nothing here is entitled to make.
func (s *ResidueStore) Put(ctx context.Context, addr addrs.AbsResourceInstance, attrs map[string]cty.Value, expectedVersion string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("no record store is configured, so %s's residue cannot be recorded", addr)
	}
	if len(attrs) == 0 {
		return "", fmt.Errorf("refusing to record an empty residue for %s", addr)
	}
	rec := residuePayload{
		FormatVersion: residueFormatVersion,
		Address:       addr.String(),
		Attributes:    make(map[string]residueAttrValue, len(attrs)),
	}
	for _, name := range sortedNames(attrs) {
		val := attrs[name]
		if val.IsNull() || !val.IsWhollyKnown() {
			// Neither is storable and neither should have got here: a null
			// carries nothing and an unknown is a plan-time placeholder,
			// not an applied value. Refusing rather than skipping, because
			// a silent skip here writes a record that is missing exactly
			// the attribute the caller believed it had stored.
			return "", fmt.Errorf("refusing to record attribute %q of %s: its applied value is null or not wholly known", name, addr)
		}
		if val.IsMarked() {
			// A marked value is sensitive by the schema's own account.
			// classifyResidue already excludes Sensitive attributes, so
			// reaching here means the marks came from somewhere else; the
			// no-secrets rule applies either way.
			return "", fmt.Errorf("refusing to record attribute %q of %s: its value is marked sensitive", name, addr)
		}
		ty := val.Type()
		tyRaw, err := ctyjson.MarshalType(ty)
		if err != nil {
			return "", fmt.Errorf("encoding the type of attribute %q of %s: %w", name, addr, err)
		}
		valRaw, err := ctyjson.Marshal(val, ty)
		if err != nil {
			return "", fmt.Errorf("encoding attribute %q of %s: %w", name, addr, err)
		}
		rec.Attributes[name] = residueAttrValue{Type: tyRaw, Value: valRaw}
	}
	payload, err := json.Marshal(rec)
	if err != nil {
		return "", fmt.Errorf("encoding the residue record for %s: %w", addr, err)
	}
	return s.store.PutIfVersion(ctx, ResidueKey(s.estate, addr), payload, expectedVersion)
}

// Delete removes addr's residue record, conditional on expectedVersion.
//
// Deleting a residue record is not deleting anything in the cloud, and
// cannot be: it is a note about arguments, and the object it describes is
// destroyed by the ordinary apply through the provider. The reverse case -
// a residue key with no configuration behind it - deliberately reaches
// nothing, because nothing enumerates this namespace. Such a key is inert:
// it costs a few bytes and is overwritten the next time the same address is
// applied.
func (s *ResidueStore) Delete(ctx context.Context, addr addrs.AbsResourceInstance, expectedVersion string) error {
	if s == nil {
		return nil
	}
	return s.store.Delete(ctx, ResidueKey(s.estate, addr), expectedVersion)
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
// Every failure here is a WARNING and never an error, and the run continues
// with the object exactly as the provider returned it. That is the whole
// difference in stakes between this store and the other two: a located
// record that cannot be read means an instance is bound to no object or to
// the wrong one, so it stops the run; a residue record that cannot be read
// means the estate proposes an update it has proposed on every run since it
// was written. The second is annoying and completely visible. Stopping a
// run over it would be trading a visible nuisance for an outage.
func (b *builder) fillResidueFor(ctx context.Context, addr addrs.AbsResourceInstance, schema providers.Schema, obj *states.ResourceInstanceObject) {
	if b.opts.ResidueStore == nil || obj == nil || schema.Block == nil {
		return
	}
	attrs, version, exists, err := b.opts.ResidueStore.Get(ctx, addr)
	if err != nil {
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryResidueUnreadable, fmt.Sprintf(
			"The residue record for %s could not be read: %s. The plan continues from what the provider returned, so any argument the provider does not give back will be proposed for update again on this run and every later one. Nothing was changed and nothing was written.",
			addr, err,
		)))
		return
	}
	if !exists {
		return
	}
	b.residueVersions = append(b.residueVersions, RecordVersion{Addr: addr, Version: version})

	filled, n := fillResidue(obj.Value, schema.Block, attrs)
	if n == 0 {
		return
	}
	obj.Value = filled
	log.Printf("[TRACE] projection: filled %d residue attribute(s) of %s from the record store", n, addr)
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
//   - Credential material, by [identity.CredentialMaterial] - the one
//     sanctioned exclusion, re-asked here rather than restated, so that a
//     type whose schema grows a secret drops out the day it does.
//   - Sensitive and write-only attributes individually, which is the same
//     rule at attribute granularity. internal/live/lint's
//     CheckResidueAttributes already warns that these can never round-trip;
//     this is why they still cannot. A write-only value the protocol forbids
//     a provider returning is not made returnable by writing it down, and a
//     sensitive value must not be in a record at all.
//   - The identity: "id" and every attribute the provider's identity schema
//     names. Those are what say WHICH object this is, they are answered by
//     the marker and the import, and a residue record must never be in a
//     position to move an instance onto a different object.
//
// Nested object ATTRIBUTES (attr.NestedType) are out of scope, and so is
// every block whose nesting mode is a list, a set, a map or a group. That
// is a stated bound rather than an oversight, and the bound's reason is
// specific: the classifier's discriminator compares a whole attribute value
// before and after, which a collection-nested block has no stable
// per-element form for. aws_lambda_function.vpc_config is the case that
// would want the collection half; on the crossing that ran for issue #275
// it is also a floci gap, so nothing yet demands it.
//
// [configschema.NestingSingle] blocks ARE in scope, because the bound's
// reason does not reach them. A single-nested block is one value in the
// implied object type, exactly like a flat attribute, so "compare the whole
// value before and after" is as well defined for it as for a string. The
// crossing that demanded it is corpus-rds-complete-postgres: terraform-aws-
// modules writes `timeouts { create = "10m" delete = "15m" }` on its
// security group and its default route table, a block the provider's Read
// never sources from the remote and only ever preserves from the prior it
// was handed. A stock state file holds it; a stateless prior state had
// nowhere to hold it, so every replan after a clean migrate proposed
// `+ timeouts {...}` on those instances forever, against a stock plan that
// shows the same block unchanged. Nothing about the rule names a type or a
// block: `timeouts` is simply the single-nested block hashicorp/aws puts on
// most of its resources, and any other config-only single-nested block
// rides the same path.
//
// Safety is unchanged and still comes from [classifyResidue], not from
// here. A single-nested block the provider really does source from the
// remote fails read A's test; one the provider does not preserve fails read
// B's. aws_default_network_acl's `egress`/`ingress` are the worked example
// of why widening the filter does not silence drift: they are
// [configschema.NestingSet], so they never reach this list at all - and
// even if they did, floci returning no rules makes read B disagree with the
// applied value, which is a skip.
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
func residueCandidates(schema providers.Schema, applied cty.Value) []string {
	if schema.Block == nil || applied == cty.NilVal || applied.IsNull() || !applied.Type().IsObjectType() {
		return nil
	}
	if identity.CredentialMaterial(schema.Block) {
		return nil
	}
	identityAttrs := residueIdentityAttrs(schema)

	var out []string
	for name, attr := range schema.Block.Attributes {
		if attr == nil || identityAttrs[name] {
			continue
		}
		if attr.Sensitive || attr.WriteOnly {
			continue
		}
		if attr.NestedType != nil {
			continue
		}
		if !applied.Type().HasAttribute(name) {
			continue
		}
		v := applied.GetAttr(name)
		if v.IsNull() || !v.IsWhollyKnown() || v.IsMarked() {
			continue
		}
		out = append(out, name)
	}
	for name := range schema.Block.BlockTypes {
		if identityAttrs[name] || !singleNestedResidueBlock(schema.Block, name) {
			continue
		}
		if !applied.Type().HasAttribute(name) {
			continue
		}
		v := applied.GetAttr(name)
		if v.IsNull() || !v.IsWhollyKnown() || v.IsMarked() {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// singleNestedResidueBlock reports whether name is a block type on this
// schema that residue may carry, and it is the ONE place that question is
// answered - [residueCandidates] asks it to decide what may be recorded and
// [fillResidue] asks it to decide what may be filled, so the two populations
// cannot drift apart.
//
// Two conditions, both from [residueCandidates]'s doc comment:
//
//   - The nesting mode is [configschema.NestingSingle], so the block is one
//     value in the implied object type and the classifier's whole-value
//     discriminator is defined for it. NestingGroup is excluded along with
//     the collections: it also renders as a single value, but an absent
//     group reads back as a block full of zero values rather than a null,
//     so "read A carries no information" cannot be told apart from "the
//     group is really empty".
//   - Nothing sensitive or write-only anywhere inside it. That is the same
//     rule the flat-attribute filter applies through attr.Sensitive and
//     attr.WriteOnly, asked over a whole nested schema because a block has
//     no single flag to read.
func singleNestedResidueBlock(block *configschema.Block, name string) bool {
	if block == nil {
		return false
	}
	blk := block.BlockTypes[name]
	if blk == nil || blk.Nesting != configschema.NestingSingle {
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
func RecordResidueForInstance(ctx context.Context, store *ResidueStore, addr addrs.AbsResourceInstance, schema providers.Schema, applied cty.Value, read func(prior cty.Value) (cty.Value, error)) (recorded bool, err error) {
	if store == nil || schema.Block == nil || applied == cty.NilVal || applied.IsNull() {
		return false, nil
	}
	candidates := residueCandidates(schema, applied)
	if len(candidates) == 0 {
		return false, nil
	}
	attrs, ok := classifyResidue(applied, candidates, residueIdentityAttrs(schema), read)
	if !ok {
		return false, nil
	}
	// Read-before-write rather than a version this call was handed: unlike
	// [writeBackResidue], which already tracked a prior-plan version through
	// [builder.fillResidueFor], a migrate has never read this estate's
	// residue store before, so there is nothing to have tracked. A second
	// live-import run over the same state (documented as idempotent) is what
	// makes this matter - without a fresh Get, its Put would always assert
	// "nothing recorded yet" and fail every time but the first.
	_, version, _, getErr := store.Get(ctx, addr)
	if getErr != nil {
		return false, fmt.Errorf("reading the existing residue record for %s before writing: %w", addr, getErr)
	}
	if _, err := store.Put(ctx, addr, attrs, version); err != nil {
		return false, fmt.Errorf("recording residue for %s: %w", addr, err)
	}
	return true, nil
}

// writeBackResidue is [WriteBack]'s residue half: after an apply, every
// marker-managed instance in the final state is put to its provider twice
// (see [classifyResidue]) and whatever the two answers prove the provider
// does not manage is recorded, so the NEXT cold replan has somewhere to
// read it from.
//
// This is the half that makes the mechanism converge in one run rather than
// two. The plan side only reads; if nothing wrote on the first apply, the
// first cold replan after it would still propose the phantom update and the
// operator would have to apply twice to settle an estate. The maintainer's
// ruling on issue #275 is that a single apply settles it, which is what
// classifying here rather than on the next plan buys.
//
// # What it costs
//
// Two extra ReadResource calls per applied instance that has any candidate
// attribute at all, stated plainly because it is not free. Nothing is
// skipped to hide that: an instance whose provider answers both reads gets
// both reads. The alternative measured against issue #275 was a static
// rule, and there is none - the whole of hashicorp/aws 6.59.0 was searched
// for one.
//
// # Every failure is closed
//
// A missing provider, a failing read, an undecodable object, a
// classification that proves nothing: all of them record NOTHING for that
// instance and leave any existing record alone. An estate that cannot be
// classified keeps exactly the behavior it had before this mechanism
// existed, which is a visible perpetual diff. It never gets a half-filled
// record, because a wrong stored value produces an EMPTY plan that is
// wrong, and that is invisible.
//
// # What is deleted
//
// Only the residue of an address the plan saw and the final state no
// longer has - the resource was destroyed. An address still present whose
// classification failed this time keeps its previous record: it describes
// what some earlier apply sent, and if this apply sent something else the
// configuration and the record simply disagree, which proposes an update.
// That is self-healing on the next successful classification, and losing
// the record would not be.
func writeBackResidue(ctx context.Context, req WriteBackRequest) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	if req.ResidueStore == nil {
		return diags
	}
	if req.Providers == nil {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryResidueNotClassified,
			"This estate declares a record_store, but the apply had no provider access to classify with, so no argument values were recorded. Arguments the provider's read does not return will be proposed for update again on the next plan. Nothing was changed and nothing was written.",
		))
		return diags
	}

	seen := make(map[string]bool, len(req.ResidueVersions))
	cache := map[string]providers.Interface{}

	if req.FinalState != nil {
		for _, entry := range req.FinalState.AllResourceInstanceObjectAddrs() {
			if entry.DeposedKey != states.NotDeposed {
				// A deposed object is a mid-replace leftover the graph is
				// about to discard; the current object is what this
				// address's arguments should be recorded from.
				continue
			}
			addr := entry.Instance
			typeName := addr.Resource.Resource.Type

			if ti, ok := identity.LookupType(typeName); ok && ti.RecordBacked {
				// A record-backed instance has no cloud object and no
				// provider read at all - record.go persists the WHOLE
				// object for it. There is nothing here for this mechanism
				// to add and no read to classify with.
				continue
			}

			res := req.FinalState.Resource(addr.ContainingResource())
			if res == nil {
				continue
			}
			ri := res.Instance(addr.Resource.Key)
			if ri == nil || ri.Current == nil {
				continue
			}
			schemaPtr, _ := req.Schemas.ResourceTypeConfig(res.ProviderConfig.Provider, addrs.ManagedResourceMode, typeName)
			if schemaPtr == nil || schemaPtr.Block == nil {
				continue
			}
			schema := *schemaPtr

			// Marked before unmarked, and in that order: candidacy is
			// decided on the value that still carries its marks, so a
			// sensitive attribute is excluded by the mark as well as by the
			// schema flag, and only then is an unmarked copy made for the
			// provider - a marked value cannot cross the plugin channel.
			obj, err := ri.Current.Decode(schema.Block.ImpliedType())
			if err != nil {
				continue
			}
			seen[addr.String()] = true

			candidates := residueCandidates(schema, obj.Value)
			if len(candidates) == 0 {
				continue
			}
			applied, _ := obj.Value.UnmarkDeep()

			provider, err := residueProvider(ctx, req.Providers, cache, res.ProviderConfig)
			if err != nil {
				diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryResidueNotClassified, fmt.Sprintf(
					"No argument values were recorded for %s: provider %s could not be reached to classify them (%s). Arguments the provider's read does not return will be proposed for update again on the next plan. Nothing was changed and nothing was written.",
					addr, res.ProviderConfig, err,
				)))
				continue
			}

			attrs, ok := classifyResidue(applied, candidates, residueIdentityAttrs(schema), func(prior cty.Value) (cty.Value, error) {
				resp := provider.ReadResource(ctx, providers.ReadResourceRequest{
					TypeName:   typeName,
					PriorState: prior,
					Private:    obj.Private,
					// The same null-of-dynamic the plan path passes, and
					// for the same reason: the plugin client marshals
					// ProviderMeta whenever the provider declares a
					// provider_meta schema, and a value with no type at all
					// panics the conformance check.
					ProviderMeta:  cty.NullVal(cty.DynamicPseudoType),
					PriorIdentity: obj.Identity,
				})
				if resp.Diagnostics.HasErrors() {
					return cty.NilVal, resp.Diagnostics.Err()
				}
				return resp.NewState, nil
			})
			if !ok {
				continue
			}

			if _, err := req.ResidueStore.Put(ctx, addr, attrs, priorVersion(req.ResidueVersions, addr)); err != nil {
				diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryResidueNotClassified, fmt.Sprintf(
					"The argument values classified for %s could not be recorded: %s. Those arguments will be proposed for update again on the next plan. Nothing in the live system was changed.",
					addr, err,
				)))
			}
		}
	}

	for _, rv := range req.ResidueVersions {
		if seen[rv.Addr.String()] {
			continue
		}
		if err := req.ResidueStore.Delete(ctx, rv.Addr, rv.Version); err != nil {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryResidueNotClassified, fmt.Sprintf(
				"The residue record for %s, whose resource this run destroyed, could not be deleted: %s. The key is inert - nothing enumerates this namespace and nothing reads a residue record for an address the configuration does not declare - but it will be overwritten rather than removed if the address is ever applied again.",
				rv.Addr, err,
			)))
		}
	}

	return diags
}

// SummaryResidueNotClassified is the summary of the warning
// [writeBackResidue] raises when an apply could not record what it sent.
// Named for [SummaryLocatedNoStore]'s reason.
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
func classifyResidue(applied cty.Value, candidates []string, identityAttrs map[string]bool, read residueReader) (map[string]cty.Value, bool) {
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
		if !av.IsNull() && av.RawEquals(want) {
			// Read A produced the applied value from a prior that did not
			// carry it, so the provider reads this attribute from the
			// remote. Whatever the live answer is, it is the provider's to
			// give and not ours to remember.
			continue
		}
		if !carriesNoInformation(av) {
			// Read A produced something else real. The provider manages the
			// attribute and currently disagrees with what we applied, which
			// is drift and belongs in the plan rather than in a record.
			continue
		}
		if bv.IsNull() || bv.IsMarked() || !bv.RawEquals(want) {
			// The provider did not preserve what the prior held, so a
			// record would not survive a read either.
			continue
		}
		out[name] = want
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
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
func fillResidue(obj cty.Value, block *configschema.Block, attrs map[string]cty.Value) (cty.Value, int) {
	if obj == cty.NilVal || obj.IsNull() || !obj.Type().IsObjectType() || block == nil || len(attrs) == 0 {
		return obj, 0
	}
	attrTypes := obj.Type().AttributeTypes()
	out := make(map[string]cty.Value, len(attrTypes))
	filled := 0
	for name, ty := range attrTypes {
		cur := obj.GetAttr(name)
		rec, recorded := attrs[name]
		schemaAttr := block.Attributes[name]
		// A name the schema carries as a single-nested BLOCK rather than an
		// attribute is fillable on exactly the terms [residueCandidates]
		// let it be recorded on, and on no others. The two populations are
		// derived from the same schema here rather than trusted to match,
		// because the record is a file on disk that outlives the run that
		// wrote it: a record written when a name was in scope must stop
		// being filled the day the schema moves it out of scope.
		fillableBlock := schemaAttr == nil && singleNestedResidueBlock(block, name)
		switch {
		case !recorded, !carriesNoInformation(cur),
			schemaAttr == nil && !fillableBlock,
			schemaAttr != nil && (schemaAttr.Sensitive || schemaAttr.WriteOnly),
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
