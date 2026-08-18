// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRecordAddrRefusesLocatedKeyUnderAnyPrefix widens
// [TestLocatedKeysAreInvisibleToOrphanDiscovery]'s decoding leg from the ONE
// prefix a default run uses to every prefix an operator can actually write.
//
// discoverOrphanedRecords lists Options.RecordKeyPrefix, which is a
// record_store block's key_prefix when one is set, not RecordKeyPrefix's
// output. internal/configs' validateRecordStoreKeyPrefix refuses a prefix
// whose first "/"-segment is "tofu-located", and that check is exact-string
// on the segment - so a prefix that is a STRING prefix of the located root
// without being a path-segment prefix of it ("tofu", "tofu-locate",
// "tofu-located-2") is accepted, and on a store whose List is a raw string
// match it would hand a located key straight to RecordAddr.
//
// The property that closes it is RecordAddr's own "prefix + slash"
// requirement, not the validator. This test is that property, stated against
// the inputs that would defeat a weaker one.
func TestRecordAddrRefusesLocatedKeyUnderAnyPrefix(t *testing.T) {
	const estate = "my-estate"
	locAddr := locatedTestAddr(t, "aws_eip_association", "bastion")
	key := LocatedKey(estate, locAddr)

	for _, prefix := range []string{
		"tofu",
		"tofu-",
		"tofu-locate",
		"tofu-located-2",
		"tofu-located2",
		"tofu-locatedx",
		locatedNamespaceRoot[:len(locatedNamespaceRoot)-1],
		"records",
		RecordKeyPrefix(estate),
		RecordKeyPrefix(estate) + "/aws_eip_association",
	} {
		t.Run(prefix, func(t *testing.T) {
			if got, ok := RecordAddr(prefix, key); ok {
				t.Errorf("RecordAddr(%q, %q) decoded a located key into %s. discoverOrphanedRecords would materialize it undeclared, which proposes destroying the live object an ID was merely recorded for - the thing issue #270's ruling forbids.", prefix, key, got)
			}
		})
	}

	// The one prefix that WOULD decode it is the located root itself, and
	// that is exactly what the configs-side validator refuses. Asserted here
	// so the two halves cannot both be relaxed without something failing:
	// if this ever stops decoding, the validator's refusal has become
	// decoration, and the comment in located.go that leans on it is stale.
	if _, ok := RecordAddr(locatedNamespaceRoot+"/"+estate, key); !ok {
		t.Errorf("RecordAddr(%q, ...) no longer decodes a located key. That is safer, but internal/configs' validateRecordStoreKeyPrefix and located.go's doc both claim this is the hole they close; one of the three is now wrong.", locatedNamespaceRoot+"/"+estate)
	}
}

// TestDecodeRecordPayloadRefusesALocatedPayload proves located.go's step 3.
//
// [LocatedStore]'s doc says a caller that reached past it, listed the located
// namespace by hand and fed the result to builder.materializeRecord would
// still fail loudly, because decodeRecordPayload cannot read a
// [locatedPayload]. That is a claim about a JSON decoder written with no
// DisallowUnknownFields, so it is worth executing rather than believing:
// encoding/json ignores fields it does not know and leaves the ones it does
// at their zero value, which is a shape that has silently succeeded before.
func TestDecodeRecordPayloadRefusesALocatedPayload(t *testing.T) {
	raw, err := json.Marshal(locatedPayload{
		FormatVersion: locatedFormatVersion,
		Address:       "aws_eip_association.bastion",
		ImportID:      "eipassoc-0123456789abcdef0",
	})
	if err != nil {
		t.Fatalf("marshalling the located payload: %s", err)
	}

	val, private, status, err := decodeRecordPayload(raw)
	if err == nil {
		t.Fatalf("decodeRecordPayload read a located payload as a record: value %#v, private %q, status %v.\n"+
			"That is the last guard between a hand-listed located key and materializeRecord's undeclared path, which proposes destroying the live object.", val, private, status)
	}
	if !strings.Contains(err.Error(), "value type") {
		t.Errorf("decodeRecordPayload refused the located payload with %q; the refusal should name the missing value type, so an operator reading it can tell a cross-namespace key from a corrupt record", err)
	}
}
