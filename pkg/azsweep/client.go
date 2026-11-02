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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/intentius/choudoufu/pkg/markerkeys"
)

// Resource is one resource or resource group Resource Graph holds with
// this estate's tag, as the query projected it.
//
// Resource Graph is eventually consistent, the Azure counterpart of the
// AWS tagging index's lag: a resource deleted moments ago may still be
// listed, with its tags as they were, which is why the family answers
// MarkerOutlivesObject true (#1122). A listed Resource is one the index
// last saw carrying the tag, not proof it exists now; a caller proposing
// a removal reads the resource itself before acting.
type Resource struct {
	// ID is the ARM id, in the casing Resource Graph returns it. ARM ids
	// are case-insensitive; compare them with strings.EqualFold.
	ID string
	// Type is the ARM type, which Resource Graph lowercases:
	// "microsoft.network/virtualnetworks".
	Type           string
	Name           string
	ResourceGroup  string
	SubscriptionID string
	Location       string
	// Tags are the resource's tags as carried, the estate's among them
	// under whatever casing its writer used.
	Tags map[string]string
	// ManagedBy is ARM's managedBy: the id of the resource that manages
	// this one (an AKS cluster on its node resource group, a managed
	// application on its resources), or "" when nothing does. Set, the
	// resource is controller-held evidence (#1122) for the family to
	// judge; this client does not exclude it.
	ManagedBy string
}

// Searcher is what a sweep asks of one credential's client, so a test can
// stand in for the API.
type Searcher interface {
	// Search returns every resource in subscription carrying the tag
	// tofu-estate=estate (the key in any case), all pages read, or an
	// error and no resources.
	Search(ctx context.Context, subscription, estate string) ([]Resource, error)
}

// TypeSearcher is [Searcher] narrowed by ARM type: every resource in
// subscription carrying the estate's tag whose type is one of armTypes
// (compared case-insensitively). live-plan -resource asks it for the cone's
// types alone, where the estate sweep asks [Searcher.Search] for everything
// (GitHub issue #2260).
type TypeSearcher interface {
	SearchTypes(ctx context.Context, subscription, estate string, armTypes []string) ([]Resource, error)
}

// APIVersion is the Resource Graph API version every query is sent at.
const APIVersion = "2022-10-01"

// PageSize is the $top every query asks for: the API's maximum.
const PageSize = 1000

// MaxThrottleRetries is how many 429s one page's request waits out before
// the query is given up as [ThrottledError]. MaxThrottleWait is the
// longest single wait it will take; an answer asking for longer is given
// up at once rather than holding a plan open.
const (
	MaxThrottleRetries = 4
	MaxThrottleWait    = 60 * time.Second
)

// defaultThrottleWait is the wait taken on a 429 that names none.
// Resource Graph's quota window is five seconds (Azure's "Guidance for
// throttled requests in Azure Resource Graph").
const defaultThrottleWait = 5 * time.Second

// Client is [Searcher] over the Resource Graph API, authenticated as one
// provider block's credential.
type Client struct {
	endpoint string
	scope    string
	cred     azcore.TokenCredential
	http     *http.Client
	// subscription is the provider block's subscription_id, "" when the
	// caller names none.
	subscription string
	creds        Credentials
	keys         markerkeys.Keys
	// sleep waits d or until ctx ends; tests replace it to record the
	// waits instead of taking them.
	sleep func(ctx context.Context, d time.Duration) error
}

// New builds a client over the caller's credential, endpoints and HTTP
// client ([Config]). No token is requested until [Client.Search].
func New(cfg Config) (*Client, error) {
	if err := cfg.Markers.Validate(); err != nil {
		return nil, err
	}
	if cfg.Credential == nil {
		return nil, fmt.Errorf("the Azure estate sweep was given no credential; the caller builds the credential it calls with")
	}
	return &Client{
		endpoint:     strings.TrimSuffix(cfg.Endpoints.ResourceManager, "/"),
		scope:        scope(cfg.Endpoints.Audience),
		cred:         cfg.Credential,
		http:         cfg.httpClient(),
		subscription: cfg.Subscription,
		creds:        cfg.Credentials,
		keys:         cfg.Markers,
		sleep:        sleepCtx,
	}, nil
}

// Subscription is the provider block's subscription_id, the one
// subscription this client is set to query when the configuration names
// no other: what a [Coverage] built from provider blocks keys the client
// by.
func (c *Client) Subscription() string { return c.subscription }

// Credentials is how this client authenticates.
func (c *Client) Credentials() Credentials { return c.creds }

