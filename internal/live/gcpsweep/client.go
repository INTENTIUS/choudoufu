// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package gcpsweep

import (
	"context"
	"fmt"
	"os"
	"strings"

	cloudasset "google.golang.org/api/cloudasset/v1"

	"github.com/intentius/choudoufu/internal/live/markers"
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
}

// New builds a client from a provider block's arguments, with the
// provider's environment fallbacks applied ([Attrs.WithEnv]). Nothing is
// called until [Client.Search], except that a credentials file is read
// and Application Default Credentials are looked up now, so a missing
// credential fails here rather than per project.
func New(ctx context.Context, a Attrs) (*Client, error) {
	a = a.WithEnv(os.Getenv)
	if refused := a.refusedMarks(); len(refused) > 0 {
		return nil, fmt.Errorf("the google provider block sets %s from a sensitive value, which the estate sweep does not read; set it from a non-sensitive value so the sweep calls as and where the provider does", strings.Join(refused, " and "))
	}
	if a.ExternalCredentials {
		return nil, fmt.Errorf("the google provider block authenticates through an external_credentials block, which the estate sweep cannot use; configure credentials, access_token or Application Default Credentials for it")
	}
	opts, err := a.clientOptions(ctx)
	if err != nil {
		return nil, err
	}
	svc, err := cloudasset.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("building the Cloud Asset client (%s): %w", a.Auth(), err)
	}
	return &Client{svc: svc, attrs: a, creds: a.Auth()}, nil
}

// Project is the provider block's project, the one project this client is
// set to search when the configuration names no other: what a [Coverage]
// built from provider blocks keys the client by. "" when the block and
// the environment name none.
func (c *Client) Project() string { return c.attrs.Project }

// Credentials is how this client authenticates.
func (c *Client) Credentials() Credentials { return c.creds }

// Query is the search query for estate's label.
func Query(estate string) string {
	return "labels." + markers.TagEstate + "=" + estate
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
	var out []Asset
	pages, results := 0, 0
	token := ""
	for {
		call := c.svc.V1.SearchAllResources("projects/" + project).
			Query(Query(estate)).
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
			if r == nil || r.Labels[markers.TagEstate] != estate {
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
