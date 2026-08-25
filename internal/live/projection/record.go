// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/states"
)

// recordNamespaceRoot is the literal segment every record-backed key lives
// under, in both the local store's directory layout and the SSM/S3 backends'
// key hierarchy. It is a different literal from live/RECEIPTS.md's
// "tofu-receipts" segment on purpose: namespace safety between the two is
// disjoint by construction here, not by a runtime check that could be wrong.
// See RecordKeyPrefix and internal/configs/live.go's
// validateRecordStoreKeyPrefix, which enforces the same disjointness for an
// operator-supplied key_prefix override.
const recordNamespaceRoot = "tofu-records"

// RecordKeyPrefix is the default key namespace for one estate's
// record-backed resources: every key a [staterecord.Store] is asked for by
// this package starts here unless a record_store block's key_prefix
// overrides it. Exported so internal/command's store construction and this
// package's own namespace-safety tests can both start from the one
// definition.
func RecordKeyPrefix(estate string) string {
	return recordNamespaceRoot + "/" + estate
}

// recordKeyEncoding is the alphabet RecordKey encodes an address string
// with: unpadded, URL-safe base64. Its whole output charset
// ("A-Za-z0-9_-") is a subset of every backend's allowed key characters
// (SSM parameter names, S3 object keys, filesystem paths), and - unlike
// hex-of-a-hash - it is reversible, which orphan discovery
// (builder.discoverOrphanedRecords) depends on: given only a store's List
// of its own keys, with no configuration and no marker to read, the
// address itself has to be recoverable from the key or a removed resource
// block's still-persisted record could never be found again.
var recordKeyEncoding = base64.RawURLEncoding

// RecordKey is the store key for one record-backed resource instance,
// rooted at prefix (ordinarily [RecordKeyPrefix]'s output, or a
// record_store block's key_prefix override).
//
// The instance address is not used verbatim: a for_each key can carry
// characters SSM parameter names and S3 object keys either forbid ("[",
// "]", the quotes around a string key). It is base64url-encoded instead
// (see recordKeyEncoding), reversible by [RecordAddr]. The resource type
// name is kept as a readable path segment ahead of the encoded address
// (type names are always "[a-z0-9_]+", already safe everywhere) purely
// for a human skimming a store's key listing - [RecordAddr] does not
// trust it and reads the address out of the encoded segment alone.
func RecordKey(prefix string, addr addrs.AbsResourceInstance) string {
	return prefix + "/" + addr.Resource.Resource.Type + "/" + recordKeyEncoding.EncodeToString([]byte(addr.String()))
}

// RecordAddr reverses [RecordKey]: given a key this package produced
// (typically from [staterecord.Store.List]) and the prefix it was built
// under, recovers the resource instance address. The second return is
// false for a key that does not start with prefix or whose last segment
// does not decode to a valid address - which any key this package did not
// itself write is free to be, since a store's namespace is not guaranteed
// to hold only this package's keys forever.
func RecordAddr(prefix, key string) (addrs.AbsResourceInstance, bool) {
	rest := strings.TrimPrefix(key, prefix+"/")
	if rest == key {
		return addrs.AbsResourceInstance{}, false
	}
	i := strings.LastIndex(rest, "/")
	if i < 0 {
		return addrs.AbsResourceInstance{}, false
	}
	encoded := rest[i+1:]
	raw, err := recordKeyEncoding.DecodeString(encoded)
	if err != nil {
		return addrs.AbsResourceInstance{}, false
	}
	addr, diags := addrs.ParseAbsResourceInstanceStr(string(raw))
	if diags.HasErrors() {
		return addrs.AbsResourceInstance{}, false
	}
	return addr, true
}

// GitHub issue #364 unit A1: what used to be four disjoint namespaces under
// four different root literals ("tofu-records" for a record-backed value,
// "tofu-located" for an import identity, "tofu-residue" for argument values
// a provider's read never gives back, "tofu-provisioned" for the
// create-time-provisioner taint bit) is now one key per instance, one JSON
// envelope carrying up to four independently-optional facts about it. See
// rfc/20260823-foundation-order-ruling.md and HANDOFF.md's "The foundation".
//
// # Why one key rather than four
//
// The four were disjoint because exactly one of them - the record
// namespace - is ever enumerated (builder.discoverOrphanedRecords), and a
// key the other three's data could land under would be proposed for
// destruction on nothing more than an operator's tag budget or a shell
// command's exit code. That hazard survives the merge: it is now the
// envelope's own "kind" field that decides delete authority, checked by
// every reader and never by which literal a key starts with. See
// [recordKindObject] and [recordKindIdentity].
//
// # The v1 story
//
// A payload with no "format_version" and no "kind" is what every
// record-backed instance's value looked like before this envelope existed
// (recordPayload, before this file's rewrite): a flat object holding
// "value_type", "attrs", "private", "sensitive_attributes" and "status" at
// its own top level, with no "address" field at all (the key's own base64
// segment was the only thing tying it to an instance). [decodeEnvelope]
// recognizes that exact shape and folds it into [recordEnvelope.Object],
// which is what "a v1 payload decodes as kind=object" means in practice:
// every reader downstream of decodeEnvelope sees one shape regardless of
// when a record was written, and nothing in this package ever writes v1
// bytes again - every write goes out as format_version 2.
const envelopeFormatVersion = 2

const (
	// recordKindObject marks a key whose Object member IS the resource
	// instance - GitHub issue #73's record-backed resources, which have no
	// cloud object at all. Only this kind is ever delete authority:
	// builder.discoverOrphanedRecords proposes destroying a kind=object key
	// with no configuration behind it, exactly as it always has.
	recordKindObject = "object"

	// recordKindIdentity marks every other key this package writes: an
	// import identity (issue #270), argument residue (issue #275),
	// provisioner taint (issue #353), or any combination of the three for
	// one instance whose cloud object - if it has one at all - is never
	// authorized by this record. builder.discoverOrphanedRecords ignores a
	// kind=identity key with no configuration exactly as it ignored an
	// undeclared located key before this envelope existed (GitHub issue
	// #270's ruling).
	recordKindIdentity = "identity"
)