// Query is the KQL for estate's tag.
//
// The plain form, `where tags['tofu-estate'] == 'prod'`, is wrong here:
// KQL's dynamic property access is case-sensitive, and ARM keeps a tag
// key in the casing its writer used, so a resource whose configuration
// set Tofu-Estate (#1122 keeps that spelling rather than add a second,
// lowercase key ARM would refuse) would never be found. So a copy of the
// tag bag is expanded one key per row (mv-expand over markerTag, leaving
// tags whole for the projection), the key is read with bag_keys, and
// compared with =~, KQL's case-insensitive equality. ARM refuses two keys
// that differ only by case, so a resource yields at most one row; the
// value is compared exactly, since ARM tag values are case-sensitive.
//
// resourcecontainers is unioned in for resource groups, which the
// resources table does not hold and which an estate declares and tags
// like any other resource (azurerm_resource_group). Subscriptions and
// management groups are in resourcecontainers too and are left out:
// they are not objects an estate creates.
//
// id is projected, which Resource Graph requires for $skipToken paging
// (without it the result is truncated at one page).
func Query(keys markerkeys.Keys, estate string) string {
	return strings.Join([]string{
		"resources",
		"| union (resourcecontainers | where type =~ 'microsoft.resources/subscriptions/resourcegroups')",
		"| where isnotempty(tags)",
		"| extend markerTag = tags",
		"| mv-expand markerTag",
		"| extend markerKey = tostring(bag_keys(markerTag)[0])",
		"| where markerKey =~ " + kqlString(keys.Owner) + " and tostring(markerTag[markerKey]) == " + kqlString(keys.OwnerValue(estate)),
		"| project id, type, name, resourceGroup, subscriptionId, location, tags, managedBy",
	}, "\n")
}

// TypesQuery is [Query] narrowed to armTypes: the same estate-tag match,
// keeping only rows whose type is one of them. An empty armTypes is the
// plain [Query].
func TypesQuery(keys markerkeys.Keys, estate string, armTypes []string) string {
	q := Query(keys, estate)
	if len(armTypes) == 0 {
		return q
	}
	quoted := make([]string, len(armTypes))
	for i, t := range armTypes {
		quoted[i] = kqlString(strings.ToLower(t))
	}
	lines := strings.Split(q, "\n")
	// Before the projection, which is the last line.
	last := lines[len(lines)-1]
	lines = append(lines[:len(lines)-1], "| where type in~ ("+strings.Join(quoted, ", ")+")", last)
	return strings.Join(lines, "\n")
}

