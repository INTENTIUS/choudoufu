// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package gcpsweep

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	cloudasset "google.golang.org/api/cloudasset/v1"
	"google.golang.org/api/option"

	"github.com/intentius/choudoufu/pkg/markerkeys"
)

// Asset is one resource the Asset Inventory index holds with this estate's
// label, as searchAllResources returned it.
//
// The index is eventually consistent, the GCP counterpart of the AWS
// tagging index's lag (internal/live/discovery's tagging leg): a resource
// created moments ago may be missing from a search, and one deleted
// moments ago may still be listed, with its labels as they were. Google
// documents no bound; its own guidance is minutes
// (https://cloud.google.com/asset-inventory/docs/overview#data_freshness_and_consistency).
// A listed Asset is therefore a resource the index last saw carrying the
// label, not proof it exists now, and a resource missing from the answer
// may exist. A caller proposing a removal reads the resource itself before
// acting, and a caller asked whether a just-applied create is visible has
// to allow for the lag.
type Asset struct {
	// AssetType is the Cloud Asset type: "compute.googleapis.com/Network".
	AssetType string
	// Name is the full resource name:
	// "//compute.googleapis.com/projects/p/global/networks/n".
	Name string
	// Project is the project the resource is in, as the index spells it:
	// "projects/" and the project NUMBER, never the id the caller searched
	// by.
	Project string
	// Location is the resource's region, zone or "global".
	Location string
	// Labels are the resource's labels, the estate's among them.
	Labels map[string]string
	// CreateTime and UpdateTime are the resource's own timestamps, RFC
	// 3339, or "" for an asset type that reports none.
	CreateTime string
	UpdateTime string
}

// Searcher is what a sweep asks of one credential's client, so a test can
// stand in for the API.
type Searcher interface {
	// Search returns every resource in project carrying the label
	// tofu-estate=estate, all pages read, or an error and no resources.
	Search(ctx context.Context, project, estate string) ([]Asset, error)
}

// AddressSearcher is the narrower search live-mv asks before it rewrites a
// tofu-address label (internal/live/mv): which resources in project carry
// tofu-estate=estate and tofu-address=address together. [Client] serves
// it; a test stands in for it as it does for [Searcher].
type AddressSearcher interface {
	// SearchAddress returns every resource in project carrying both
	// labels, all pages read, or an error and no resources, with the same
	// typed errors as [Searcher.Search].
	SearchAddress(ctx context.Context, project, estate, address string) ([]Asset, error)
}

// PageSize is the page size every search asks for: the API's maximum.
const PageSize = 500

// ReadMask is the fields every search asks for, which are exactly
// [Asset]'s.
const ReadMask = "name,assetType,project,location,labels,createTime,updateTime"

// Client is [Searcher] over the Cloud Asset API, authenticated as one
// provider block's credential.
type Client struct {
	svc   *cloudasset.Service
	attrs Attrs
	creds Credentials
	keys  markerkeys.Keys
	// http is the authenticated transport the per-type listers call their
	// services through ([Client.ListType]).
	http *http.Client
}

// Config is what the caller supplies a [Client] (GitHub issue #2281): the
// marker vocabulary and the authenticated HTTP client. The package builds
// no credential of its own.
type Config struct {
	// Markers names the owner and address label keys a search matches.
	Markers markerkeys.Keys
	// HTTPClient carries every call, the Cloud Asset search and each
	// per-type list, already authenticated as the caller decided. Nothing
	// here reads a credentials file, searches for Application Default
	// Credentials or asks the metadata server. [HTTPClient] builds one the
	// way hashicorp/google's provider does; a caller with its own rule
	// passes its own.
	HTTPClient *http.Client
	// Credentials is what diagnostics name the credential as.
	Credentials Credentials
}

// New builds a client from a provider block's arguments, with the
// provider's environment fallbacks applied ([Attrs.WithEnv]), over the
// caller's HTTP client. Nothing is called until [Client.Search].
func New(ctx context.Context, a Attrs, cfg Config) (*Client, error) {
	a = a.WithEnv(os.Getenv)
	if refused := a.refusedMarks(); len(refused) > 0 {
		return nil, fmt.Errorf("the google provider block sets %s from a sensitive value, which the estate sweep does not read; set it from a non-sensitive value so the sweep calls as and where the provider does", strings.Join(refused, " and "))
	}
	if err := cfg.Markers.Validate(); err != nil {
		return nil, err
	}
	if cfg.HTTPClient == nil {
		return nil, fmt.Errorf("the GCP estate sweep was given no HTTP client; the caller builds the credential it calls with (gcpsweep.HTTPClient builds the provider's)")
	}
	opts := []option.ClientOption{option.WithHTTPClient(cfg.HTTPClient)}
	if a.UniverseDomain != "" {
		opts = append(opts, option.WithUniverseDomain(a.UniverseDomain))
	}
	if a.CloudAssetEndpoint != "" {
		opts = append(opts, option.WithEndpoint(serviceRoot(a.CloudAssetEndpoint)))
	}
	svc, err := cloudasset.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("building the Cloud Asset client (%s): %w", cfg.Credentials, err)
	}
	return &Client{svc: svc, attrs: a, creds: cfg.Credentials, keys: cfg.Markers, http: cfg.HTTPClient}, nil
}

// Region is the provider block's region, the location a per-type list of
// a type whose block names none is made under ([TypeList.Location]).
func (c *Client) Region() string { return c.attrs.Region }

// Zone is the provider block's zone, the zone hashicorp/google puts a zonal
// resource in when its block names none.
func (c *Client) Zone() string { return c.attrs.Zone }