// identityPayload is [recordEnvelope.Identity]: the answer to "which live
// object is this" for an instance whose type carries no ownership marker
// (today's locatedPayload). For most of [identity.LocatedIdentityPlanFor]'s
// three-way shape exactly one of ImportID or Attrs is populated - which
// [LocatedRecordFrom] reads - but GitHub issue #401 family 1 is a deliberate
// exception: a type whose wire identity schema names nothing better than
// the bare "id" default (LocatedRecordFrom's own default branch) may still
// carry a schema-fallback-synthesized identity ([schemaFallbackComponentsRecord]),
// and that writer populates BOTH fields at once - ImportID stays the bare
// id a genuine ReadResource PriorState needs, and Attrs carries the real
// identity components a record-first stub ([builder.recordFirstStubValues])
// could otherwise never recover. Nothing about the envelope shape changed
// for this: both fields were always independently optional JSON keys.
type identityPayload struct {
	// ImportID is the provider's import identity string for the live
	// object this instance owns, for a type whose identity is one
	// server-minted string.
	ImportID string `json:"import_id,omitempty"`

	// Attrs is the same answer for a type whose identity is COMPOSITE - one
	// string per identity-schema attribute, named as the provider's own
	// identity schema names them.
	Attrs map[string]string `json:"attrs,omitempty"`
}

func (p *identityPayload) empty() bool {
	return p == nil || (p.ImportID == "" && len(p.Attrs) == 0)
}

// deposedFields is one entry of [recordEnvelope.Deposed]: GitHub issue
// #361's crash-window recovery for a single deposed object.
type deposedFields struct {
	// Identity is the deposed object's own identity, the same shape a
	// current object's Identity member carries. Nil when the pass that
	// found this deposed object could not derive one - see writeback.go's
	// diffDeposedForWrite - which still leaves Provider below recoverable;
	// an entry with neither populated is never written (see empty()) and
	// is instead simply not added.
	Identity *identityPayload `json:"identity,omitempty"`

	// Provider is this deposed object's OWN managing provider
	// configuration ([addrs.AbsProviderConfig.String]), independent of the
	// envelope's top-level Provider field - which names the CURRENT
	// object's provider, and may already have moved on (a provider alias
	// change, a moved provider block) by the time this deposed entry is
	// read.
	Provider string `json:"provider,omitempty"`
}

func (d *deposedFields) empty() bool {
	return d == nil || (d.Identity.empty() && d.Provider == "")
}

// tombstoneFields is one entry of [recordEnvelope.Tombstone]: a live object
// this address currently claimed, destroyed by an apply THIS estate ran
// (maintainer ruling 2026-08-25, corpus-ec2-instance-complete's day2_remove
// unit). Written by [RecordStore.tombstone] in place of the plain delete
// [WriteBack] otherwise does when an address drops out of the final state
// entirely - a live object's own tags can stay visible via the tagging API
// for a time after it is terminated (real AWS's own documented lag,
// confirmed directly against the emulator with no tofu in the loop, not a
// floci gap), and a hard-deleted record left [classifyOrphans]'s
// collision guard nothing to tell that lingering tag apart from a genuine
// second live claimant. An entry naming an identity this store has since
// seen destroyed lets that guard treat the lingering tag as what it is - a
// known-destroyed object, never a live claim - rather than refusing the
// whole address until a human says which is which.
//
// Entries are never actively pruned: an identity's own value (an instance
// id, say) is specific enough that a stale entry is inert once the live
// object's tags stop being listed at all - which happens on its own, well
// inside any reasonable estate's own apply cadence - and there is nothing
// this package could safely reclaim earlier without risking exactly the
// wrong-marker hazard the whole mechanism exists to avoid.
type tombstoneFields struct {
	// Identity is the destroyed object's own identity, the same shape a
	// current object's Identity member carries - what
	// [orphanMatchesTombstone] compares a live tag-sweep sighting against.
	// Nil is never written deliberately: [RecordStore.tombstone] only adds
	// an entry when the envelope it is replacing already carried one (see
	// that function's own comment), and an entry with nothing populated is
	// never added at all (see empty()).
	Identity *identityPayload `json:"identity,omitempty"`

	// Provider is this destroyed object's OWN managing provider
	// configuration ([addrs.AbsProviderConfig.String]) at the moment it
	// was destroyed - the same "independent of the envelope's own
	// current Provider field" reasoning as [deposedFields.Provider].
	Provider string `json:"provider,omitempty"`

	// Time is when this entry was written, RFC 3339 - "destroyed by us at
	// <time>", for an operator reading the record store by hand. Nothing
	// in this package reads it back to decide anything: entries are never
	// actively expired (see this type's own doc comment).
	Time string `json:"time,omitempty"`
}

func (t *tombstoneFields) empty() bool {
	return t == nil || (t.Identity.empty() && t.Provider == "" && t.Time == "")
}

// objectFields is [recordEnvelope.Object]: the whole of a record-backed
// resource instance's value, since for that class the record IS the object.
// It is today's recordPayload, unchanged field for field - only the
// enclosing envelope is new.
type objectFields struct {
	// ValueType is the value's own cty type, as ctyjson.MarshalType writes
	// it - what lets [decodeObjectValue] rebuild the value with no schema
	// at all.
	ValueType json.RawMessage `json:"value_type"`

	// Attrs is the value itself, ctyjson-encoded against ValueType.
	Attrs json.RawMessage `json:"attrs"`

	// Private is the provider's opaque private-state bytes, round-tripped
	// unchanged.
	Private []byte `json:"private,omitempty"`

	// SensitiveAttrs is the set of paths inside Attrs that carried
	// [marks.Sensitive], encoded exactly as the state file's own
	// "sensitive_attributes" is. See record.go's history: ctyjson cannot
	// encode a marked value, so the marks have to travel beside it or be
	// lost, and losing them produces a perpetual sensitivity-only diff.
	SensitiveAttrs json.RawMessage `json:"sensitive_attributes,omitempty"`

	// Status is the object's [states.ObjectStatus], encoded as
	// recordStatusTainted for a tainted object and omitted entirely for a
	// ready one - see [encodeObjectStatus].
	Status string `json:"status,omitempty"`
}

// residueFields is [recordEnvelope.Residue]: today's residuePayload's
// Attributes map, unchanged - the values this estate last sent for
// arguments the provider's Read never gives back (issue #275).
type residueFields struct {
	Attributes map[string]residueAttrValue `json:"attributes"`
}

func (r *residueFields) empty() bool {
	return r == nil || len(r.Attributes) == 0
}

// provisionedFields is [recordEnvelope.Provisioned]: today's
// provisionedPayload's one bit (issue #353). There is deliberately no
// "not tainted" spelling here either: a present Provisioned member always
// carries Tainted=true, and absence is the only spelling of "no failure".
type provisionedFields struct {
	Tainted bool `json:"tainted"`
}

func (p *provisionedFields) empty() bool {
	return p == nil || !p.Tainted
}

