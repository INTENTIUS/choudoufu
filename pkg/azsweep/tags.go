// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package azsweep

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// TagsAPIVersion is the Microsoft.Resources api-version the post-create
// marker write is sent at: the version Tags - Update At Scope is
// documented at, and the one go-azure-sdk's resources/2021-04-01/tags
// client (which azurerm 5.10.0 vendors for its own tag writes) sends.
const TagsAPIVersion = "2021-04-01"

// Tagger is the Azure family's post-create marker write (#1120 unit 12,
// substrate.WriteARMTagsAPI): a merge of tags onto one ARM scope through
// the Tags API, authenticated as one provider block's credential. It
// lives beside the sweep client because it shares that client's
// credential chain, endpoints and error reading, as kubesweep's
// LabelPatcher shares the cluster client.
type Tagger struct {
	endpoint string
	scope    string
	cred     azcore.TokenCredential
	http     *http.Client
	creds    Credentials
}

// NewTagger builds a Tagger over the caller's credential, endpoints and
// HTTP client ([Config]; its Markers are not read).
func NewTagger(cfg Config) *Tagger {
	return &Tagger{
		endpoint: strings.TrimSuffix(cfg.Endpoints.ResourceManager, "/"),
		scope:    scope(cfg.Endpoints.Audience),
		cred:     cfg.Credential,
		http:     cfg.httpClient(),
		creds:    cfg.Credentials,
	}
}

// tagsPatch is the body of Tags - Update At Scope (TagsPatchResource).
type tagsPatch struct {
	Operation  string `json:"operation"`
	Properties struct {
		Tags map[string]string `json:"tags"`
	} `json:"properties"`
}

// MergeTags merges tags onto the resource, resource group or subscription
// whose ARM id is id:
//
//	PATCH {resourceManager}{id}/providers/Microsoft.Resources/tags/default?api-version=2021-04-01
//	{"operation": "Merge", "properties": {"tags": {...}}}
//
// Merge replaces the value of a key the object already carries and adds the
// rest; every other tag on the object is left as it is. ARM compares tag
// keys case-insensitively, so a key the object already carries in another
// spelling is updated in place under that spelling rather than added
// beside it. Any answer other than 2xx is an error naming the status, the
// ARM error code and message, and the credential the call was made as.
func (t *Tagger) MergeTags(ctx context.Context, id string, tags map[string]string) error {
	if !strings.HasPrefix(id, "/") {
		return fmt.Errorf("%q is not an ARM id the Tags API can address", id)
	}
	var body tagsPatch
	body.Operation = "Merge"
	body.Properties.Tags = tags
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	tok, err := t.cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{t.scope}})
	if err != nil {
		return fmt.Errorf("getting a token for %s as %s: %w", t.scope, t.creds, err)
	}
	url := t.endpoint + strings.TrimSuffix(id, "/") + "/providers/Microsoft.Resources/tags/default?api-version=" + TagsAPIVersion
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.http.Do(req)
	if err != nil {
		return fmt.Errorf("merging tags onto %s: %w", id, err)
	}
	answer, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return fmt.Errorf("reading the Tags API's answer for %s: %w", id, err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
		return nil
	}
	var env armError
	_ = json.Unmarshal(answer, &env)
	message := strings.TrimSpace(env.Error.Message)
	if message == "" {
		message = strings.TrimSpace(string(answer))
	}
	if env.Error.Code != "" {
		return fmt.Errorf("the Tags API answered %d %s merging tags onto %s as %s: %s", resp.StatusCode, env.Error.Code, id, t.creds, message)
	}
	return fmt.Errorf("the Tags API answered %d merging tags onto %s as %s: %s", resp.StatusCode, id, t.creds, message)
}
