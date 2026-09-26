// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/smithy-go"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// deniedStore answers every Get the way S3 answers a role with no grant on
// the key, and nothing else is called.
type deniedStore struct {
	staterecord.Store
	err  error
	keys []string
}

func (s *deniedStore) Get(_ context.Context, key string) ([]byte, string, bool, error) {
	s.keys = append(s.keys, key)
	return nil, "", false, s.err
}

func producerStore(t *testing.T) staterecord.Store {
	t.Helper()
	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	return raw
}

func onlyError(t *testing.T, diags tfdiags.Diagnostics) tfdiags.Description {
	t.Helper()
	if !diags.HasErrors() || len(diags) != 1 {
		t.Fatalf("want exactly one error diagnostic, got %d: %s", len(diags), diags.Err())
	}
	return diags[0].Description()
}

// TestReadEstateOutputsDeniedNamesTheOtherEstate is #1371's central
// refusal: a missing grant is reported as a missing grant for THAT estate,
// with the flag that adds it, and never as "not recorded" or a bare
// AccessDenied.
func TestReadEstateOutputsDeniedNamesTheOtherEstate(t *testing.T) {
	store := &deniedStore{err: &smithy.GenericAPIError{Code: "AccessDenied", Message: "Access Denied"}}
	values, diags := ReadEstateOutputs(t.Context(), EstateOutputsSource{
		Store: store, Estate: "app", StoreType: "s3", Bucket: "records",
	}, "network", []string{"namespace"})
	if values != nil {
		t.Errorf("a denied read returned values: %v", values)
	}
	desc := onlyError(t, diags)
	if desc.Summary != SummaryEstateOutputsDenied {
		t.Fatalf("summary = %q, want %q", desc.Summary, SummaryEstateOutputsDenied)
	}
	for _, want := range []string{
		`estate "network"`,
		"render-policy.sh app records --reads-outputs-of network",
		"tofu-outputs/network/",
		"Access Denied",
	} {
		if !strings.Contains(desc.Detail, want) {
			t.Errorf("detail does not say %q:\n%s", want, desc.Detail)
		}
	}
	if len(store.keys) != 1 || !strings.HasPrefix(store.keys[0], RootOutputKeyPrefix("network")) {
		t.Errorf("the read went to %v, want one key under %s", store.keys, RootOutputKeyPrefix("network"))
	}
}

// TestReadEstateOutputsKMSIsNotADenial: a KMS refusal arrives as
// AccessDenied too, and must not send the reader to the bucket policy.
func TestReadEstateOutputsKMSIsNotADenial(t *testing.T) {
	kms := &staterecord.KMSDeniedError{Action: "kms:Decrypt", Err: &smithy.GenericAPIError{Code: "AccessDenied", Message: "kms says no"}}
	_, diags := ReadEstateOutputs(t.Context(), EstateOutputsSource{
		Store: &deniedStore{err: kms}, Estate: "app", StoreType: "s3", Bucket: "records",
	}, "network", []string{"namespace"})
	desc := onlyError(t, diags)
	if desc.Summary != SummaryEstateOutputsUnreadable {
		t.Fatalf("summary = %q, want %q", desc.Summary, SummaryEstateOutputsUnreadable)
	}
	if strings.Contains(desc.Detail, "--reads-outputs-of") {
		t.Errorf("a KMS refusal was worded as a missing bucket grant:\n%s", desc.Detail)
	}
}

// TestReadEstateOutputsUnreachableIsAnError: any other failure is an error
// too, never a skipped value. This is the difference from
// ReadRootOutputValues, which logs and skips.
func TestReadEstateOutputsUnreachableIsAnError(t *testing.T) {
	_, diags := ReadEstateOutputs(t.Context(), EstateOutputsSource{
		Store: &deniedStore{err: errors.New("dial tcp: connection refused")}, Estate: "app", StoreType: "s3",
	}, "network", []string{"namespace"})
	desc := onlyError(t, diags)
	if desc.Summary != SummaryEstateOutputsUnreadable || !strings.Contains(desc.Detail, "connection refused") || !strings.Contains(desc.Detail, `"network"`) {
		t.Errorf("got %q: %s", desc.Summary, desc.Detail)
	}
}