// recordEnvelope is the one JSON shape every key under [RecordKeyPrefix]
// holds: one address, one kind, and up to four optional facts about it. See
// this file's package-level comment for the whole design.
type recordEnvelope struct {
	// FormatVersion is always [envelopeFormatVersion] on anything this
	// package writes. Zero (the Go zero value, indistinguishable from an
	// absent JSON field) is what a v1 payload has, which is exactly the
	// signal [decodeEnvelope] uses to fold Legacy* below into Object.
	FormatVersion int `json:"format_version,omitempty"`

	// Address is the instance address this record is for, written out in
	// full and checked against the caller's address by every reader -
	// [RecordStore.getRaw]'s own discipline, restated from today's
	// locatedPayload/residuePayload/provisionedPayload. Empty for a v1
	// payload, which carried no such field and relied on the key's own
	// encoded segment alone; a reader skips the check when Address is
	// empty for exactly that reason.
	Address string `json:"address,omitempty"`

	// Kind is [recordKindObject] or [recordKindIdentity]. Empty only for a
	// v1 payload; [decodeEnvelope] always resolves it before handing an
	// envelope to a caller.
	Kind string `json:"kind,omitempty"`

	// Provider is the managing provider instance address
	// ([addrs.AbsProviderConfig.String]) at the moment this envelope was
	// last written - states.Resource.ProviderConfig for write-back after
	// an apply, or the migrated state's own recorded ProviderConfig at
	// live-import. Ruled 2026-08-23 (#389 research): a record is the only
	// place a DEPOSED object's managing provider can live later (#361, not
	// this unit's scope - no deposed-object shape is added here), and it
	// gives undeclared-resource provider selection (#69,
	// [Options.UndeclaredProviders]) a source that is not a sweep.
	//
	// Empty for a v1 payload, which predates this field, and for any v2
	// envelope written before this field existed - [decodeEnvelope]
	// tolerates its absence exactly as it tolerates a v1 payload's absent
	// Address: a reader treats an empty Provider as "not known from a
	// record", the same answer it already had before this field existed.
	Provider string `json:"provider,omitempty"`

	Identity    *identityPayload   `json:"identity,omitempty"`
	Object      *objectFields      `json:"object,omitempty"`
	Residue     *residueFields     `json:"residue,omitempty"`
	Provisioned *provisionedFields `json:"provisioned,omitempty"`

	// Deposed is GitHub issue #361's crash-window recovery: every deposed
	// object write-back finds recorded for this address, keyed by
	// [states.DeposedKey]'s string form - the SAME physical key as the
	// current object's own envelope, rather than a key of its own. See
	// #361's design comment (issuecomment-5405599939): a second physical
	// key would mean "the new object exists" and "the old one is now
	// deposed" commit through two independent CAS writes instead of one,
	// which reopens the crash window a second write later rather than
	// closing it. One envelope, one [RecordStore.mergeEnvelope] call, one
	// PutIfVersion makes the two facts atomic by construction.
	//
	// Nil for every envelope written before this field existed, and for a
	// v1 payload, which predates the whole envelope; [decodeEnvelope]
	// tolerates its absence exactly as it tolerates [recordEnvelope.Provider]'s.
	// [RecordStore.MoveRecord] needs no change for this member: it
	// re-marshals the whole decoded envelope with only Address rewritten,
	// so Deposed rides along unchanged, same as every other member.
	Deposed map[string]*deposedFields `json:"deposed,omitempty"`

	// Tombstone is every identity this address has held that this estate's
	// own apply has since destroyed - see [tombstoneFields]'s own doc
	// comment for why this exists and [RecordStore.tombstone] for how it
	// is written. Keyed by the destroyed identity's own [identityPayload]
	// key (ImportID, or an encoding of Attrs for a composite identity) so
	// that destroying the same address's several successive occupants
	// over an estate's history (replace, then remove) accumulates
	// distinct entries rather than each overwriting the last.
	//
	// Nil for every envelope written before this field existed; tolerated
	// exactly as [Deposed]'s own absence is. [RecordStore.MoveRecord]
	// needs no change for this member either, for the same reason
	// [Deposed]'s own comment gives.
	Tombstone map[string]*tombstoneFields `json:"tombstone,omitempty"`

	// The four fields below are v1's flat shape, decode-only. A v1
	// record-backed payload carried these at the envelope's own top level
	// under exactly these names (recordPayload's original tags); nothing in
	// this package ever sets them on a value it is about to encode -
	// [decodeEnvelope] folds them into Object and clears them the moment it
	// recognizes the shape, so a re-write of anything this package has
	// decoded always goes out as format_version 2 with a nested Object.
	LegacyValueType      json.RawMessage `json:"value_type,omitempty"`
	LegacyAttrs          json.RawMessage `json:"attrs,omitempty"`
	LegacyPrivate        []byte          `json:"private,omitempty"`
	LegacySensitiveAttrs json.RawMessage `json:"sensitive_attributes,omitempty"`
	LegacyStatus         string          `json:"status,omitempty"`
}

// isEmpty reports whether env carries none of the four facts - the signal
// [RecordStore.mergeEnvelope] uses to delete a key rather than write back an
// envelope with nothing left in it. A tombstone-only envelope (every other
// field cleared by [RecordStore.tombstone]) is deliberately NOT empty: that
// is the one case this whole mechanism exists for - keeping the key alive
// with nothing but a tombstone is the entire difference between it and a
// plain delete.
func (env recordEnvelope) isEmpty() bool {
	return env.Identity.empty() && env.Object == nil && env.Residue.empty() && env.Provisioned.empty() && len(env.Deposed) == 0 && len(env.Tombstone) == 0
}

// providerString renders p as [recordEnvelope.Provider]'s value, "" for a
// zero-value address (no provider known - callers that never resolved one,
// such as [recordLocatedFor]'s fallback path for an instance with no
// carrier at all). [addrs.AbsProviderConfig] cannot be compared with ==
// (its Module field is a slice), so this checks the one sub-field that is
// never legitimately empty for a real provider address.
func providerString(p addrs.AbsProviderConfig) string {
	if p.Provider.Type == "" {
		return ""
	}
	return p.String()
}