// kqlString is s as a single-quoted KQL string literal, with backslash
// and quote escaped.
func kqlString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`)
	return "'" + r.Replace(s) + "'"
}

// queryRequest is the body of a Resource Graph query (QueryRequest,
// api-version 2022-10-01).
type queryRequest struct {
	Subscriptions []string     `json:"subscriptions"`
	Query         string       `json:"query"`
	Options       queryOptions `json:"options"`
}

type queryOptions struct {
	Top          int    `json:"$top"`
	SkipToken    string `json:"$skipToken,omitempty"`
	ResultFormat string `json:"resultFormat"`
}

// queryResponse is the part of a QueryResponse a sweep reads.
// resultTruncated is "true" or "false", a string enum in the API's
// definition; it is decoded loosely so that a JSON boolean also reads.
type queryResponse struct {
	TotalRecords    int64           `json:"totalRecords"`
	Count           int64           `json:"count"`
	ResultTruncated json.RawMessage `json:"resultTruncated"`
	SkipToken       string          `json:"$skipToken"`
	Data            []row           `json:"data"`
}

type row struct {
	ID             string         `json:"id"`
	Type           string         `json:"type"`
	Name           string         `json:"name"`
	ResourceGroup  string         `json:"resourceGroup"`
	SubscriptionID string         `json:"subscriptionId"`
	Location       string         `json:"location"`
	Tags           map[string]any `json:"tags"`
	ManagedBy      string         `json:"managedBy"`
}

// Search reads every page of subscription's query for estate's tag. It
// returns all of them or none: a failure on any page is an error, typed
// when the API's answer is one this package names, and a [PartialError]
// wrapping it when it came after the first page. A 200 with no rows is an
// empty answer and a nil error.
//
// Each row is checked again here: its own tofu-estate tag, the key in any
// case, must be exactly estate, and its subscriptionId must be the one
// asked about. A row failing either is dropped, so a query that matched
// too widely can never read another estate's resource as this one's. A
// row repeated (by id, case-insensitively) is kept once.
func (c *Client) Search(ctx context.Context, subscription, estate string) ([]Resource, error) {
	return c.search(ctx, subscription, estate, Query(c.keys, estate))
}

// SearchTypes implements [TypeSearcher]: [Client.Search] with [TypesQuery],
// and every row checked again against armTypes as well as the estate.
func (c *Client) SearchTypes(ctx context.Context, subscription, estate string, armTypes []string) ([]Resource, error) {
	all, err := c.search(ctx, subscription, estate, TypesQuery(c.keys, estate, armTypes))
	if err != nil || len(armTypes) == 0 {
		return all, err
	}
	keep := make(map[string]bool, len(armTypes))
	for _, t := range armTypes {
		keep[strings.ToLower(t)] = true
	}
	out := all[:0]
	for _, r := range all {
		if keep[strings.ToLower(r.Type)] {
			out = append(out, r)
		}
	}
	return out, nil
}

func (c *Client) search(ctx context.Context, subscription, estate, query string) ([]Resource, error) {
	var out []Resource
	seen := map[string]bool{}
	pages, rows := 0, 0
	token := ""
	for {
		resp, err := c.page(ctx, subscription, query, token)
		if err != nil {
			if pages > 0 {
				return nil, &PartialError{Subscription: subscription, PagesRead: pages, RowsRead: rows, Err: err}
			}
			return nil, err
		}
		pages++
		rows += len(resp.Data)
		for _, r := range resp.Data {
			if !strings.EqualFold(r.SubscriptionID, subscription) {
				continue
			}
			tags := stringTags(r.Tags)
			if v, ok := OwnerTag(c.keys, tags); !ok || v != c.keys.OwnerValue(estate) {
				continue
			}
			key := strings.ToLower(r.ID)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, Resource{
				ID:             r.ID,
				Type:           r.Type,
				Name:           r.Name,
				ResourceGroup:  r.ResourceGroup,
				SubscriptionID: r.SubscriptionID,
				Location:       r.Location,
				Tags:           tags,
				ManagedBy:      r.ManagedBy,
			})
		}
		if resp.SkipToken == "" {
			if truncated(resp.ResultTruncated) {
				return nil, &PartialError{Subscription: subscription, PagesRead: pages, RowsRead: rows,
					Err: fmt.Errorf("Resource Graph marked the result truncated and gave no $skipToken to read the rest")}
			}
			sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].ID) < strings.ToLower(out[j].ID) })
			return out, nil
		}
		if resp.SkipToken == token {
			return nil, &PartialError{Subscription: subscription, PagesRead: pages, RowsRead: rows,
				Err: fmt.Errorf("the API returned $skipToken %q again, so paging would never end", token)}
		}
		token = resp.SkipToken
	}
}

// OwnerTag is the owner tag's value in tags (keys.Owner: choudoufu's
// tofu-estate), its key matched case-insensitively (#1122): ARM tag keys
// are case-insensitive, so Tofu-Estate is the marker as surely as
// tofu-estate is.
func OwnerTag(keys markerkeys.Keys, tags map[string]string) (string, bool) {
	if v, ok := tags[keys.Owner]; ok {
		return v, true
	}
	for k, v := range tags {
		if strings.EqualFold(k, keys.Owner) {
			return v, true
		}
	}
	return "", false
}

func stringTags(in map[string]any) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		switch t := v.(type) {
		case string:
			out[k] = t
		case nil:
			out[k] = ""
		default:
			out[k] = fmt.Sprint(t)
		}
	}
	return out
}

func truncated(raw json.RawMessage) bool {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	return strings.EqualFold(s, "true")
}

// page sends one page's query, waiting out 429s as the service's headers
// say, and classifies any other failure.
func (c *Client) page(ctx context.Context, subscription, query, token string) (*queryResponse, error) {
	body, err := json.Marshal(queryRequest{
		Subscriptions: []string{subscription},
		Query:         query,
		Options:       queryOptions{Top: PageSize, SkipToken: token, ResultFormat: "objectArray"},
	})
	if err != nil {
		return nil, err
	}
	url := c.endpoint + "/providers/" + ResourceProvider + "/resources?api-version=" + APIVersion
	for attempt := 1; ; attempt++ {
		tok, err := c.cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{c.scope}})
		if err != nil {
			return nil, fmt.Errorf("getting a token for %s as %s: %w", c.scope, c.creds, err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+tok.Token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("querying Resource Graph for subscription %s: %w", subscription, err)
		}
		data, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("reading Resource Graph's answer for subscription %s: %w", subscription, err)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			wait := throttleWait(resp.Header)
			if attempt > MaxThrottleRetries || wait > MaxThrottleWait {
				return nil, &ThrottledError{Subscription: subscription, Attempts: attempt, RetryAfter: wait}
			}
			if err := c.sleep(ctx, wait); err != nil {
				return nil, err
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return nil, classify(subscription, c.creds, resp.StatusCode, data)
		}
		var out queryResponse
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, fmt.Errorf("decoding Resource Graph's answer for subscription %s: %w", subscription, err)
		}
		// The quota headers come on every answer. A page that spent the
		// last of the window waits the window out before the next page
		// rather than walking into a 429.
		if out.SkipToken != "" && resp.Header.Get(headerQuotaRemaining) == "0" {
			if wait, ok := quotaReset(resp.Header); ok && wait <= MaxThrottleWait {
				if err := c.sleep(ctx, wait); err != nil {
					return nil, err
				}
			}
		}
		return &out, nil
	}
}

// Resource Graph's throttling headers (Azure's "Guidance for throttled
// requests in Azure Resource Graph"): how many requests the caller has
// left in the current window, and how long until the window resets, as
// hh:mm:ss.
const (
	headerQuotaRemaining = "x-ms-user-quota-remaining"
	headerQuotaResets    = "x-ms-user-quota-resets-after"
)

// throttleWait is how long a 429 says to wait: Retry-After (seconds, or
// an HTTP date) when the answer carries it, as ARM documents for every
// 429, else Resource Graph's own x-ms-user-quota-resets-after, else
// [defaultThrottleWait].
func throttleWait(h http.Header) time.Duration {
	if v := strings.TrimSpace(h.Get("Retry-After")); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
			return time.Duration(secs) * time.Second
		}
		if at, err := http.ParseTime(v); err == nil {
			if d := time.Until(at); d > 0 {
				return d
			}
			return 0
		}
	}
	if d, ok := quotaReset(h); ok {
		return d
	}
	return defaultThrottleWait
}

// quotaReset reads x-ms-user-quota-resets-after, hh:mm:ss.
func quotaReset(h http.Header) (time.Duration, bool) {
	v := strings.TrimSpace(h.Get(headerQuotaResets))
	if v == "" {
		return 0, false
	}
	parts := strings.Split(v, ":")
	if len(parts) != 3 {
		return 0, false
	}
	var total time.Duration
	units := []time.Duration{time.Hour, time.Minute, time.Second}
	for i, p := range parts {
		f, err := strconv.ParseFloat(p, 64)
		if err != nil || f < 0 {
			return 0, false
		}
		total += time.Duration(f * float64(units[i]))
	}
	return total, true
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Coverage is which client queries which subscription: the subscription
// id a provider block names, keyed to the client its credential built.
// Keys are compared case-insensitively.
type Coverage map[string]Searcher

func (cov Coverage) lookup(subscription string) Searcher {
	if s, ok := cov[subscription]; ok {
		return s
	}
	for k, s := range cov {
		if strings.EqualFold(k, subscription) {
			return s
		}
	}
	return nil
}

// SubscriptionResult is one subscription's sweep: its resources, or the
// error that stopped it and no resources. Resources nil with Err nil is a
// subscription that holds nothing tagged for the estate.
type SubscriptionResult struct {
	Subscription string
	Resources    []Resource
	Err          error
}

// Sweep queries each of subscriptions for estate's tag with the client cov
// assigns it, in subscriptions' order, each subscription once (subscription
// ids compared case-insensitively). A subscription cov has no client for
// is an [UncoveredSubscriptionError] result, never a missing row. A failed
// subscription does not stop the others: each result stands alone, so
// the discovery leg can warn of the one that failed, refuse on
// [NotRegisteredError], and still read the ones that answered.
func Sweep(ctx context.Context, cov Coverage, subscriptions []string, estate string) []SubscriptionResult {
	seen := map[string]bool{}
	var out []SubscriptionResult
	for _, sub := range subscriptions {
		key := strings.ToLower(sub)
		if seen[key] {
			continue
		}
		seen[key] = true
		s := cov.lookup(sub)
		if s == nil {
			out = append(out, SubscriptionResult{Subscription: sub, Err: &UncoveredSubscriptionError{Subscription: sub}})
			continue
		}
		res, err := s.Search(ctx, sub, estate)
		if err != nil {
			out = append(out, SubscriptionResult{Subscription: sub, Err: err})
			continue
		}
		out = append(out, SubscriptionResult{Subscription: sub, Resources: res})
	}
	return out
}
