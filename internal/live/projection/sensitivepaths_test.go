// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/states/statefile"
)

// TestSensitivePathsUseTheStateFilesOwnShape is the external check on this
// encoding: nothing here is compared against this package's own idea of what
// a path looks like. A real state file is written by internal/states/
// statefile, its "sensitive_attributes" is read straight back out of the
// bytes, and marshalSensitivePaths has to produce the same JSON for the same
// paths.
//
// It exists because the alternative - a round-trip test - is a ratchet that
// measures agreement with itself: an encoder and a decoder that are wrong in
// the same way round-trip perfectly. The state file is the format this one
// deliberately reproduces, so the state file is the authority on it.
func TestSensitivePathsUseTheStateFilesOwnShape(t *testing.T) {
	paths := []cty.Path{
		cty.GetAttrPath("content"),
		cty.GetAttrPath("nested").IndexInt(0).GetAttr("secret"),
		cty.GetAttrPath("keyed").IndexString("k"),
	}

	pvms := asSensitiveMarks(paths)
	if len(pvms) != len(paths) {
		t.Fatalf("asSensitiveMarks produced %d marks for %d paths", len(pvms), len(paths))
	}

	state := states.NewState()
	state.RootModule().SetResourceInstanceCurrent(
		addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "local_file", Name: "f"}.Instance(addrs.NoKey),
		&states.ResourceInstanceObjectSrc{
			AttrsJSON:          []byte(`{}`),
			AttrSensitivePaths: pvms,
			Status:             states.ObjectReady,
		},
		addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("local")},
		addrs.NoKey,
	)

	var buf bytes.Buffer
	if err := statefile.WriteForTest(statefile.New(state, "lineage", 1), &buf); err != nil {
		t.Fatalf("writing the reference state file: %s", err)
	}

	var file struct {
		Resources []struct {
			Instances []struct {
				SensitiveAttributes json.RawMessage `json:"sensitive_attributes"`
			} `json:"instances"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(buf.Bytes(), &file); err != nil {
		t.Fatalf("the reference state file is not JSON: %s", err)
	}
	if len(file.Resources) != 1 || len(file.Resources[0].Instances) != 1 {
		t.Fatalf("the reference state file does not have exactly one instance: %s", buf.String())
	}
	want := file.Resources[0].Instances[0].SensitiveAttributes
	if len(want) == 0 {
		t.Fatalf("the reference state file wrote no sensitive_attributes at all: %s", buf.String())
	}

	got, err := marshalSensitivePaths(paths)
	if err != nil {
		t.Fatalf("marshalSensitivePaths: %s", err)
	}

	// Compared as decoded JSON, not as bytes: the state file's writer sorts
	// nothing, so only the SET of paths is a shared property. Both sides are
	// normalized the same way and the comparison is still on the encoding of
	// every step, which is the thing under test.
	if a, b := normalizePathJSON(t, got), normalizePathJSON(t, want); a != b {
		t.Errorf("marshalSensitivePaths wrote\n  %s\nand the state file writes\n  %s", a, b)
	}

	// And the decoder gets the same paths back. This half IS a round trip,
	// deliberately second: it only means anything once the encoding above is
	// pinned to something external.
	back, err := unmarshalSensitivePaths(got)
	if err != nil {
		t.Fatalf("unmarshalSensitivePaths: %s", err)
	}
	if len(back) != len(paths) {
		t.Fatalf("decoded %d paths, want %d", len(back), len(paths))
	}
	for _, want := range paths {
		found := false
		for _, have := range back {
			if have.Equals(want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the path %#v did not survive the round trip: got %#v", want, back)
		}
	}
}

// normalizePathJSON re-encodes a sensitive-paths document with its entries
// sorted, so two writers that disagree only about order compare equal.
func normalizePathJSON(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("sensitive paths are not a JSON array: %s (%s)", err, raw)
	}
	as := make([]string, 0, len(entries))
	for _, e := range entries {
		var steps []pathStepJSON
		if err := json.Unmarshal(e, &steps); err != nil {
			t.Fatalf("a sensitive path is not a JSON array of steps: %s (%s)", err, e)
		}
		re, err := json.Marshal(steps)
		if err != nil {
			t.Fatalf("re-encoding a sensitive path: %s", err)
		}
		as = append(as, string(re))
	}
	sortStrings(as)
	return strings.Join(as, "\n")
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// TestEncodeRecordPayloadRefusesANonSensitiveMark is the refusal half. The
// record format carries exactly the one mark kind a state file carries, and
// a mark it cannot carry has to be an error rather than a silent drop: cty
// marks cannot carry provenance, so a dropped one is unrecoverable and
// invisible.
func TestEncodeRecordPayloadRefusesANonSensitiveMark(t *testing.T) {
	val := cty.ObjectVal(map[string]cty.Value{
		"id":      cty.StringVal("x"),
		"content": cty.StringVal("s").Mark(marks.Ephemeral),
	})
	_, err := encodeRecordPayload(val, nil, states.ObjectReady)
	if err == nil {
		t.Fatal("encodeRecordPayload accepted an ephemeral mark silently")
	}
	if !strings.Contains(err.Error(), "content") {
		t.Errorf("the refusal does not name the offending attribute: %s", err)
	}
}

// TestRecordWithNoSensitivityIsByteIdenticalToTheOldFormat pins the
// compatibility property SeedRecordForInstance leans on: it treats a
// byte-different record as a CONFLICT rather than an update, so adding a
// field that appeared unconditionally would turn every already-migrated
// estate's next migration into a refusal.
func TestRecordWithNoSensitivityIsByteIdenticalToTheOldFormat(t *testing.T) {
	val := cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("abc123"),
		"triggers": cty.MapVal(map[string]cty.Value{"input": cty.StringVal("value")}),
	})
	payload, err := encodeRecordPayload(val, nil, states.ObjectReady)
	if err != nil {
		t.Fatalf("encodeRecordPayload: %s", err)
	}
	if bytes.Contains(payload, []byte("sensitive_attributes")) {
		t.Errorf("a record for an unmarked value carries a sensitive_attributes key: %s", payload)
	}
}

// TestRecordPayloadCarriesSensitivityThroughARoundTrip is the unit-level
// statement of the fix: what goes in marked comes out marked, at the same
// path, with everything else unmarked.
func TestRecordPayloadCarriesSensitivityThroughARoundTrip(t *testing.T) {
	val := cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("d8186d18"),
		"filename": cty.StringVal("builds/plan.json"),
		"content":  cty.StringVal("a-secret-build-plan").Mark(marks.Sensitive),
	})

	payload, err := encodeRecordPayload(val, nil, states.ObjectReady)
	if err != nil {
		t.Fatalf("encodeRecordPayload: %s", err)
	}

	got, _, _, err := decodeRecordPayload(payload)
	if err != nil {
		t.Fatalf("decodeRecordPayload: %s", err)
	}
	if !got.GetAttr("content").HasMark(marks.Sensitive) {
		t.Error("content came back unmarked - the record lost its sensitivity")
	}
	if got.GetAttr("filename").IsMarked() {
		t.Error("filename came back marked - the record invented sensitivity")
	}
	if got.GetAttr("id").IsMarked() {
		t.Error("id came back marked - the record invented sensitivity")
	}
	unmarked, _ := got.UnmarkDeep()
	if s := unmarked.GetAttr("content").AsString(); s != "a-secret-build-plan" {
		t.Errorf("content = %q, want the value that went in", s)
	}
}