// decodeEnvelope reads raw as a [recordEnvelope], resolving a v1 payload
// (recordPayload's old flat shape) into the same struct a v2 payload
// decodes to: Kind [recordKindObject], Object populated, Legacy* fields
// cleared. See this file's package-level comment.
func decodeEnvelope(raw []byte) (recordEnvelope, error) {
	var env recordEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return recordEnvelope{}, fmt.Errorf("the stored record is not valid JSON: %w", err)
	}

	// Normalize an Identity/Residue/Provisioned pointer json.Unmarshal
	// allocated because the raw JSON happened to carry a same-named key,
	// but which carries none of ITS OWN fields, back to nil.
	//
	// This is what closes a retired pre-#364 payload's own hole: the old
	// locatedPayload's "identity" member was `map[string]string` keyed by
	// identity-schema attribute name (locatedFormatVersion, choudoufu
	// before 0be41c03ef), a completely different shape from today's
	// identityPayload{ImportID, Attrs}. None of its keys are "import_id"
	// or "attrs", so json.Unmarshal leaves both of THIS struct's own
	// fields at their zero value - but it still allocates the pointer,
	// because the JSON value at "identity" is a non-null object. Left
	// unnormalized, that non-nil-but-vacuous pointer defeats the
	// all-four-nil check below, and this function fell through to
	// resolving Kind as [recordKindObject] with Object still nil - a
	// shape [RecordStore.mergeEnvelope] never writes and this package must
	// never hand a caller either. Found by 2026-08-23's adversarial audit
	// of this file's own #364 unit A1; TestDecodeEnvelopeRefusesARetiredLocatedPayload
	// pins it with the literal old bytes.
	if env.Identity.empty() {
		env.Identity = nil
	}
	if env.Residue.empty() {
		env.Residue = nil
	}
	if env.Provisioned.empty() {
		env.Provisioned = nil
	}
	// Deposed's own normalization: a map value json.Unmarshal allocated for
	// a "deposed" key that carried no real Identity and no real Provider -
	// the same vacuous-pointer shape the block above closes for Identity,
	// Residue and Provisioned, but here per map entry rather than once.
	// [RecordStore.mergeEnvelope]'s own writer never produces such an
	// entry (diffDeposedForWrite only ever adds one with Identity or
	// Provider set), so this only ever fires on a hand-edited or foreign
	// payload - never silently accepting it here would leave a vacuous
	// entry defeating the all-empty check just below, the same hole
	// 2026-08-23's audit found and closed for the other three members.
	for dk, df := range env.Deposed {
		if df.empty() {
			delete(env.Deposed, dk)
		}
	}
	if len(env.Deposed) == 0 {
		env.Deposed = nil
	}
	// Tombstone's own normalization: the same vacuous-pointer shape the
	// block above closes for Deposed, per map entry. [RecordStore.tombstone]
	// never produces such an entry (it only ever adds one when the
	// envelope it is replacing already carried a real identity - see that
	// function's own comment), so this only ever fires on a hand-edited or
	// foreign payload.
	for tk, tf := range env.Tombstone {
		if tf.empty() {
			delete(env.Tombstone, tk)
		}
	}
	if len(env.Tombstone) == 0 {
		env.Tombstone = nil
	}

	if env.Kind == "" && env.Object == nil && env.Identity == nil && env.Residue == nil && env.Provisioned == nil && env.Deposed == nil && env.Tombstone == nil {
		if len(env.LegacyValueType) == 0 {
			// Neither v1-shaped (no legacy value_type) nor v2-shaped (no
			// kind, no member at all) - a payload this package cannot
			// recognize as anything it ever wrote, whether foreign,
			// corrupted, or truncated. [RecordStore.mergeEnvelope] never
			// writes an envelope this empty - isEmpty() makes it delete the
			// key instead - so nothing legitimate looks like this on disk.
			// Treating it as "an empty envelope" here would read a garbage
			// or corrupted payload as "nothing recorded, nothing tainted,
			// nothing to fill", which is exactly the silent under-run this
			// whole mechanism exists to prevent (see provisioned.go's
			// [ProvisionedStore.Get] history). A decode error is loud and
			// stops the run instead.
			return recordEnvelope{}, fmt.Errorf("the stored record names no recognizable format - not a v1 record-backed payload and not a v2 envelope this version of choudoufu understands")
		}
		env.Kind = recordKindObject
		env.Object = &objectFields{
			ValueType:      env.LegacyValueType,
			Attrs:          env.LegacyAttrs,
			Private:        env.LegacyPrivate,
			SensitiveAttrs: env.LegacySensitiveAttrs,
			Status:         env.LegacyStatus,
		}
		env.LegacyValueType = nil
		env.LegacyAttrs = nil
		env.LegacyPrivate = nil
		env.LegacySensitiveAttrs = nil
		env.LegacyStatus = ""
	}
	if env.Kind == "" {
		env.Kind = recordKindObject
	}

	// The retired residuePayload's own hole: unlike locatedPayload's
	// "identity", its "attributes" member ([residueAttrValue]'s own
	// "attrType"/"attrValue" tags never changed) decodes into
	// [recordEnvelope.Residue] as REAL, non-empty data - so the
	// normalization above cannot tell it apart from a genuine v2
	// residue-only envelope, and the all-four-nil check just above never
	// fires. What both a retired residuePayload and a corrupted or
	// hand-edited "kind" value share is the same defect: Kind resolves to
	// something that disagrees with what the envelope actually carries.
	// Validating the resolved shape here, once, for every reader, is what
	// [SeedRecordForInstance]'s and [WriteBack]'s own nil-checks were
	// papering over rather than preventing.
	switch env.Kind {
	case recordKindObject:
		if env.Object == nil {
			return recordEnvelope{}, fmt.Errorf("the stored record's kind is %q but it carries no object - not a payload this package ever wrote", recordKindObject)
		}
	case recordKindIdentity:
		if env.Object != nil {
			return recordEnvelope{}, fmt.Errorf("the stored record's kind is %q but it also carries an object, which only %q ever carries", recordKindIdentity, recordKindObject)
		}
		if env.Identity == nil && env.Residue == nil && env.Provisioned == nil && len(env.Deposed) == 0 && len(env.Tombstone) == 0 {
			// A GitHub issue #361-only envelope - an ordinary taggable
			// instance whose only recorded fact this pass is a deposed
			// object from an interrupted create-before-destroy - is a
			// legitimate kind=identity shape, not an error: see
			// writeback.go's diffDeposedForWrite, which can be the sole
			// reason [RecordStore.mergeEnvelope] wrote this key at all. A
			// tombstone-only envelope ([RecordStore.tombstone]) is the
			// same shape for the same reason.
			return recordEnvelope{}, fmt.Errorf("the stored record's kind is %q but it carries none of an identity, a residue classification, a provisioner taint, a deposed object or a tombstone - not a payload this package ever wrote", recordKindIdentity)
		}
	default:
		return recordEnvelope{}, fmt.Errorf("the stored record names kind %q, which this version of choudoufu does not understand", env.Kind)
	}

	return env, nil
}

// recordStatusTainted is objectFields.Status's encoding of
// states.ObjectTainted. There is deliberately no "ready" string: the empty
// value (json's omitempty default, and every pre-#216 record's implicit
// status) already means ready, so only the one state that needs a marker
// gets one.
const recordStatusTainted = "tainted"

// encodeObjectStatus turns a real [states.ObjectStatus] into
// objectFields.Status's string. Only ObjectReady and ObjectTainted are
// legal here - see record_test.go and issue #216's own reasoning, carried
// over unchanged from before this envelope existed.
func encodeObjectStatus(status states.ObjectStatus) (string, error) {
	switch status {
	case states.ObjectReady:
		return "", nil
	case states.ObjectTainted:
		return recordStatusTainted, nil
	default:
		return "", fmt.Errorf("cannot persist a record for an object with status %s: only ready and tainted objects are ever recorded", status)
	}
}

