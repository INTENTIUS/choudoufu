// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/intentius/choudoufu/internal/addrs"
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

// recordPayload is the JSON envelope a record-backed resource instance's
// value is stored as. It follows internal/states/statefile's own version-4
// convention (ctyjson.MarshalType alongside ctyjson.Marshal) rather than
// requiring the reader to already have the exact provider schema type in
// hand: the type travels with the value, self-described, the same "native
// state format as internal lingua franca" precedent issue #73's charter
// names for projections and micro-state alike (its third use, the
// observational snapshot, was removed by issue #109).
type recordPayload struct {
	// ValueType is the value's own cty type, as ctyjson.MarshalType writes
	// it - what lets decodeRecordPayload rebuild the value with no schema
	// at all.
	ValueType json.RawMessage `json:"value_type"`

	// Attrs is the value itself, ctyjson-encoded against ValueType.
	Attrs json.RawMessage `json:"attrs"`

	// Private is the provider's opaque private-state bytes, round-tripped
	// unchanged. Most of the ten record-backed types never set it, but
	// carrying it costs nothing and a future provider version that starts
	// using it is not this package's business to notice.
	Private []byte `json:"private,omitempty"`

	// SensitiveAttrs is the set of paths inside Attrs that carried
	// [marks.Sensitive], encoded exactly as the state file's own
	// "sensitive_attributes" is (see sensitivepaths.go). It is what
	// [states.ResourceInstanceObjectSrc.AttrSensitivePaths] is to a state
	// file, and it exists for the same reason: ctyjson cannot encode a
	// marked value, so the marks have to travel beside it or be lost.
	//
	// Losing them is not cosmetic. `live-plan` runs with SkipRefresh, so a
	// projected object's marks are the only marks the plan's "before" side
	// ever has, while the "after" side is re-marked from the configuration
	// and the provider schema on every run - and an unmarked before against
	// a marked after is a difference. Every migrated estate holding a
	// sensitive attribute on a record-backed type therefore proposed a
	// perpetual sensitivity-only in-place update, annotated by OpenTofu's
	// own renderer with "The value is unchanged".
	//
	// Omitted entirely when nothing is sensitive, so a record for such a
	// value is byte-identical to one written before this field existed -
	// which [SeedRecordForInstance] depends on, since it treats a
	// byte-different record as a conflict rather than an update.
	SensitiveAttrs json.RawMessage `json:"sensitive_attributes,omitempty"`

	// Status is the object's [states.ObjectStatus], encoded as
	// recordStatusTainted for a tainted object and omitted entirely for a
	// ready one - see encodeObjectStatus. A record-backed resource has no
	// state file to carry the tainted bit stock OpenTofu sets when a
	// create-time provisioner fails (see checkProvisioners in
	// internal/live/lint/lint.go), so the record store is that bit's only
	// home; omitting it here is exactly the write-back bug issue #216
	// found, where a failed provisioner on e.g. a null_resource was
	// written back and read back as healthy forever. A record written by a
	// choudoufu build that predates this field decodes with an empty
	// Status, which decodeObjectStatus treats as ready - the correct
	// default, since every record old enough to lack this field was also
	// written back before RuleProvisioner ever let a provisioner run on a
	// record-backed type in the first place (issue #199), so no legacy
	// record can have actually been tainted.
	Status string `json:"status,omitempty"`
}

// recordStatusTainted is recordPayload.Status's encoding of
// states.ObjectTainted. There is deliberately no "ready" string: the empty
// value (json's omitempty default, and every pre-#216 record's implicit
// status) already means ready, so only the one state that needs a marker
// gets one.
const recordStatusTainted = "tainted"

// encodeObjectStatus turns a real [states.ObjectStatus] - the one
// [states.ResourceInstanceObjectSrc.Decode] and AsTainted actually produce -
// into recordPayload's Status string. Only ObjectReady and ObjectTainted
// are legal here: those are the only two statuses a resource's *current*
// object can hold once an apply has finished (states.ObjectPlanned is a
// transient plan/refresh-walk placeholder that [states.SyncState] strips
// before a real apply runs, per its own RemovePlannedResourceInstanceObjects
// doc comment - it should never reach WriteBack's FinalState). Anything
// else is refused rather than silently folded into "ready", which is
// exactly the failure this type exists to stop.
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

// decodeObjectStatus reverses [encodeObjectStatus]. An empty string -
// either a genuinely ready object or a record written before this field
// existed - decodes as states.ObjectReady, the safe default (see
// recordPayload.Status's doc comment for why a pre-#216 record can never
// have actually been tainted). Any other value this package did not itself
// write is refused rather than guessed at, the same discipline
// encodeObjectStatus applies on the way in.
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

// encodeRecordPayload turns a materialized value, its provider-private
// bytes, and its [states.ObjectStatus] into the bytes a [staterecord.Store]
// holds for one record-backed resource instance. status must be
// states.ObjectReady or states.ObjectTainted - see [encodeObjectStatus].
func encodeRecordPayload(val cty.Value, private []byte, status states.ObjectStatus) ([]byte, error) {
	// The unmark is the first thing that happens and it is not defensive.
	// ctyjson.Marshal panics on a marked leaf, and both callers hand this a
	// value that has been through states.ResourceInstanceObjectSrc.Decode,
	// which re-applies the state's AttrSensitivePaths - so a marked value
	// is the ordinary case for any record-backed type with a sensitive
	// attribute, not an edge one. splitSensitiveMarks keeps the paths so
	// they can be persisted beside the value; see recordPayload.SensitiveAttrs.
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
	p := recordPayload{ValueType: valTy, Attrs: attrs, SensitiveAttrs: sensitiveAttrs, Private: private, Status: statusStr}
	out, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("encoding the record payload: %w", err)
	}
	return out, nil
}

// decodeRecordPayload reverses [encodeRecordPayload]: the value's own type
// travels with it, so decoding needs no provider schema at all. The caller
// is still responsible for converting the result to the current schema's
// implied type before trusting it against a running provider - see
// builder.materializeRecord - because a record written under an older
// provider version may not conform to the schema in hand today.
func decodeRecordPayload(raw []byte) (cty.Value, []byte, states.ObjectStatus, error) {
	var p recordPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return cty.NilVal, nil, 0, fmt.Errorf("the stored record is not valid JSON: %w", err)
	}
	ty, err := ctyjson.UnmarshalType(p.ValueType)
	if err != nil {
		return cty.NilVal, nil, 0, fmt.Errorf("the stored record's value type could not be read: %w", err)
	}
	val, err := ctyjson.Unmarshal(p.Attrs, ty)
	if err != nil {
		return cty.NilVal, nil, 0, fmt.Errorf("the stored record's value could not be read: %w", err)
	}
	// Marked exactly the way states.ResourceInstanceObjectSrc.Decode marks
	// what it reads out of a state file, and for the same reason: the
	// caller is entitled to an object whose sensitivity is intact.
	sensitive, err := unmarshalSensitivePaths(p.SensitiveAttrs)
	if err != nil {
		return cty.NilVal, nil, 0, fmt.Errorf("the stored record's sensitive paths could not be read: %w", err)
	}
	if pvms := asSensitiveMarks(sensitive); len(pvms) > 0 {
		val = val.MarkWithPaths(pvms)
	}
	status, err := decodeObjectStatus(p.Status)
	if err != nil {
		return cty.NilVal, nil, 0, fmt.Errorf("the stored record's status could not be read: %w", err)
	}
	return val, p.Private, status, nil
}
