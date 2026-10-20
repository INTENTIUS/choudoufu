// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"os"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"

	gcsbackend "github.com/intentius/choudoufu/internal/backend/remote-state/gcs/gcsclient"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// newGCSClient builds the "gcs" backend's storage client with stock's gcs
// backend's own construction, [gcsbackend.NewStorageClient], so an access
// token, credentials, the GOOGLE_* environment variables, Application Default
// Credentials and impersonation take effect in the order stock gives them.
//
// One option is this store's own: reads go through the JSON API, which is
// what the store's tests fake and what its 404 handling is written against.
//
// rt's max_attempts, when set, becomes the client's retry budget. Its mode is
// an AWS SDK notion and has no counterpart here.
func newGCSClient(ctx context.Context, rs *configs.LiveRecordStore, rt *configs.LiveRetry) (*storage.Client, error) {
	g := rs.GCS
	client, err := gcsbackend.NewStorageClient(ctx, gcsbackend.ClientConfigFromEnv(gcsbackend.ClientConfig{
		Credentials:                        g.Credentials,
		AccessToken:                        g.AccessToken,
		ImpersonateServiceAccount:          g.ImpersonateServiceAccount,
		ImpersonateServiceAccountDelegates: g.ImpersonateServiceAccountDelegates,
		StorageCustomEndpoint:              g.StorageCustomEndpoint,
		UniverseDomain:                     g.UniverseDomain,
		ExtraOptions:                       []option.ClientOption{storage.WithJSONReads()},
	}))
	if err != nil {
		return nil, err
	}
	if rt != nil && rt.MaxAttemptsSet && rt.MaxAttempts > 0 {
		client.SetRetry(storage.WithMaxAttempts(rt.MaxAttempts))
	}
	return client, nil
}

// gcsEncryption is the block's encryption_key and kms_encryption_key, each
// falling back to the environment variable stock's schema reads for it.
func gcsEncryption(rs *configs.LiveRecordStore) (key []byte, kmsKeyName string, err error) {
	raw := rs.GCS.EncryptionKey
	if raw == "" {
		raw = os.Getenv("GOOGLE_ENCRYPTION_KEY")
	}
	kmsKeyName = rs.GCS.KMSEncryptionKey
	if kmsKeyName == "" {
		kmsKeyName = os.Getenv("GOOGLE_KMS_ENCRYPTION_KEY")
	}
	key, err = gcsbackend.DecodeEncryptionKey(raw)
	if err != nil {
		return nil, "", err
	}
	return key, kmsKeyName, nil
}

// newGCSStore builds the "gcs" backend's store.
func newGCSStore(ctx context.Context, rs *configs.LiveRecordStore, rt *configs.LiveRetry, o recordStoreOptions) (staterecord.Store, error) {
	key, kmsKeyName, err := gcsEncryption(rs)
	if err != nil {
		return nil, fmt.Errorf("record_store \"gcs\": %w", err)
	}
	client, err := newGCSClient(ctx, rs, rt)
	if err != nil {
		return nil, fmt.Errorf("record_store \"gcs\": %w", err)
	}
	store, err := staterecord.NewGCSStore(gcsStoreConfig(client, rs, key, kmsKeyName, o))
	if err != nil {
		return nil, fmt.Errorf("record_store \"gcs\": %w", err)
	}
	return store, nil
}

// gcsStoreConfig is apart from [newGCSStore] for [s3StoreConfig]'s reason:
// what the block sets can be checked without a network.
func gcsStoreConfig(client *storage.Client, rs *configs.LiveRecordStore, key []byte, kmsKeyName string, o recordStoreOptions) staterecord.GCSConfig {
	return staterecord.GCSConfig{
		Client: client,
		Bucket: rs.Bucket,
		// Empty on purpose: see backendKeyPrefix. The prefix is in the key.
		KeyPrefix:         backendKeyPrefix,
		EncryptionKey:     key,
		KMSKeyName:        kmsKeyName,
		GetAllParallelism: o.bulkReadParallelism,
		BaseTags:          map[string]string{markers.TagEstate: o.estate},
	}
}

// VerifyGCSBucket is [VerifyBucket] for record_store "gcs": the contract read
// for the block's bucket, with no store opened and no sentinel written. It is
// what `choudoufu live-bucket` runs in a directory whose live block declares a
// gcs store. estate may be "", as there.
func VerifyGCSBucket(ctx context.Context, rs *configs.LiveRecordStore, estate string) ([]staterecord.Finding, error) {
	client, err := newGCSClient(ctx, rs, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()
	var namespaces []string
	if estate != "" {
		namespaces = BucketNamespaces(rs, estate)
	}
	return staterecord.CheckGCSBucketContract(ctx, client, rs.Bucket, namespaces)
}