// decodeObjectStatus reverses [encodeObjectStatus].
func decodeObjectStatus(raw string) (states.ObjectStatus, error) {
	switch raw {
	case "":
		return states.ObjectReady, nil
	case recordStatusTainted:
		return states.ObjectTainted, nil
	default:
		return 0, fmt.Errorf("the stored record's status %q is not one this version of choudoufu understands", raw)
	}
}

// encodeObjectFields turns a materialized value, its provider-private
// bytes, and its [states.ObjectStatus] into an [objectFields] ready to be
// set as a [recordEnvelope]'s Object member. status must be
// states.ObjectReady or states.ObjectTainted - see [encodeObjectStatus].
func encodeObjectFields(val cty.Value, private []byte, status states.ObjectStatus) (*objectFields, error) {
	// The unmark is the first thing that happens and it is not defensive.
	// ctyjson.Marshal panics on a marked leaf, and both callers hand this a
	// value that has been through states.ResourceInstanceObjectSrc.Decode,
	// which re-applies the state's AttrSensitivePaths - so a marked value
	// is the ordinary case for any record-backed type with a sensitive
	// attribute, not an edge one. splitSensitiveMarks keeps the paths so
	// they can be persisted beside the value.
	unmarked, sensitive, err := splitSensitiveMarks(val)
	if err != nil {
		return nil, fmt.Errorf("reading the record's sensitivity: %w", err)
	}
	valTy, err := ctyjson.MarshalType(unmarked.Type())
	if err != nil {
		return nil, fmt.Errorf("encoding the record's value type: %w", err)
	}
	attrs, err := ctyjson.Marshal(unmarked, unmarked.Type())
	if err != nil {
		return nil, fmt.Errorf("encoding the record's value: %w", err)
	}
	sensitiveAttrs, err := marshalSensitivePaths(sensitive)
	if err != nil {
		return nil, fmt.Errorf("encoding the record's sensitive paths: %w", err)
	}
	statusStr, err := encodeObjectStatus(status)
	if err != nil {
		return nil, fmt.Errorf("encoding the record's status: %w", err)
	}
	return &objectFields{ValueType: valTy, Attrs: attrs, SensitiveAttrs: sensitiveAttrs, Private: private, Status: statusStr}, nil
}

// decodeObjectValue reverses [encodeObjectFields]: the value's own type
// travels with it, so decoding needs no provider schema at all. The caller
// is still responsible for converting the result to the current schema's
// implied type before trusting it against a running provider - see
// builder.materializeRecord - because a record written under an older
// provider version may not conform to the schema in hand today.
func decodeObjectValue(of *objectFields) (cty.Value, []byte, states.ObjectStatus, error) {
	ty, err := ctyjson.UnmarshalType(of.ValueType)
	if err != nil {
		return cty.NilVal, nil, 0, fmt.Errorf("the stored record's value type could not be read: %w", err)
	}
	val, err := ctyjson.Unmarshal(of.Attrs, ty)
	if err != nil {
		return cty.NilVal, nil, 0, fmt.Errorf("the stored record's value could not be read: %w", err)
	}
	sensitive, err := unmarshalSensitivePaths(of.SensitiveAttrs)
	if err != nil {
		return cty.NilVal, nil, 0, fmt.Errorf("the stored record's sensitive paths could not be read: %w", err)
	}
	if pvms := asSensitiveMarks(sensitive); len(pvms) > 0 {
		val = val.MarkWithPaths(pvms)
	}
	status, err := decodeObjectStatus(of.Status)
	if err != nil {
		return cty.NilVal, nil, 0, fmt.Errorf("the stored record's status could not be read: %w", err)
	}
	return val, of.Private, status, nil
}

// RecordStore is the one type every reader and writer of a per-instance
// record now goes through: a single [staterecord.Store], keyed by
// [RecordKey], holding [recordEnvelope]s. It replaces four namespaces and
// three no-List wrapper types (LocatedStore, ResidueStore, ProvisionedStore)
// with one enumerable store, filtered by kind rather than by key root.
//
// It embeds no [staterecord.Store] publicly and exposes exactly the methods
// this package's own readers and writers need; List is the one exception,
// used by [builder.discoverOrphanedRecords], and callers outside that
// function have no reason to enumerate every key an estate's record store
// holds.
type RecordStore struct {
	store  staterecord.Store
	prefix string
}

// NewRecordEnvelopeStore wraps store as the one record envelope store for
// prefix (ordinarily [RecordKeyPrefix](estate), or a record_store block's
// key_prefix override). A nil store yields a nil *RecordStore, so a run
// with no record_store block declared has nothing to consult and every
// record-admitted, record-located, residue-carrying or provisioner-taint
// instance stays exactly where it was before any of those mechanisms
// existed.
func NewRecordEnvelopeStore(store staterecord.Store, prefix string) *RecordStore {
	if store == nil {
		return nil
	}
	return &RecordStore{store: store, prefix: prefix}
}

// Prefix returns the key namespace this store was built with, "" for a nil
// receiver.
func (s *RecordStore) Prefix() string {
	if s == nil {
		return ""
	}
	return s.prefix
}

// List returns every key this store holds, exactly [staterecord.Store.List]
// rooted at this store's own prefix. Used by
// [builder.discoverOrphanedRecords] alone.
func (s *RecordStore) List(ctx context.Context) ([]string, error) {
	if s == nil {
		return nil, nil
	}
	return s.store.List(ctx, s.prefix)
}

// getRaw fetches and decodes the envelope stored for addr. exists is false
// when there is no key at all - the ordinary "nothing recorded yet" answer,
// not an error. A decoded envelope whose own Address disagrees with addr
// (only possible for a v2 payload; a v1 payload carries no Address at all,
// and skips the check for exactly the reason a v1 record-backed payload
// always has) is refused rather than trusted, the same discipline every one
// of the four predecessor stores applied to its own namespace.
func (s *RecordStore) getRaw(ctx context.Context, addr addrs.AbsResourceInstance) (env recordEnvelope, version string, exists bool, err error) {
	if s == nil {
		return recordEnvelope{}, "", false, nil
	}
	key := RecordKey(s.prefix, addr)
	payload, version, exists, err := s.store.Get(ctx, key)
	if err != nil {
		return recordEnvelope{}, "", false, fmt.Errorf("reading the record for %s: %w", addr, err)
	}
	if !exists {
		return recordEnvelope{}, "", false, nil
	}
	env, err = decodeEnvelope(payload)
	if err != nil {
		return recordEnvelope{}, "", false, fmt.Errorf("decoding the record for %s: %w", addr, err)
	}
	if env.Address != "" && env.Address != addr.String() {
		return recordEnvelope{}, "", false, fmt.Errorf("the record stored for %s says it is for %s; refusing to bind an instance to another resource's identity", addr, env.Address)
	}
	return env, version, true, nil
}