// ProviderScope is the provider block's project, region and zone, the
// defaults a list scope falls back to ([ProviderScope.Default]).
func (c *Client) ProviderScope() ProviderScope {
	return ProviderScope{Project: c.attrs.Project, Region: c.attrs.Region, Zone: c.attrs.Zone}
}

// Project is the provider block's project, the one project this client is
// set to search when the configuration names no other: what a [Coverage]
// built from provider blocks keys the client by. "" when the block and
// the environment name none.
func (c *Client) Project() string { return c.attrs.Project }

// Credentials is how this client authenticates.
func (c *Client) Credentials() Credentials { return c.creds }

// Query is the search query for estate's owner label under keys.
func Query(keys markerkeys.Keys, estate string) string {
	return "labels." + keys.Owner + "=" + keys.OwnerValue(estate)
}

// AddressQuery is the search query for estate's owner label and one
// address label value together, under keys. Both terms are exact matches,
// joined with AND as the search syntax spells a conjunction.
func AddressQuery(keys markerkeys.Keys, estate, address string) string {
	return Query(keys, estate) + " AND labels." + keys.Address + "=" + keys.AddressValue(address)
}

// SearchAddress is [Client.Search] narrowed by one more label term: every
// resource in project whose tofu-estate is exactly estate and whose
// tofu-address is exactly address. Results are checked against both labels
// again, for the reason Search checks the estate.
func (c *Client) SearchAddress(ctx context.Context, project, estate, address string) ([]Asset, error) {
	return c.search(ctx, project, AddressQuery(c.keys, estate, address), func(labels map[string]string) bool {
		return labels[c.keys.Owner] == c.keys.OwnerValue(estate) && labels[c.keys.Address] == c.keys.AddressValue(address)
	})
}

// Search reads every page of project's search for estate's label. It
// returns all of them or none: a failure on any page is an error, typed
// ([ServiceDisabledError], [PermissionDeniedError]) when the API's answer
// is one of those, and a [PartialError] wrapping it when it came after the
// first page. A 200 with no results is an empty answer and a nil error.
//
// A result whose own tofu-estate label is not exactly estate is dropped:
// the query asks for an exact match, and this is the same test made again
// on what came back, so that a token match can never read a neighbouring
// estate's resource as this one's.
func (c *Client) Search(ctx context.Context, project, estate string) ([]Asset, error) {
	return c.search(ctx, project, Query(c.keys, estate), func(labels map[string]string) bool {
		return labels[c.keys.Owner] == c.keys.OwnerValue(estate)
	})
}

// search pages one query through project, keeping the results keep
// accepts by their labels.
func (c *Client) search(ctx context.Context, project, query string, keep func(map[string]string) bool) ([]Asset, error) {
	if err := c.attrs.publicCall(Service, c.attrs.CloudAssetEndpoint); err != nil {
		return nil, err
	}
	var out []Asset
	pages, results := 0, 0
	token := ""
	for {
		call := c.svc.V1.SearchAllResources("projects/" + project).
			Query(query).
			PageSize(PageSize).
			ReadMask(ReadMask).
			Context(ctx)
		if token != "" {
			call = call.PageToken(token)
		}
		if c.attrs.UserProjectOverride {
			quota := c.attrs.BillingProject
			if quota == "" {
				quota = project
			}
			call.Header().Set("X-Goog-User-Project", quota)
		}
		resp, err := call.Do()
		if err != nil {
			err = classify(project, c.creds, err)
			if pages > 0 {
				return nil, &PartialError{Project: project, PagesRead: pages, ResultsRead: results, Err: err}
			}
			return nil, err
		}
		pages++
		results += len(resp.Results)
		for _, r := range resp.Results {
			if r == nil || !keep(r.Labels) {
				continue
			}
			out = append(out, Asset{
				AssetType:  r.AssetType,
				Name:       r.Name,
				Project:    r.Project,
				Location:   r.Location,
				Labels:     r.Labels,
				CreateTime: r.CreateTime,
				UpdateTime: r.UpdateTime,
			})
		}
		if resp.NextPageToken == "" {
			return out, nil
		}
		if resp.NextPageToken == token {
			return nil, &PartialError{Project: project, PagesRead: pages, ResultsRead: results,
				Err: fmt.Errorf("the API returned page token %q again, so paging would never end", token)}
		}
		token = resp.NextPageToken
	}
}

// Coverage is which client searches which project: the project id a
// provider block names, keyed to the client its credential built.
type Coverage map[string]Searcher

// ProjectResult is one project's sweep: its assets, or the error that
// stopped it and no assets. Assets nil with Err nil is a project that
// holds nothing labelled for the estate.
type ProjectResult struct {
	Project string
	Assets  []Asset
	Err     error
}

// Sweep searches each of projects for estate's label with the client cov
// assigns it, in projects' order, each project once. A project cov has no
// client for is an [UncoveredProjectError] result, never a missing row. A
// failed project does not stop the others: each result stands alone, so
// the discovery leg can report a gap for the project that failed and
// still read the ones that answered.
func Sweep(ctx context.Context, cov Coverage, projects []string, estate string) []ProjectResult {
	seen := map[string]bool{}
	var out []ProjectResult
	for _, p := range projects {
		if seen[p] {
			continue
		}
		seen[p] = true
		s, ok := cov[p]
		if !ok || s == nil {
			out = append(out, ProjectResult{Project: p, Err: &UncoveredProjectError{Project: p}})
			continue
		}
		assets, err := s.Search(ctx, p, estate)
		if err != nil {
			out = append(out, ProjectResult{Project: p, Err: err})
			continue
		}
		out = append(out, ProjectResult{Project: p, Assets: assets})
	}
	return out
}