// TestReadEstateOutputsReadsWhatTheProducerWrote is the round trip through
// the producer's own writer, so the reader cannot drift from what is
// written: a sensitive output and an unknown one are never written, and so
// are refused by name as not recorded.
func TestReadEstateOutputsReadsWhatTheProducerWrote(t *testing.T) {
	raw := producerStore(t)
	restore := rootOutputNow
	rootOutputNow = func() time.Time { return time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { rootOutputNow = restore })

	state := states.NewState()
	root := state.EnsureModule(nil)
	root.SetOutputValue("namespace", cty.StringVal("cluster-services"), false, "")
	root.SetOutputValue("zones", cty.ListVal([]cty.Value{cty.StringVal("a"), cty.StringVal("b")}), false, "")
	root.SetOutputValue("token", cty.StringVal("s3cr3t"), true, "")
	root.SetOutputValue("pending", cty.UnknownVal(cty.String), false, "")
	WriteRootOutputValues(t.Context(), NewRootOutputStore(raw, "network"), state)

	src := EstateOutputsSource{Store: raw, Estate: "app", StoreType: "local"}
	values, diags := ReadEstateOutputs(t.Context(), src, "network", []string{"zones", "namespace"})
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}
	if got := values["namespace"]; !got.RawEquals(cty.StringVal("cluster-services")) {
		t.Errorf("namespace = %#v", got)
	}
	if got := values["zones"]; !got.RawEquals(cty.ListVal([]cty.Value{cty.StringVal("a"), cty.StringVal("b")})) {
		t.Errorf("zones = %#v", got)
	}
	if len(diags) != 1 || diags[0].Severity() != tfdiags.Warning || diags[0].Description().Summary != SummaryEstateOutputsAsOf {
		t.Fatalf("want one as-of warning, got %v", diags)
	}
	if d := diags[0].Description().Detail; !strings.Contains(d, "2026-09-26T12:00:00Z") || !strings.Contains(d, `estate "network"`) {
		t.Errorf("the as-of warning does not give the producer and the time:\n%s", d)
	}

	for _, name := range []string{"token", "pending", "never_declared"} {
		_, diags := ReadEstateOutputs(t.Context(), src, "network", []string{"namespace", name})
		desc := onlyError(t, diags)
		if desc.Summary != SummaryEstateOutputNotRecorded || !strings.Contains(desc.Detail, `"`+name+`"`) || !strings.Contains(desc.Detail, `"network"`) {
			t.Errorf("%s: got %q: %s", name, desc.Summary, desc.Detail)
		}
	}
}

// TestReadEstateOutputsUndatedRecord: a record from before RecordedAt
// existed still reads, and the warning says the time is not known rather
// than inventing one.
func TestReadEstateOutputsUndatedRecord(t *testing.T) {
	raw := producerStore(t)
	payload := []byte(`{"formatVersion":"tofu-live-root-output-v1","name":"namespace","type":"string","value":"cluster-services"}`)
	if _, err := raw.PutIfVersion(t.Context(), RootOutputKey("network", "namespace"), payload, ""); err != nil {
		t.Fatal(err)
	}
	values, diags := ReadEstateOutputs(t.Context(), EstateOutputsSource{Store: raw, Estate: "app"}, "network", []string{"namespace"})
	if diags.HasErrors() || !values["namespace"].RawEquals(cty.StringVal("cluster-services")) {
		t.Fatalf("values %v, diags %s", values, diags.Err())
	}
	if d := diags[0].Description().Detail; !strings.Contains(d, "does not carry") {
		t.Errorf("an undated record's warning should say the time is unknown:\n%s", d)
	}
}

func TestReadEstateOutputsRefusesItselfABadNameAndNoStore(t *testing.T) {
	raw := producerStore(t)
	for _, tc := range []struct {
		src   EstateOutputsSource
		other string
		want  string
	}{
		{EstateOutputsSource{Store: raw, Estate: "app"}, "app", SummaryEstateOutputsSelf},
		{EstateOutputsSource{Store: raw, Estate: "app"}, "../app", SummaryEstateOutputsUnreadable},
		{EstateOutputsSource{Estate: "app", Unavailable: "the store is down"}, "network", SummaryEstateOutputsUnreadable},
	} {
		_, diags := ReadEstateOutputs(t.Context(), tc.src, tc.other, []string{"x"})
		if desc := onlyError(t, diags); desc.Summary != tc.want {
			t.Errorf("%q: summary %q, want %q", tc.other, desc.Summary, tc.want)
		}
	}
}

// TestAnOutputMadeSensitiveStopsCrossing is the "only non-sensitive
// outputs cross" bound over time. A value recorded while the output was
// plain must not stay readable by another estate after the producer marks
// the output sensitive: the next apply removes the old record.
func TestAnOutputMadeSensitiveStopsCrossing(t *testing.T) {
	raw := producerStore(t)
	producer := NewRootOutputStore(raw, "network")

	before := states.NewState()
	before.EnsureModule(nil).SetOutputValue("token", cty.StringVal("was-plain"), false, "")
	WriteRootOutputValues(t.Context(), producer, before)

	after := states.NewState()
	after.EnsureModule(nil).SetOutputValue("token", cty.StringVal("now-secret"), true, "")
	WriteRootOutputValues(t.Context(), producer, after)

	_, diags := ReadEstateOutputs(t.Context(), EstateOutputsSource{Store: raw, Estate: "app"}, "network", []string{"token"})
	if desc := onlyError(t, diags); desc.Summary != SummaryEstateOutputNotRecorded {
		t.Fatalf("an output made sensitive still crosses: %q %s", desc.Summary, desc.Detail)
	}
}