// currentVersion reports the version the store holds for addr's key right
// now, "" when the key does not exist. It deliberately does not decode the
// payload - see provisioned.go's history of this exact method - and exists
// so a write-back merge whose plan-time read never touched this address
// (because the concern it cared about did not apply then) can still merge
// safely against a key a DIFFERENT, earlier incarnation of this address
// left behind.
func (s *RecordStore) currentVersion(ctx context.Context, addr addrs.AbsResourceInstance) (string, error) {
	if s == nil {
		return "", nil
	}
	_, version, exists, err := s.store.Get(ctx, RecordKey(s.prefix, addr))
	if err != nil {
		return "", fmt.Errorf("reading the record for %s: %w", addr, err)
	}
	if !exists {
		return "", nil
	}
	return version, nil
}

// getIdentity reads addr's Identity member - GitHub issue #270's
// record-located import identity. keyExists is true whenever the physical
// key exists, regardless of whether Identity itself is populated (the key
// may carry only Residue or Provisioned data); identityFound is true only
// when an Identity member is present and well-formed. A present but
// malformed Identity (empty overall, or an empty component) is an ERROR:
// continuing would bind the instance to a wrong identity, which is
// invisible to every verdict-level check.
func (s *RecordStore) GetIdentity(ctx context.Context, addr addrs.AbsResourceInstance) (rec LocatedRecord, version string, keyExists bool, identityFound bool, err error) {
	env, version, exists, err := s.getRaw(ctx, addr)
	if err != nil {
		return LocatedRecord{}, "", false, false, err
	}
	if !exists {
		return LocatedRecord{}, "", false, false, nil
	}
	if env.Identity == nil {
		return LocatedRecord{}, version, true, false, nil
	}
	out := LocatedRecord{ImportID: env.Identity.ImportID, Components: env.Identity.Attrs}
	if out.Empty() {
		return LocatedRecord{}, "", false, false, fmt.Errorf("the located record for %s carries an empty identity", addr)
	}
	for name, v := range out.Components {
		if v == "" {
			return LocatedRecord{}, "", false, false, fmt.Errorf("the located record for %s carries an empty %q component", addr, name)
		}
	}
	return out, version, true, true, nil
}

// DeposedRecord is one deposed object recorded for an address: the same
// identity shape [LocatedRecord] carries, plus the provider configuration
// that managed this specific deposed object - see [deposedFields.Provider].
type DeposedRecord struct {
	ImportID   string
	Components map[string]string
	Provider   string
}

// Empty reports whether this record says nothing at all about the deposed
// object it names.
func (r DeposedRecord) Empty() bool {
	return r.ImportID == "" && len(r.Components) == 0 && r.Provider == ""
}

// GetDeposed reads addr's Deposed member - GitHub issue #361's crash-window
// recovery: every deposed object recorded for this address, keyed by
// [states.DeposedKey]'s string form. keyExists carries [GetIdentity]'s same
// distinction: the physical key may exist for Object/Residue/Provisioned/
// Identity alone, carrying no Deposed member at all, in which case the
// returned map is nil.
func (s *RecordStore) GetDeposed(ctx context.Context, addr addrs.AbsResourceInstance) (deposed map[string]DeposedRecord, version string, keyExists bool, err error) {
	if s == nil {
		return nil, "", false, nil
	}
	env, version, exists, err := s.getRaw(ctx, addr)
	if err != nil {
		return nil, "", false, err
	}
	if !exists {
		return nil, "", false, nil
	}
	if len(env.Deposed) == 0 {
		return nil, version, true, nil
	}
	out := make(map[string]DeposedRecord, len(env.Deposed))
	for dk, df := range env.Deposed {
		if df.empty() {
			continue
		}
		rec := DeposedRecord{Provider: df.Provider}
		if df.Identity != nil {
			rec.ImportID = df.Identity.ImportID
			rec.Components = df.Identity.Attrs
		}
		out[dk] = rec
	}
	if len(out) == 0 {
		return nil, version, true, nil
	}
	return out, version, true, nil
}

// TombstoneRecord is one destroyed identity recorded for an address - see
// [tombstoneFields]'s own doc comment for what this is for.
type TombstoneRecord struct {
	ImportID   string
	Components map[string]string
	Provider   string
}

// Empty reports whether this record says nothing at all about the
// destroyed object it names.
func (r TombstoneRecord) Empty() bool {
	return r.ImportID == "" && len(r.Components) == 0 && r.Provider == ""
}

// GetTombstones reads addr's Tombstone member: every identity this store
// has recorded as destroyed for this address, keyed by that identity's own
// key (see [tombstoneFields]). keyExists carries [GetIdentity]'s same
// distinction: the physical key may exist for Object/Residue/Provisioned/
// Identity/Deposed alone, carrying no Tombstone member at all, in which
// case the returned map is nil.
func (s *RecordStore) GetTombstones(ctx context.Context, addr addrs.AbsResourceInstance) (tombstones map[string]TombstoneRecord, version string, keyExists bool, err error) {
	if s == nil {
		return nil, "", false, nil
	}
	env, version, exists, err := s.getRaw(ctx, addr)
	if err != nil {
		return nil, "", false, err
	}
	if !exists {
		return nil, "", false, nil
	}
	if len(env.Tombstone) == 0 {
		return nil, version, true, nil
	}
	out := make(map[string]TombstoneRecord, len(env.Tombstone))
	for tk, tf := range env.Tombstone {
		if tf.empty() {
			continue
		}
		rec := TombstoneRecord{Provider: tf.Provider}
		if tf.Identity != nil {
			rec.ImportID = tf.Identity.ImportID
			rec.Components = tf.Identity.Attrs
		}
		out[tk] = rec
	}
	if len(out) == 0 {
		return nil, version, true, nil
	}
	return out, version, true, nil
}

// getResidue reads addr's Residue member - GitHub issue #275's argument
// values. keyExists and residueFound carry [getIdentity]'s same distinction.
func (s *RecordStore) GetResidue(ctx context.Context, addr addrs.AbsResourceInstance) (attrs map[string]cty.Value, version string, keyExists bool, residueFound bool, err error) {
	env, version, exists, err := s.getRaw(ctx, addr)
	if err != nil {
		return nil, "", false, false, err
	}
	if !exists {
		return nil, "", false, false, nil
	}
	if env.Residue == nil || len(env.Residue.Attributes) == 0 {
		return nil, version, true, false, nil
	}
	out := make(map[string]cty.Value, len(env.Residue.Attributes))
	for name, raw := range env.Residue.Attributes {
		ty, err := ctyjson.UnmarshalType(raw.Type)
		if err != nil {
			return nil, "", false, false, fmt.Errorf("the residue record for %s records attribute %q at a type that could not be read: %w", addr, name, err)
		}
		val, err := ctyjson.Unmarshal(raw.Value, ty)
		if err != nil {
			return nil, "", false, false, fmt.Errorf("the residue record for %s records attribute %q at a value that could not be read: %w", addr, name, err)
		}
		out[name] = val
	}
	return out, version, true, true, nil
}

