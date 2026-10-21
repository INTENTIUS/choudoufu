// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"

	"cloud.google.com/go/storage"
	"golang.org/x/oauth2"
	"google.golang.org/api/impersonate"
	"google.golang.org/api/option"

	"github.com/intentius/choudoufu/internal/backend"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/httpclient"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/version"
)

// gcsConnection is the "gcs" block's connection arguments with stock's
// environment defaults applied: each unset argument takes the first set
// variable of the ones stock gcs's schema names for it, in its order.
//
// This is a copy of stock's order, not a call into it. Stock builds its
// client inside an unexported configure method, and its package cannot be
// imported from here: internal/backend/remote-state/gcs reaches
// internal/states/remote, which reaches internal/backend/local, which imports
// this package. The fork does not edit stock packages to make that possible,
// so gcs_store_stock_test.go imports stock's package from the outside and
// holds this copy to its schema's names and defaults.
type gcsConnection struct {
	credentials                        string
	accessToken                        string
	impersonateServiceAccount          string
	impersonateServiceAccountDelegates []string
	storageCustomEndpoint              string
	universeDomain                     string
	encryptionKey                      string
	kmsEncryptionKey                   string
}

// gcsEnvDefaults is, per argument, the environment variables stock gcs's
// schema DefaultFunc reads, in order. credentials is not here: stock reads its
// variables inside configure, after access_token, and so does
// [gcsClientOptions].
var gcsEnvDefaults = map[string][]string{
	"access_token":                {"GOOGLE_OAUTH_ACCESS_TOKEN"},
	"impersonate_service_account": {"GOOGLE_BACKEND_IMPERSONATE_SERVICE_ACCOUNT", "GOOGLE_IMPERSONATE_SERVICE_ACCOUNT"},
	"encryption_key":              {"GOOGLE_ENCRYPTION_KEY"},
	"kms_encryption_key":          {"GOOGLE_KMS_ENCRYPTION_KEY"},
	"storage_custom_endpoint":     {"GOOGLE_BACKEND_STORAGE_CUSTOM_ENDPOINT", "GOOGLE_STORAGE_CUSTOM_ENDPOINT"},
	"universe_domain":             {"GOOGLE_BACKEND_UNIVERSE_DOMAIN"},
}

func gcsConnectionFor(rs *configs.LiveRecordStore) gcsConnection {
	g := rs.GCS
	env := func(set, arg string) string {
		if set != "" {
			return set
		}
		for _, name := range gcsEnvDefaults[arg] {
			if v := os.Getenv(name); v != "" {
				return v
			}
		}
		return ""
	}
	return gcsConnection{
		credentials:                        g.Credentials,
		accessToken:                        env(g.AccessToken, "access_token"),
		impersonateServiceAccount:          env(g.ImpersonateServiceAccount, "impersonate_service_account"),
		impersonateServiceAccountDelegates: g.ImpersonateServiceAccountDelegates,
		storageCustomEndpoint:              env(g.StorageCustomEndpoint, "storage_custom_endpoint"),
		universeDomain:                     env(g.UniverseDomain, "universe_domain"),
		encryptionKey:                      env(g.EncryptionKey, "encryption_key"),
		kmsEncryptionKey:                   env(g.KMSEncryptionKey, "kms_encryption_key"),
	}
}

// gcsClientOptions is stock gcs configure's client options, in its order: an
// access token, else credentials (a path or the JSON itself, else
// GOOGLE_BACKEND_CREDENTIALS, else GOOGLE_CREDENTIALS), else nothing, which
// leaves Application Default Credentials to the client; impersonation over
// whichever of those applies; the user agent; the endpoint and universe
// overrides.
func gcsClientOptions(ctx context.Context, c gcsConnection) ([]option.ClientOption, error) {
	var opts, credOptions []option.ClientOption

	creds := c.credentials
	switch {
	case c.accessToken != "":
		credOptions = append(credOptions, option.WithTokenSource(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: c.accessToken})))
	default:
		if creds == "" {
			creds = os.Getenv("GOOGLE_BACKEND_CREDENTIALS")
		}
		if creds == "" {
			creds = os.Getenv("GOOGLE_CREDENTIALS")
		}
		if creds != "" {
			contents, err := backend.ReadPathOrContents(creds)
			if err != nil {
				return nil, fmt.Errorf("loading credentials: %w", err)
			}
			if !json.Valid([]byte(contents)) {
				return nil, fmt.Errorf("the string provided in credentials is neither valid json nor a valid file path")
			}
			//nolint:staticcheck // deprecated upstream; stock's backend still uses it, and this follows stock.
			credOptions = append(credOptions, option.WithCredentialsJSON([]byte(contents)))
		}
	}

	if c.impersonateServiceAccount != "" {
		ts, err := impersonate.CredentialsTokenSource(ctx, impersonate.CredentialsConfig{
			TargetPrincipal: c.impersonateServiceAccount,
			Scopes:          []string{storage.ScopeReadWrite},
			Delegates:       c.impersonateServiceAccountDelegates,
		}, credOptions...)
		if err != nil {
			return nil, err
		}
		opts = append(opts, option.WithTokenSource(ts))
	} else {
		opts = append(opts, credOptions...)
	}

	opts = append(opts, option.WithUserAgent(httpclient.OpenTofuUserAgent(version.Version)))
	if c.storageCustomEndpoint != "" {
		opts = append(opts, option.WithEndpoint(c.storageCustomEndpoint))
	}
	if c.universeDomain != "" {
		opts = append(opts, option.WithUniverseDomain(c.universeDomain))
	}
	return opts, nil
}

// newGCSClient builds the "gcs" backend's storage client. One option is this
// store's own and not stock's: reads go through the JSON API, which is what
// the store's tests fake and what its 404 handling is written against.
//
// rt's max_attempts, when set, becomes the client's retry budget. Its mode is
// an AWS SDK notion and has no counterpart here.
func newGCSClient(ctx context.Context, rs *configs.LiveRecordStore, rt *configs.LiveRetry) (*storage.Client, error) {
	opts, err := gcsClientOptions(ctx, gcsConnectionFor(rs))
	if err != nil {
		return nil, err
	}
	client, err := storage.NewClient(ctx, append(opts, storage.WithJSONReads())...)
	if err != nil {
		return nil, fmt.Errorf("building the storage client: %w", err)
	}
	if rt != nil && rt.MaxAttemptsSet && rt.MaxAttempts > 0 {
		client.SetRetry(storage.WithMaxAttempts(rt.MaxAttempts))
	}
	return client, nil
}

// gcsEncryption is the block's encryption_key, decoded the way stock decodes
// it (a path or the contents, base64 of 32 bytes), and kms_encryption_key.
func gcsEncryption(rs *configs.LiveRecordStore) (key []byte, kmsKeyName string, err error) {
	c := gcsConnectionFor(rs)
	if c.encryptionKey != "" {
		raw, err := backend.ReadPathOrContents(c.encryptionKey)
		if err != nil {
			return nil, "", fmt.Errorf("loading encryption key: %w", err)
		}
		key, err = base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return nil, "", fmt.Errorf("decoding encryption key: %w", err)
		}
	}
	return key, c.kmsEncryptionKey, nil
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
