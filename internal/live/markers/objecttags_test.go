// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markers

import (
	"regexp"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
)

func objectTagsAddr(t *testing.T, s string) addrs.AbsResourceInstance {
	t.Helper()
	addr, diags := addrs.ParseAbsResourceInstanceStr(s)
	if diags.HasErrors() {
		t.Fatalf("parsing %q: %s", s, diags.Err())
	}
	return addr
}

// s3TagValue is the character set S3 accepts in an object tag's value:
// letters, digits, whitespace, and + - = . _ : / @. Anything else fails the
// PutObject, so a record whose address escaped to something outside it would
// be a record that could not be written.
var s3TagValue = regexp.MustCompile(`^[\p{L}\p{N}\s+\-=._:/@]*$`)

// TestRecordObjectTagsAreTheResourcesOwnMarkers is GitHub issue #1337's
// second acceptance item: the values come from the same source as the
// markers on the estate's managed resources. The assertion is against
// EscapeAddress, SplitAddress and AddressTagKey directly, which is what
// stamps a resource, so a second derivation creeping into AddressObjectTags
// shows up here as a disagreement.
//
// The estate tag is added here the way the store adds it, from
// staterecord.S3Config.BaseTags, because the full set is what has to fit
// inside S3's limits and stay inside the tag-value charset.
func TestRecordObjectTagsAreTheResourcesOwnMarkers(t *testing.T) {
	long := `module.m.aws_thing.x["` + strings.Repeat("k", MaxTagValue+40) + `"]`
	for _, raw := range []string{
		`aws_sqs_queue.plain`,
		`module.net.aws_subnet.private[2]`,
		`aws_iam_role.by_key["team.a:b@c+d"]`,
		`module.a["x y"].module.b[0].aws_thing.z["k/v"]`,
		long,
	} {
		addr := objectTagsAddr(t, raw)
		tags := AddressObjectTags(addr)
		tags[TagEstate] = "prod-eu"

		if tags[TagEstate] != "prod-eu" {
			t.Errorf("%s: %s = %q, want the estate as given", raw, TagEstate, tags[TagEstate])
		}
		chunks := SplitAddress(EscapeAddress(addr.String()))
		if len(tags) != 1+len(chunks) {
			t.Errorf("%s: %d tags, want the estate plus %d address chunk(s): %v", raw, len(tags), len(chunks), tags)
		}
		for i, chunk := range chunks {
			if got := tags[AddressTagKey(i)]; got != chunk {
				t.Errorf("%s: %s = %q, want %q - the value the resource's own marker carries", raw, AddressTagKey(i), got, chunk)
			}
		}
		// Round trip: what the bucket says is recoverable as the address.
		if gathered, corrupt := GatherAddress(tags); corrupt || gathered != EscapeAddress(addr.String()) {
			t.Errorf("%s: the tags gather back to %q (corrupt=%v), want %q", raw, gathered, corrupt, EscapeAddress(addr.String()))
		}
		for key, value := range tags {
			if len([]rune(value)) > MaxTagValue {
				t.Errorf("%s: %s is %d characters, past the %d an S3 tag value holds", raw, key, len([]rune(value)), MaxTagValue)
			}
			if !s3TagValue.MatchString(value) {
				t.Errorf("%s: %s = %q carries a character S3 refuses in a tag value, so this record's PutObject would fail", raw, key, value)
			}
		}
		if len(tags) > 10 {
			t.Errorf("%s: %d tags, and S3 allows an object ten", raw, len(tags))
		}
	}
	if got := len(SplitAddress(EscapeAddress(objectTagsAddr(t, long).String()))); got < 2 {
		t.Fatalf("the long fixture fits one tag (%d chunk), so continuation tags were never exercised", got)
	}
}