// getProvisioned reads addr's Provisioned member - GitHub issue #353's one
// bit. A present member always means Tainted, and one that says otherwise
// is an error rather than a spelling of "not tainted" - see
// [provisionedFields]'s own comment.
func (s *RecordStore) getProvisioned(ctx context.Context, addr addrs.AbsResourceInstance) (tainted bool, version string, keyExists bool, err error) {
	env, version, exists, err := s.getRaw(ctx, addr)
	if err != nil {
		return false, "", false, err
	}
	if !exists {
		return false, "", false, nil
	}
	if env.Provisioned == nil {
		return false, version, true, nil
	}
	if !env.Provisioned.Tainted {
		return false, "", false, fmt.Errorf("the provisioner record for %s says nothing: a record exists only to record a failed create-time provisioner, and absence is the only spelling of \"no failure\"", addr)
	}
	return true, version, true, nil
}

// peekKind reports the [recordKindObject]/[recordKindIdentity] of the
// envelope at key, without requiring the caller to already hold the
// decoded address - [builder.discoverOrphanedRecords]'s own use, which
// finds keys by [List] rather than by address. exists is false for a key
// that has vanished between the List and this read (another writer's race,
// not an error).
func (s *RecordStore) peekKind(ctx context.Context, key string) (kind string, exists bool, err error) {
	if s == nil {
		return "", false, nil
	}
	payload, _, exists, err := s.store.Get(ctx, key)
	if err != nil {
		return "", false, fmt.Errorf("reading the record at %s: %w", key, err)
	}
	if !exists {
		return "", false, nil
	}
	env, err := decodeEnvelope(payload)
	if err != nil {
		return "", false, fmt.Errorf("decoding the record at %s: %w", key, err)
	}
	return env.Kind, true, nil
}

// mergeEnvelope reads the envelope currently stored for addr (if any),
// applies mutate to it, and writes the result back conditional on
// expectedVersion - the version a caller's plan-time read (or an earlier
// merge within the same write-back pass) observed, "" asserting nothing
// exists yet. If mutate leaves the envelope with none of its four facts
// populated, the key is deleted instead of written empty.
//
// The fresh read is what lets several independent concerns (an import
// identity, a residue classification, a provisioner taint) share one key
// safely within one write-back pass: each calls mergeEnvelope in turn, and
// each sees what the one before it just wrote, never a stale copy it holds
// itself. What still guards against a writer OUTSIDE this run is
// expectedVersion, unconditionally passed to the underlying conditional
// write - a caller that wants "assert nothing changed since I planned"
// passes the version it read at plan time, not the version this call just
// observed.
func (s *RecordStore) mergeEnvelope(ctx context.Context, addr addrs.AbsResourceInstance, expectedVersion string, mutate func(*recordEnvelope)) (string, error) {
	if s == nil {
		return "", fmt.Errorf("no record store is configured, so %s's record cannot be written", addr)
	}
	current, _, exists, err := s.getRaw(ctx, addr)
	if err != nil {
		return "", err
	}
	env := current
	if !exists {
		env = recordEnvelope{Kind: recordKindIdentity}
	}
	mutate(&env)
	env.FormatVersion = envelopeFormatVersion
	env.Address = addr.String()
	if env.Kind == "" {
		env.Kind = recordKindIdentity
	}

	key := RecordKey(s.prefix, addr)
	if env.isEmpty() {
		if !exists {
			return "", nil
		}
		if err := s.store.Delete(ctx, key, expectedVersion); err != nil {
			return "", err
		}
		return "", nil
	}
	payload, err := json.Marshal(env)
	if err != nil {
		return "", fmt.Errorf("encoding the record for %s: %w", addr, err)
	}
	return s.store.PutIfVersion(ctx, key, payload, expectedVersion)
}

// MoveRecord relocates the whole record stored for from to the key for to -
// the day2_rename stage's (live/GAUNTLET.md #6, GitHub issue #357)
// live-mv-and-no-moved-block sibling of what c5f530c48d already fixed for
// the `moved`-block path: that fix's plan-time read follows a
// record-located instance through a moved-alias index when the config
// declares one (located.go's locatedIdentityWithAliases) - live-mv rewrites
// a live marker with no `moved` block to consult at all, so nothing else
// ever teaches a later plan where a record-backed instance's key went. This
// is the physical re-key that stands in for that alias consult when there
// is no `moved` block: called once per instance
// whose address falls under a renamed module boundary (mv.go's
// propagateModuleRename), including the resource live-mv was asked to
// rename itself - its own kind=identity record (written by
// internal/live/liveimport/stamp.go's seedIdentityFor for every stamped
// instance, taggable or not) is exactly as stale after a rename as any
// other record under the old module prefix, and a SECOND rename of the
// same instance would look for it under an address the store no longer
// holds anything at if this were skipped.
//
// The envelope's own Address field is rewritten to match to; every other
// member - Kind, Provider, Identity, Object, Residue, Provisioned - crosses
// unchanged, so a moved record decodes identically to the one that was
// there before, apart from the address it now names.
//
// moved is false, with no error, when nothing is recorded for from at all -
// the ordinary case for most instances under a renamed module, which carry
// no record because they are markable and their marker is rewritten
// elsewhere (mv.go's rewrite). A record already occupying to's key is
// refused rather than overwritten: two records claiming one key is the
// wrong-marker hazard HANDOFF.md's safety rule exists to stop, and this
// function has no way to tell which of the two is right.
//
// Not one atomic operation across the two keys: the copy to `to` is
// written and CAS-confirmed (conditional on nothing already being there)
// before the delete at `from` runs, so a crash between the two leaves the
// record readable at BOTH keys - correct at its new address either way,
// since every reader asks by address, and inert at the old one, since
// [builder.discoverOrphanedRecords] never proposes destroying a
// kind=identity key on its own. A crash before the write to `to` completes
// leaves the record only at `from`, exactly as if this call had never
// run. Either way nothing is lost and nothing binds to the wrong address;
// the one visible symptom of an interrupted move is a stale, inert copy
// left at `from`, which day2_crash (live/GAUNTLET.md #10, planned) will
// need a recovery story for - re-running the same live-mv command is the
// obvious one, since a record already moved is simply not found at `from`
// on the next pass and is left alone rather than moved twice.
func (s *RecordStore) MoveRecord(ctx context.Context, from, to addrs.AbsResourceInstance) (moved bool, err error) {
	if s == nil {
		return false, nil
	}
	env, fromVersion, exists, err := s.getRaw(ctx, from)
	if err != nil {
		return false, fmt.Errorf("reading the record to move from %s: %w", from, err)
	}
	if !exists {
		return false, nil
	}

	env.Address = to.String()
	payload, err := json.Marshal(env)
	if err != nil {
		return false, fmt.Errorf("encoding the record moved from %s to %s: %w", from, to, err)
	}
	if _, err := s.store.PutIfVersion(ctx, RecordKey(s.prefix, to), payload, ""); err != nil {
		return false, fmt.Errorf("writing the record moved from %s to %s: %w (nothing was deleted at %s)", from, to, err, from)
	}
	if err := s.store.Delete(ctx, RecordKey(s.prefix, from), fromVersion); err != nil {
		return true, fmt.Errorf(
			"the record for %s was copied to %s, but the old key could not be removed: %w; nothing was lost - %s now holds the correct record - but the stale copy at %s should be cleaned up by hand or by rerunning the same rename",
			from, to, err, to, from)
	}
	return true, nil
}

