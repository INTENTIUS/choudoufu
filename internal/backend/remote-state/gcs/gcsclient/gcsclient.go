// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package gcsclient is how stock's gcs backend builds its storage client,
// apart from the backend so that choudoufu's record_store "gcs"
// (internal/live/projection) can build its client the same way. The backend
// package itself cannot be imported from there: it reaches
// internal/states/remote, which reaches internal/backend/local, which imports
// internal/live/projection.
package gcsclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"

	"cloud.google.com/go/storage"
	"github.com/intentius/choudoufu/internal/backend"
	"github.com/intentius/choudoufu/internal/httpclient"
	"github.com/intentius/choudoufu/version"
	"golang.org/x/oauth2"
	"google.golang.org/api/impersonate"
	"google.golang.org/api/option"
)

// ClientConfig is the connection half of the gcs backend's configuration: every
// argument that decides how the storage client authenticates and where it
// sends requests. Each field is the argument of the same name, with the
// schema's environment defaults already applied; see [ClientConfigFromEnv].
//
// It is apart from the backend so that choudoufu's record_store "gcs"
// (internal/live/projection) builds its client exactly the way the backend
// does, rather than keeping a second copy of the precedence below.
type ClientConfig struct {
	Credentials                        string
	AccessToken                        string
	ImpersonateServiceAccount          string
	ImpersonateServiceAccountDelegates []string
	StorageCustomEndpoint              string
	UniverseDomain                     string

	// ExtraOptions go on the client after everything above.
	ExtraOptions []option.ClientOption
}

// ClientConfigFromEnv fills every field of c that is empty from the
// environment variables the gcs backend's schema reads for it, in the same
// order. credentials is not among them: its environment fallback is part of
// [NewStorageClient]'s precedence, after access_token, as it is in the
// backend's configure.
func ClientConfigFromEnv(c ClientConfig) ClientConfig {
	firstEnv := func(cur string, names ...string) string {
		if cur != "" {
			return cur
		}
		for _, n := range names {
			if v := os.Getenv(n); v != "" {
				return v
			}
		}
		return ""
	}
	c.AccessToken = firstEnv(c.AccessToken, "GOOGLE_OAUTH_ACCESS_TOKEN")
	c.ImpersonateServiceAccount = firstEnv(c.ImpersonateServiceAccount, "GOOGLE_BACKEND_IMPERSONATE_SERVICE_ACCOUNT", "GOOGLE_IMPERSONATE_SERVICE_ACCOUNT")
	c.StorageCustomEndpoint = firstEnv(c.StorageCustomEndpoint, "GOOGLE_BACKEND_STORAGE_CUSTOM_ENDPOINT", "GOOGLE_STORAGE_CUSTOM_ENDPOINT")
	c.UniverseDomain = firstEnv(c.UniverseDomain, "GOOGLE_BACKEND_UNIVERSE_DOMAIN")
	return c
}

// NewStorageClient builds the storage client the gcs backend uses: an access
// token first, then credentials (a path or the JSON itself, falling back to
// GOOGLE_BACKEND_CREDENTIALS and GOOGLE_CREDENTIALS), then Application
// Default Credentials; impersonation over whichever of those applies; and the
// endpoint and universe overrides.
func NewStorageClient(ctx context.Context, c ClientConfig) (*storage.Client, error) {
	var opts []option.ClientOption
	var credOptions []option.ClientOption

	// Add credential source
	var creds string
	var tokenSource oauth2.TokenSource

	if c.AccessToken != "" {
		tokenSource = oauth2.StaticTokenSource(&oauth2.Token{
			AccessToken: c.AccessToken,
		})
	} else if c.Credentials != "" {
		creds = c.Credentials
	} else if v := os.Getenv("GOOGLE_BACKEND_CREDENTIALS"); v != "" {
		creds = v
	} else {
		creds = os.Getenv("GOOGLE_CREDENTIALS")
	}

	if tokenSource != nil {
		credOptions = append(credOptions, option.WithTokenSource(tokenSource))
	} else if creds != "" {

		// to mirror how the provider works, we accept the file path or the contents
		contents, err := backend.ReadPathOrContents(creds)
		if err != nil {
			return nil, fmt.Errorf("Error loading credentials: %w", err)
		}

		if !json.Valid([]byte(contents)) {
			return nil, fmt.Errorf("the string provided in credentials is neither valid json nor a valid file path")
		}

		//nolint:staticcheck // [option.WithCredentialsJSON] is deprecated
		// upstream, but we're waiting to see how the hashicorp/google provider
		// reacts to that deprecation so we can then mimic their strategy as
		// closely as possible.
		credOptions = append(credOptions, option.WithCredentialsJSON([]byte(contents)))
	}

	// Service Account Impersonation
	if c.ImpersonateServiceAccount != "" {
		ts, err := impersonate.CredentialsTokenSource(ctx, impersonate.CredentialsConfig{
			TargetPrincipal: c.ImpersonateServiceAccount,
			Scopes:          []string{storage.ScopeReadWrite},
			Delegates:       c.ImpersonateServiceAccountDelegates,
		}, credOptions...)

		if err != nil {
			return nil, err
		}

		opts = append(opts, option.WithTokenSource(ts))

	} else {
		opts = append(opts, credOptions...)
	}

	opts = append(opts, option.WithUserAgent(httpclient.OpenTofuUserAgent(version.Version)))

	// Custom endpoint for storage API
	if c.StorageCustomEndpoint != "" {
		opts = append(opts, option.WithEndpoint(c.StorageCustomEndpoint))
	}
	// Custom universe domain for sovereign cloud authentication
	if c.UniverseDomain != "" {
		opts = append(opts, option.WithUniverseDomain(c.UniverseDomain))
	}
	opts = append(opts, c.ExtraOptions...)
	client, err := storage.NewClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("storage.NewClient() failed: %w", err)
	}
	return client, nil
}

// DecodeEncryptionKey reads encryption_key: a path or the contents, holding a
// base64 encoded 32 byte customer supplied encryption key. "" decodes to nil.
func DecodeEncryptionKey(key string) ([]byte, error) {
	if key == "" {
		return nil, nil
	}
	kc, err := backend.ReadPathOrContents(key)
	if err != nil {
		return nil, fmt.Errorf("Error loading encryption key: %w", err)
	}

	// The GCS client expects a customer supplied encryption key to be
	// passed in as a 32 byte long byte slice. The byte slice is base64
	// encoded before being passed to the API. We take a base64 encoded key
	// to remain consistent with the GCS docs.
	// https://cloud.google.com/storage/docs/encryption#customer-supplied
	// https://github.com/GoogleCloudPlatform/google-cloud-go/blob/def681/storage/storage.go#L1181
	k, err := base64.StdEncoding.DecodeString(kc)
	if err != nil {
		return nil, fmt.Errorf("Error decoding encryption key: %w", err)
	}
	return k, nil
}
