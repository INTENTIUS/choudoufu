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
	"strings"

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
// (today's locatedPayload). Exactly one of ImportID or Attrs is ever
// populated for a given instance - see [identity.LocatedIdentityPlanFor]'s
// three-way shape, which [LocatedRecordFrom] reads.
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
// envelope with nothing left in it.
func (env recordEnvelope) isEmpty() bool {
	return env.Identity.empty() && env.Object == nil && env.Residue.empty() && env.Provisioned.empty()
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
	if env.Kind == "" && env.Object == nil && env.Identity == nil && env.Residue == nil && env.Provisioned == nil {
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
