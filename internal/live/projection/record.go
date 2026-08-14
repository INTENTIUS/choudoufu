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
}

// encodeRecordPayload turns a materialized value plus its provider-private
// bytes into the bytes a [staterecord.Store] holds for one record-backed
// resource instance.
func encodeRecordPayload(val cty.Value, private []byte) ([]byte, error) {
	valTy, err := ctyjson.MarshalType(val.Type())
	if err != nil {
		return nil, fmt.Errorf("encoding the record's value type: %w", err)
	}
	attrs, err := ctyjson.Marshal(val, val.Type())
	if err != nil {
		return nil, fmt.Errorf("encoding the record's value: %w", err)
	}
	p := recordPayload{ValueType: valTy, Attrs: attrs, Private: private}
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
func decodeRecordPayload(raw []byte) (cty.Value, []byte, error) {
	var p recordPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return cty.NilVal, nil, fmt.Errorf("the stored record is not valid JSON: %w", err)
	}
	ty, err := ctyjson.UnmarshalType(p.ValueType)
	if err != nil {
		return cty.NilVal, nil, fmt.Errorf("the stored record's value type could not be read: %w", err)
	}
	val, err := ctyjson.Unmarshal(p.Attrs, ty)
	if err != nil {
		return cty.NilVal, nil, fmt.Errorf("the stored record's value could not be read: %w", err)
	}
	return val, p.Private, nil
}
