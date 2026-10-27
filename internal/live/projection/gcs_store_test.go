// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"testing"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/pkg/staterecord"
)

// TestGCSStoreConfigCarriesTheBlock: the bucket, the estate's tag and the
// parallelism reach the store, and the prefix stays in the key, not in the
// store, as for s3 (#916).
func TestGCSStoreConfigCarriesTheBlock(t *testing.T) {
	rs := &configs.LiveRecordStore{Type: "gcs", Bucket: "records", BucketSet: true, KeyPrefix: "team/prod", KeyPrefixSet: true}
	cfg := gcsStoreConfig(nil, rs, nil, "projects/p/locations/l/keyRings/r/cryptoKeys/k", recordStoreOptions{estate: "prod", bulkReadParallelism: 3})
	if cfg.Bucket != "records" || cfg.KeyPrefix != "" || cfg.GetAllParallelism != 3 || cfg.KMSKeyName == "" {
		t.Errorf("config = %+v", cfg)
	}
	if cfg.BaseTags[markers.TagEstate] != "prod" {
		t.Errorf("BaseTags = %v, want the estate", cfg.BaseTags)
	}
	if got := RecordStoreKeyPrefix(rs, "prod"); got != "team/prod/" {
		t.Errorf("RecordStoreKeyPrefix = %q, want the prefix with its delimiter", got)
	}
}

// TestGCSEncryptionFallsBackToStocksEnvironment: an unset encryption_key
// takes GOOGLE_ENCRYPTION_KEY, as stock's schema default does.
func TestGCSEncryptionFallsBackToStocksEnvironment(t *testing.T) {
	raw := bytes.Repeat([]byte{7}, 32)
	t.Setenv("GOOGLE_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(raw))
	t.Setenv("GOOGLE_KMS_ENCRYPTION_KEY", "")
	key, kms, err := gcsEncryption(&configs.LiveRecordStore{Type: "gcs"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, raw) || kms != "" {
		t.Errorf("key %x, kms %q; want the environment's key", key, kms)
	}
}

// TestAGCSKMSRefusalIsAStoreRefusal: live-plan and live-mv go on past an
// outage and never past a refusal (#1376), and a Cloud KMS key nobody can use
// is a refusal, as S3's KMS errors are.
func TestAGCSKMSRefusalIsAStoreRefusal(t *testing.T) {
	err := fmt.Errorf("record_store: provisioning the sentinel: %w", &staterecord.GCSKMSError{Status: 403, Err: errors.New("cloudkms denied")})
	if !IsStoreRefusal(err) {
		t.Error("a Cloud KMS refusal reads as an outage")
	}
}