// delete removes addr's whole envelope, conditional on expectedVersion -
// used when an address leaves the final state entirely (the record-backed
// half) or drops out of every concern this run tracked for it (the merged
// identity/residue/provisioned half). See [WriteBack].
func (s *RecordStore) delete(ctx context.Context, addr addrs.AbsResourceInstance, expectedVersion string) error {
	if s == nil {
		return nil
	}
	return s.store.Delete(ctx, RecordKey(s.prefix, addr), expectedVersion)
}

// tombstone is [delete]'s replacement for the one case [WriteBack] used to
// call [delete] for unconditionally: an address that dropped out of the
// final state entirely because THIS run's own apply destroyed it (see
// [tombstoneFields]'s own doc comment for why a plain delete is not
// enough). It reads the envelope already stored for addr - the SAME fresh
// read [mergeEnvelope] does, so this composes with a same-pass write to
// the identical key exactly as every other merge does - carries forward
// whatever identity that envelope named (env.Identity, the only member an
// ordinary taggable or located instance's kind=identity envelope ever
// populates; a record-backed kind=object instance's Object member carries
// no comparable identity concept and is simply dropped, same as it always
// was for a real delete) into a new tombstone entry, and clears every
// other member.
//
// If the envelope being replaced named no identity at all - an object this
// pass never derived one for, or a key that only ever held a
// residue/provisioned/deposed fact - there is nothing to tombstone, and
// this reduces to exactly what [delete] already did: [mergeEnvelope]'s own
// isEmpty check deletes the key rather than writing an envelope with
// nothing in it.
//
// The tombstone's own Provider comes from the envelope's own, already-
// stored top-level Provider field, not a parameter: that field is exactly
// "the managing provider instance address at the moment this envelope was
// last written" ([recordEnvelope.Provider]'s own doc comment), which for
// the envelope being replaced here means the provider that managed the
// object being destroyed - there is no fresher answer available once the
// address has already left the final state.
func (s *RecordStore) tombstone(ctx context.Context, addr addrs.AbsResourceInstance, expectedVersion string) error {
	if s == nil {
		return nil
	}
	_, err := s.mergeEnvelope(ctx, addr, expectedVersion, func(env *recordEnvelope) {
		identity := env.Identity
		providerAddr := env.Provider
		env.Identity = nil
		env.Object = nil
		env.Residue = nil
		env.Provisioned = nil
		env.Deposed = nil
		// The top-level Provider field names the CURRENT object's
		// provider (its own doc comment); there is no current object once
		// this envelope holds nothing but a tombstone, so it moves onto
		// the tombstone entry itself, the same "own provider,
		// independent of the envelope's current one" reasoning
		// [deposedFields.Provider] already uses.
		env.Provider = ""
		if identity.empty() {
			return
		}
		tk := tombstoneKey(identity)
		if tk == "" {
			return
		}
		if env.Tombstone == nil {
			env.Tombstone = make(map[string]*tombstoneFields, 1)
		}
		env.Tombstone[tk] = &tombstoneFields{
			Identity: identity,
			Provider: providerAddr,
			Time:     tombstoneClock().UTC().Format(time.RFC3339),
		}
	})
	return err
}

// tombstoneClock is [tombstone]'s own time source, a package variable so a
// test can pin it - the same seam [tombstoneFields.Time]'s doc comment
// promises nothing else in this package ever reads back to decide
// anything, so a fixed value only ever affects what a test asserts by
// value, never behavior.
var tombstoneClock = time.Now

// tombstoneKey is [recordEnvelope.Tombstone]'s own map key for identity: the
// ImportID directly for a type identified by one server-minted string
// (the ordinary case for every taggable and located type today), or a
// deterministic encoding of every named component for a composite
// identity, sorted so the same identity always produces the same key
// regardless of map iteration order. "" for an identity with neither -
// [tombstone] never adds an entry for that.
func tombstoneKey(p *identityPayload) string {
	if p == nil {
		return ""
	}
	if p.ImportID != "" {
		return p.ImportID
	}
	if len(p.Attrs) == 0 {
		return ""
	}
	names := make([]string, 0, len(p.Attrs))
	for name := range p.Attrs {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for i, name := range names {
		if i > 0 {
			b.WriteByte('\x00')
		}
		b.WriteString(name)
		b.WriteByte('=')
		b.WriteString(p.Attrs[name])
	}
	return b.String()
}

// DeleteRecord is [delete] exported for mv.go's own reconciliation case
// (gauntlet:giantswarm-mv-children): a chained rename that mixes a bare
// `moved` block with a live-mv call can leave TWO records for the exact
// same instance - one at the address an ordinary apply refreshed it to
// along the way, one older and superseded - once [mover.propagateModuleRename]
// has already carried the fresher copy's content to the final address via
// [MoveRecord]. The older copy is not a second, competing claim on a live
// object; it is dead weight that would otherwise resurface as a false,
// live-confirmed orphan on the next plan (see propagateModuleRename's own
// doc comment). Removing it is not the wrong-marker hazard HANDOFF.md's
// safety rule guards against: a kind=identity key carries no delete
// authority over the cloud object it names (build.go's own comment, "a
// kind=identity key is never delete authority") - only a bookkeeping entry,
// and the caller has already confirmed this exact instance's identity
// content is carried forward under a different key. expectedVersion is the
// version the caller most recently read for addr, exactly as every other
// exported write on this store is conditional.
func (s *RecordStore) DeleteRecord(ctx context.Context, addr addrs.AbsResourceInstance, expectedVersion string) error {
	return s.delete(ctx, addr, expectedVersion)
}
