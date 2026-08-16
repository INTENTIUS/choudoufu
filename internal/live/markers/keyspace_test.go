// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markers

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// GitHub issue #243. Every description below is transcribed verbatim from a
// real GetProviderSchema response, read through internal/live/pluginschema on
// 2026-08-16 against TF_PLUGIN_CACHE_DIR: hashicorp/aws 6.59.0,
// hashicorp/google 7.44.0, hashicorp/tfe 0.80.0, DataDog/datadog 4.1.0.
//
// That provenance is the point. The rule under test reads English written by
// somebody else, so a table of strings this fork invented would only prove
// the rule reproduces itself - the ratchet-measuring-itself shape. These
// strings are the provider's. Rewriting VocabularyRefusal to agree with its
// own reasoning does not make them agree with it.
//
// Reproduce the corpus with pluginschema.ResourceTypes, then, per resource
// type, block.Attributes["tags"].Description.
// ---------------------------------------------------------------------------

func TestVocabularyRefusal(t *testing.T) {
	for _, tc := range []struct {
		name    string
		desc    string
		refused bool
		says    string
	}{
		{
			// All 847 settable "tags" maps at hashicorp/aws 6.59.0 carry
			// exactly this description. The provider that works says
			// nothing, and nothing must be read into that.
			name:    "aws/every tags map, 6.59.0",
			desc:    "",
			refused: false,
		},
		{
			name:    "google_project.tags",
			desc:    "A map of resource manager tags. Resource manager tag keys and values have the same definition as resource manager tags. Keys must be in the format tagKeys/{tag_key_id}, and values are in the format tagValues/456. The field is ignored when empty. This field is only set at create time and modifying this field after creation will trigger recreation. To apply tags to an existing resource, see the google_tags_tag_value resource.",
			refused: true,
			says:    "tagKeys/{tag_key_id}",
		},
		{
			// The variant that names no template at all, only two
			// alternatives with an example of each.
			name:    "google_bigtable_instance.tags",
			desc:    "A map of Resource Manager Tags. Keys can be either the numeric tag key ID (tagKeys/123) or the namespaced name (project/tag-key). Values can be the numeric tag value ID (tagValues/456) or the namespaced value (project/tag-key/tag-value). The field is ignored when empty.",
			refused: true,
			says:    "tagKeys/123",
		},
		{
			// The one of google's 17 that states no rule anywhere and only
			// shows two bindings. A rule that read stated requirements
			// alone would let this one through.
			name:    "google_workstations_workstation_cluster.tags",
			desc:    "Resource manager tags bound to this resource.\nFor example:\n\"123/environment\": \"production\",\n\"123/costCenter\": \"marketing\"",
			refused: true,
			says:    "123/environment",
		},
		{
			// The GCP label grammar, quoted by the provider as the service
			// states it. This is the vocabulary the marker cannot be
			// spelled in: no ".", no ":", no uppercase, 63 characters
			// against MaxTagValue's 256.
			name:    "google_secret_manager_secret.labels",
			desc:    "The labels assigned to this Secret.\n\nLabel keys must be between 1 and 63 characters long, have a UTF-8 encoding of maximum 128 bytes,\nand must conform to the following PCRE regular expression: [\\p{Ll}\\p{Lo}][\\p{Ll}\\p{Lo}\\p{N}_-]{0,62}\n\nLabel values must be between 0 and 63 characters long, have a UTF-8 encoding of maximum 128 bytes,\nand must conform to the following PCRE regular expression: [\\p{Ll}\\p{Lo}\\p{N}_-]{0,63}\n\nNo more than 64 labels can be assigned to a given resource.",
			refused: true,
			says:    `cannot spell "."`,
		},
		{
			// A closed key vocabulary stated in prose plus a class, with
			// no namespace separator anywhere.
			name:    "google_iam_workload_identity_pool_provider.attribute_mapping",
			desc:    "Maps attributes from the authentication credentials issued by an external identity provider to Google Cloud attributes. Each key must be a string specifying the Google Cloud IAM attribute to map to. The maximum length of an attribute key is 100 characters, and the key may only contain the characters [a-z0-9_].",
			refused: true,
			says:    "tofu-estate",
		},
		{
			// tfe's tags maps, the only genuinely free-form key/value maps
			// found outside AWS across twelve non-AWS providers. Refusing
			// these would mean the rule had become "documented, therefore
			// refused".
			name:    "tfe_workspace.tags",
			desc:    "A map of key value tags for this workspace.",
			refused: false,
		},
		{
			name:    "tfe_project.tags",
			desc:    "A map of key-value tags to add to the project.",
			refused: false,
		},
		{
			// A rule that requires certain keys to be PRESENT is not a
			// closed vocabulary: a marker key is still a legal entry
			// alongside them. Refusing this would refuse a map that works.
			name:    "datadog_appsec_waf_custom_rule.tags",
			desc:    "Tags associated with the WAF custom rule. `category` and `type` tags are required. Supported categories include `business_logic`, `attack_attempt` and `security_response`.",
			refused: false,
		},
		{
			// GCP annotations. Keys admit dashes, dots, underscores and
			// alphanumerics to 63 characters, and values are arbitrary, so
			// a marker key is spellable - the reason annotations are not a
			// marker surface is that they sit inside a nested metadata
			// block the stamp rewriter cannot reach, which is not this
			// rule's business. Refusing here would be refusing for the
			// wrong reason.
			name:    "google_secret_manager_secret.annotations",
			desc:    "Custom metadata about the secret.\n\nAnnotation keys must be between 1 and 63 characters long, have a UTF-8 encoding of\nmaximum 128 bytes, begin and end with an alphanumeric character ([a-z0-9A-Z]), and\nmay have dashes (-), underscores (_), dots (.), and alphanumerics in between these\nsymbols.",
			refused: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reason, refused := VocabularyRefusal(tc.desc)
			if refused != tc.refused {
				t.Fatalf("refused = %v, want %v (reason %q)", refused, tc.refused, reason)
			}
			if tc.says != "" && !strings.Contains(reason, tc.says) {
				t.Errorf("the reason does not quote the provider back; want %q in:\n%s", tc.says, reason)
			}
		})
	}
}

