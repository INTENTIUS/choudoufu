// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package azsweep

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/intentius/choudoufu/internal/httpclient"
)

// This file is the Key Vault data-plane listing of GitHub issue #2205.
// Resource Graph indexes ARM resources and a vault's keys, secrets and
// certificates are not among them, so they are listed from the vault's own
// endpoint (https://{vault}.vault.azure.net/keys, /secrets, /certificates),
// as the same provider-block credential the Resource Graph client uses, with
// a token for the vault resource rather than for ARM.
//
// Nothing here has been run against a vault: the request building and the
// paging are tested against an httptest server only.

// KeyVaultAPIVersion is the Key Vault data-plane API version every listing
// is sent at.
const KeyVaultAPIVersion = "7.4"

// KeyVaultPageSize is the maxresults every listing asks for: the API's
// documented maximum.
const KeyVaultPageSize = 25

// KeyVaultItem is one listed object. ID is the identifier the listing
// returns (the "kid" of a key, the "id" of a secret or certificate), which
// names the object without a version: the versioned id azurerm imports by
// is not derived here (it waits on GitHub issue #2202).
type KeyVaultItem struct {
	ID   string
	Name string
	Tags map[string]string
}

// KeyVaultClient lists the objects of a vault, authenticated as one
// provider block's credential.
type KeyVaultClient struct {
	cred  azcore.TokenCredential
	http  *http.Client
	creds Credentials
}

// NewKeyVault builds a client from a provider block's arguments, with the
// provider's environment fallbacks applied: the credential chain
// [New] uses, so the listing calls as the provider does.
func NewKeyVault(ctx context.Context, a Attrs) (*KeyVaultClient, error) {
	a = a.WithEnv(os.Getenv)
	cred, _, creds, err := a.credential(ctx, "the Key Vault listing")
	if err != nil {
		return nil, err
	}
	return &KeyVaultClient{cred: cred, http: httpclient.New(ctx), creds: creds}, nil
}

// NewKeyVaultWith builds a client over an already-made credential, for tests
// and for a caller that has one. hc nil is http.DefaultClient.
func NewKeyVaultWith(cred azcore.TokenCredential, hc *http.Client) *KeyVaultClient {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &KeyVaultClient{cred: cred, http: hc}
}

// keyVaultScope is the token scope for a vault base URL: the vault resource
// of that cloud, which is the host without the vault's own name
// ("https://vault.azure.net/.default" for https://v1.vault.azure.net).
func keyVaultScope(base *url.URL) string {
	host := base.Hostname()
	if i := strings.Index(host, "."); i >= 0 {
		host = host[i+1:]
	}
	return "https://" + host + "/.default"
}

// keyVaultPage is one page of a listing.
type keyVaultPage struct {
	Value []struct {
		ID      string            `json:"id"`
		KID     string            `json:"kid"`
		Tags    map[string]string `json:"tags"`
		Managed bool              `json:"managed"`
	} `json:"value"`
	NextLink string `json:"nextLink"`
}

// List returns every object in the vault's collection (keys, secrets or
// certificates), all pages read, or an error and no items. A key or secret
// the vault manages for a certificate is left out: it is the certificate's,
// listed once as the certificate.
func (c *KeyVaultClient) List(ctx context.Context, baseURL, collection string) ([]KeyVaultItem, error) {
	switch collection {
	case "keys", "secrets", "certificates":
	default:
		return nil, fmt.Errorf("%q is not a Key Vault collection", collection)
	}
	base, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") {
		return nil, fmt.Errorf("%q is not a vault URL", baseURL)
	}
	next := base.String() + "/" + collection + "?api-version=" + KeyVaultAPIVersion + "&maxresults=" + fmt.Sprint(KeyVaultPageSize)
	var out []KeyVaultItem
	seenPage := map[string]bool{}
	for next != "" {
		if seenPage[next] {
			return nil, fmt.Errorf("the vault returned nextLink %q again, so paging would never end", next)
		}
		seenPage[next] = true
		page, err := c.page(ctx, base, next)
		if err != nil {
			return nil, err
		}
		for _, v := range page.Value {
			id := v.ID
			if id == "" {
				id = v.KID
			}
			if id == "" || v.Managed {
				continue
			}
			out = append(out, KeyVaultItem{ID: id, Name: keyVaultItemName(id), Tags: v.Tags})
		}
		next = page.NextLink
		if next != "" {
			// The bearer token goes only to the vault it was asked for. A
			// nextLink naming another host is refused rather than followed.
			nu, err := url.Parse(next)
			if err != nil || !strings.EqualFold(nu.Host, base.Host) || nu.Scheme != base.Scheme {
				return nil, fmt.Errorf("the vault returned nextLink %q, which is not on %s", next, base.Host)
			}
		}
	}
	return out, nil
}

// keyVaultItemName is the object name in a Key Vault identifier,
// https://{vault}/{collection}/{name}[/{version}].
func keyVaultItemName(id string) string {
	u, err := url.Parse(id)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

func (c *KeyVaultClient) page(ctx context.Context, base *url.URL, target string) (*keyVaultPage, error) {
	scope := keyVaultScope(base)
	for attempt := 1; ; attempt++ {
		tok, err := c.cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{scope}})
		if err != nil {
			return nil, fmt.Errorf("getting a token for %s as %s: %w", scope, c.creds, err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+tok.Token)
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("listing %s: %w", target, err)
		}
		data, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("reading the answer from %s: %w", base.Host, err)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			wait := throttleWait(resp.Header)
			if attempt > MaxThrottleRetries || wait > MaxThrottleWait {
				return nil, fmt.Errorf("%s throttled the listing %d times", base.Host, attempt)
			}
			if err := sleepCtx(ctx, wait); err != nil {
				return nil, err
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			var env struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			_ = json.Unmarshal(data, &env)
			msg := strings.TrimSpace(env.Error.Code + " " + env.Error.Message)
			if msg == "" {
				msg = strings.TrimSpace(string(data))
			}
			return nil, fmt.Errorf("%s answered %d to %s's listing: %s", base.Host, resp.StatusCode, c.creds, msg)
		}
		var out keyVaultPage
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("decoding the answer from %s: %w", base.Host, err)
		}
		return &out, nil
	}
}
