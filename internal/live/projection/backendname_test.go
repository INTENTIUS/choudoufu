// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// Issue #916: the "ssm" and "s3" backends were built with the record key
// prefix as their OWN namespace, and every key handed to them already
// began with that same prefix, so the name that reached AWS carried it
// twice. Nothing in this package could see it: writes and reads both went
// through the doubled name and agreed with each other perfectly. Measured
// against real AWS on 2026-09-06, key_prefix = "chdf916probe/e1" put the
// record at the SSM parameter
// "/chdf916probe/e1/chdf916probe/e1/aws_instance/<key>" and at the S3
// object key "chdf916probe/e1/chdf916probe/e1/aws_instance/<key>", one
// level deeper than the operator's IAM policy, `aws ssm
// get-parameters-by-path --path /chdf916probe/e1`, or the live-cert
// harness's own teardown would ever look.
//
// So these assert the RENDERED backend name against a literal. A test that
// wrote a record and listed it back would pass under the doubling, because
// both halves share the renderer; only a literal catches a name nothing
// outside this package reads.
func TestRecordStoreRendersTheKeyPrefixExactlyOnce(t *testing.T) {
	// Keep the SDK's config chain off the network and off whatever
	// credentials this machine happens to have: newRecordStore builds a
	// client, and nothing here ever calls AWS with it.
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	const estate = "prod-networking"

	// The address is fixed so the expected names below can be literals all
	// the way down to the encoded segment.
	addr, diags := addrs.ParseAbsResourceInstanceStr("aws_instance.web")
	if diags.HasErrors() {
		t.Fatalf("parsing the test address: %v", diags.Err())
	}
	const encodedAddr = "YXdzX2luc3RhbmNlLndlYg" // base64url of "aws_instance.web", unpadded

	tests := []struct {
		name string
		rs   *configs.LiveRecordStore

		// wantRecord and wantSentinel are the full backend names - the SSM
		// parameter name, or the S3 object key - not store-relative keys.
		wantRecord   string
		wantSentinel string
	}{
		{
			name: "ssm with a key_prefix the operator set",
			rs: &configs.LiveRecordStore{
				Type:         "ssm",
				KeyPrefix:    "teamx/prod",
				KeyPrefixSet: true,
				Region:       "us-east-2",
				RegionSet:    true,
			},
			wantRecord:   "/teamx/prod/aws_instance/" + encodedAddr,
			wantSentinel: "/teamx/prod/.store-sentinel",
		},
		{
			name: "ssm with the default prefix derived from the estate",
			rs: &configs.LiveRecordStore{
				Type:      "ssm",
				Region:    "us-east-2",
				RegionSet: true,
			},
			wantRecord:   "/tofu-records/prod-networking/aws_instance/" + encodedAddr,
			wantSentinel: "/tofu-records/prod-networking/.store-sentinel",
		},
		{
			name: "s3 with a key_prefix the operator set",
			rs: &configs.LiveRecordStore{
				Type:         "s3",
				Bucket:       "records-bucket",
				BucketSet:    true,
				KeyPrefix:    "teamx/prod",
				KeyPrefixSet: true,
				Region:       "us-east-2",
				RegionSet:    true,
			},
			wantRecord:   "teamx/prod/aws_instance/" + encodedAddr,
			wantSentinel: "teamx/prod/.store-sentinel",
		},
		{
			name: "s3 with the default prefix derived from the estate",
			rs: &configs.LiveRecordStore{
				Type:      "s3",
				Bucket:    "records-bucket",
				BucketSet: true,
				Region:    "us-east-2",
				RegionSet: true,
			},
			wantRecord:   "tofu-records/prod-networking/aws_instance/" + encodedAddr,
			wantSentinel: "tofu-records/prod-networking/.store-sentinel",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// newRecordStore, not NewRecordStore: the exported one runs
			// #693's provisioning handshake, which talks to AWS. The
			// wiring under test - which prefix each backend is built
			// with - is all in the unexported one.
			store, err := newRecordStore(context.Background(), tc.rs, estate, t.TempDir())
			if err != nil {
				t.Fatalf("newRecordStore: %v", err)
			}

			prefix := recordStoreKeyPrefix(tc.rs, estate)
			recordKey := RecordKey(prefix, addr)
			sentinelKey := SentinelKey(prefix)

			var gotRecord, gotSentinel string
			switch s := store.(type) {
			case *staterecord.SSMStore:
				gotRecord, gotSentinel = s.ParameterName(recordKey), s.ParameterName(sentinelKey)
			case *staterecord.S3Store:
				gotRecord, gotSentinel = s.ObjectKey(recordKey), s.ObjectKey(sentinelKey)
			default:
				t.Fatalf("newRecordStore returned a %T, which this test does not know how to ask for a rendered name", store)
			}

			if gotRecord != tc.wantRecord {
				t.Errorf("the record for %s lands at\n  %q\nbut the configured prefix says it must be\n  %q\nA name nothing outside this package reads is a record nothing outside this package can find (issue #916).", addr, gotRecord, tc.wantRecord)
			}
			if gotSentinel != tc.wantSentinel {
				t.Errorf("the store sentinel lands at\n  %q\nwant\n  %q", gotSentinel, tc.wantSentinel)
			}
		})
	}
}