// TestVocabularyRefusal_proseIsNotAKeyFormat pins the two extractions that
// were wrong on the first pass and would have refused maps that work, both
// found by running the rule over every settable map(string) attribute in aws
// 6.59.0 and google 7.44.0 rather than over a table.
//
// "key/value pairs" is two English words joined by a slash meaning "or", not
// a namespaced key. "[Tag definitions]" is a markdown link label, and it
// compiles as a perfectly valid character class of the letters in "Tag
// definitions" - which is why a character class is required to contain no
// whitespace.
func TestVocabularyRefusal_proseIsNotAKeyFormat(t *testing.T) {
	for _, desc := range []string{
		"User-supplied key/value data that must be unique. Keys must be strings.",
		"A map of tags. See [Tag definitions] for what a key may contain; keys must be valid.",
		"Key/value pairs to apply. Each key must be provided by the caller.",
	} {
		if reason, refused := VocabularyRefusal(desc); refused {
			t.Errorf("prose read as a key format:\n  desc:   %s\n  reason: %s", desc, reason)
		}
	}
}

// TestMarkerTagKeys_areUnnamespaced is the assumption clause one rests on,
// asserted rather than believed: every key this fork writes is a single word
// with no separator, so a provider whose key space is namespaced has no room
// for any of them.
func TestMarkerTagKeys_areUnnamespaced(t *testing.T) {
	keys := markerTagKeys()
	if len(keys) != 3+MaxContinuations-1 {
		t.Errorf("markerTagKeys returned %d keys, want %d: %v", len(keys), 3+MaxContinuations-1, keys)
	}
	for _, k := range keys {
		if strings.ContainsAny(k, "/") {
			t.Errorf("marker tag key %q is namespaced, which clause one assumes it is not", k)
		}
	}
}

// TestUpperBound reads the length cap the way a documented pattern states it.
// GCP's key rule is written as two classes, "[first][rest]{0,62}", and means
// 63 - the leading class contributes the character its quantifier does not.
func TestUpperBound(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		want    int
		ok      bool
	}{
		{`[\p{Ll}\p{Lo}][\p{Ll}\p{Lo}\p{N}_-]{0,62}`, 63, true},
		{`[\p{Ll}\p{Lo}\p{N}_-]{0,63}`, 63, true},
		{`[a-z0-9_]`, 0, false},
	} {
		got, ok := upperBound(tc.pattern)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("upperBound(%q) = %d, %v; want %d, %v", tc.pattern, got, ok, tc.want, tc.ok)
		}
	}
}

// TestVocabularyRefusal_posixClassIsOneClass is the third wrong extraction of
// the same family as "[Tag definitions]", found by an adversarial sweep
// rather than by a provider: a POSIX bracket expression nested inside a
// character class.
//
// "[[:print:]]{0,999}" is one class, and the pattern this fork used to
// extract from it was "[:print:]" - a class of the six letters in "print"
// and a colon - because the inner brackets are excluded from a class's own
// character set and the match therefore started one bracket in. The
// resulting refusal said the values cannot spell ".", of a vocabulary that
// admits every printable character there is, and refusing a working tags map
// is the direction that costs a user their resource type.
//
// The second case is the boundary and must still refuse: [[:alnum:]_.-] is
// read correctly and genuinely cannot spell every character in the marker
// value alphabet.
func TestVocabularyRefusal_posixClassIsOneClass(t *testing.T) {
	cases := []struct {
		name        string
		desc        string
		wantRefused bool
	}{
		{
			"a permissive POSIX class spells a marker",
			"Values may only contain [[:print:]]{0,999} characters.",
			false,
		},
		{
			"a POSIX class with a negation",
			"Values may only contain [[:^cntrl:]]{0,999} characters.",
			false,
		},
		{
			"a POSIX class that genuinely excludes a separator",
			"Values may only contain [[:alnum:]_-]{1,999} characters.",
			true,
		},
		{
			"a POSIX class capped below what one marker value needs",
			"Values may only contain [[:print:]]{0,8} characters.",
			true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, refused := VocabularyRefusal(tc.desc)
			if refused != tc.wantRefused {
				t.Errorf("refused = %v, want %v\n  desc:   %s\n  reason: %s",
					refused, tc.wantRefused, tc.desc, reason)
			}
			if refused && strings.Contains(reason, "[:") && !strings.Contains(reason, "[[:") {
				t.Errorf("the quoted pattern lost the outer bracket, so the class it names is not the one the provider wrote: %s", reason)
			}
		})
	}
}
