// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package gcpsweep

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"google.golang.org/api/googleapi"

	"github.com/intentius/choudoufu/pkg/markerkeys"
)

// The per-type listers (INTENTIUS/choudoufu#2264, after the maintainer's
// ruling on INTENTIUS/plunge#15): each covered google type is listed through
// its own service's list method, read from that service's own store, and
// kept by the estate's label. Cloud Asset Inventory's index lags, so a
// search that finds one object of a server-assigned type is no proof there
// is not a second one created moments ago. Two concurrent creates of such a
// type (CONCURRENCY.md's second race) both succeed, and the next plan can
// only name the collision if its list shows both objects. A service's own
// list does, because it reads the store the create wrote to.
//
// The covered types are the labelled google types whose identity the API
// assigns at create time (live/gcp-survey.json's "server-assigned" verdict)
// and whose list is scoped by project and location: those are the types the
// collision claim is about. A client-named type needs no list for it, since
// the API refuses a second create of the same name. One server-assigned
// type is not covered: google_dialogflow_cx_intent lists under its agent,
// which is another resource's attribute at plan time, so it binds through
// Asset Inventory as before, and the collision claim does not cover it.
// google_assured_workloads_workload, which lists under an organization, is
// listed with the types that have no Asset name (handwrittenlist.go).
//
// Every other covered type is derived (servicelist.go): its Asset name's
// parent collection, listed at a service in [Services], the rule plunge's
// gcplist uses, so the two repos list the same types.

// TypeLister lists one covered type's objects carrying an estate's label,
// all pages read, or an error and no objects. [Client] serves it; a test
// stands in for it.
type TypeLister interface {
	// ListType returns every object of typeName at scope whose tofu-estate
	// label is exactly estate, with the same typed errors as
	// [Searcher.Search]. A type [Covered] does not name is an error.
	ListType(ctx context.Context, typeName string, scope ListScope, estate string) ([]Asset, error)
}

// ListScope is where one list call is made: a project, and for a type
// listed per location, the location.
type ListScope struct {
	Project string
	// Location is the region or location the type's list is under, or ""
	// for a type listed per project ([TypeList.Location] is "").
	Location string
	// Collection is a derived type's list path with every variable filled
	// ([TypeList.Scope]), and Query its encoded query, if any. "" for a
	// hand-written type, whose path is filled from Project and Location;
	// for a derived type it is filled the same way when the path needs no
	// other variable.
	Collection string
	Query      string
}

func (s ListScope) String() string {
	out := "project " + s.Project
	if s.Location != "" {
		out += " location " + s.Location
	}
	if s.Collection != "" {
		out += " (" + s.Collection + ")"
	}
	return out
}

// TypeList is how one covered type is listed.
type TypeList struct {
	// Type is the google resource type: "google_vertex_ai_index_endpoint".
	Type string
	// Service is the API the list is made on, which is what the project has
	// to have enabled: "aiplatform.googleapis.com".
	Service string
	// AssetType is the Cloud Asset type the same object is indexed as,
	// which a listed [Asset] carries so that it reads like a search hit:
	// "aiplatform.googleapis.com/IndexEndpoint".
	AssetType string
	// Location is the resource argument the type's location comes from
	// ("region" or "location"), or "" for a type listed per project.
	// FixedLocation is the location of a type the API lists under one
	// location only ("global").
	Location      string
	FixedLocation string
	// Path is the list path under the service's base URL, in the
	// provider's ReplaceVars syntax: "projects/{{project}}/locations/{{location}}/indexEndpoints".
	// {{location}} stands for the location whatever the argument is called.
	Path string
	// Field is the list response's array field: "indexEndpoints".
	Field string
	// Filter is whether the list method takes an AIP-160 filter, so the
	// label test is also made server side. The test is always made again
	// on what comes back.
	Filter bool
	// Permission is the IAM permission the list needs.
	Permission string
	// Endpoint is the provider's base path for the type's product.
	Endpoint Endpoint

	// Derived is set for a type listed by the rule in servicelist.go (its
	// Asset name's parent collection, at a service in [Services]) rather
	// than by a row of [typeLists]. Asset is its Asset name template, Query
	// any query parameters its list takes (Cloud Storage's project), and
	// Consistency Google's statement of the list's consistency, if any.
	Derived     bool
	Asset       string
	Query       map[string]string
	Consistency string

	// For a type with no Asset name (handwrittenlist.go): AssetHost is the
	// host its documented Asset name carries when that is not Service
	// (Cloud SQL's is cloudsql.googleapis.com), NameByLocation names each
	// listed object from its own location field (GKE, listed at
	// locations/-), and Import is its import ID's shape, the type's
	// ImportSyntax in pkg/identity.
	AssetHost      string
	NameByLocation bool
	Import         string
}

// Endpoint is one hashicorp/google product's base path: the provider block
// argument and environment variable that override it, and its default.
// Default may carry {{region}} or {{location}}, which the provider fills
// with the resource's own location, and so does [Client.ListType].
type Endpoint struct {
	Arg     string
	Env     string
	Default string
}

// The products the covered types belong to, with hashicorp/google 8.6.0's
// base paths (google/services/<product>/product.go at the pin).
var (
	endpointVertexAI = Endpoint{Arg: "vertex_ai_custom_endpoint", Env: "GOOGLE_VERTEX_AI_CUSTOM_ENDPOINT", Default: "https://{{region}}-aiplatform.googleapis.com/v1/"}
	endpointDocAI    = Endpoint{Arg: "document_ai_custom_endpoint", Env: "GOOGLE_DOCUMENT_AI_CUSTOM_ENDPOINT", Default: "https://{{location}}-documentai.googleapis.com/v1/"}
	endpointTransc   = Endpoint{Arg: "transcoder_custom_endpoint", Env: "GOOGLE_TRANSCODER_CUSTOM_ENDPOINT", Default: "https://transcoder.googleapis.com/v1/"}
	endpointSvcDir   = Endpoint{Arg: "service_directory_custom_endpoint", Env: "GOOGLE_SERVICE_DIRECTORY_CUSTOM_ENDPOINT", Default: "https://servicedirectory.googleapis.com/v1/"}
	endpointReCAPTCH = Endpoint{Arg: "recaptcha_enterprise_custom_endpoint", Env: "GOOGLE_RECAPTCHA_ENTERPRISE_CUSTOM_ENDPOINT", Default: "https://recaptchaenterprise.googleapis.com/v1/"}
	endpointAD       = Endpoint{Arg: "active_directory_custom_endpoint", Env: "GOOGLE_ACTIVE_DIRECTORY_CUSTOM_ENDPOINT", Default: "https://managedidentities.googleapis.com/v1/"}
)

// typeLists is every covered type. Each path is the collection the
// provider's own create call posts to (its resource source at the pin), and
// each field the list response's array as the API reference names it.
var typeLists = map[string]TypeList{
	"google_vertex_ai_dataset": {
		Service: "aiplatform.googleapis.com", AssetType: "aiplatform.googleapis.com/Dataset",
		Location: "region", Path: "projects/{{project}}/locations/{{location}}/datasets", Field: "datasets",
		Filter: true, Permission: "aiplatform.datasets.list", Endpoint: endpointVertexAI,
	},
	"google_vertex_ai_index": {
		Service: "aiplatform.googleapis.com", AssetType: "aiplatform.googleapis.com/Index",
		Location: "region", Path: "projects/{{project}}/locations/{{location}}/indexes", Field: "indexes",
		Filter: true, Permission: "aiplatform.indexes.list", Endpoint: endpointVertexAI,
	},
	"google_vertex_ai_index_endpoint": {
		Service: "aiplatform.googleapis.com", AssetType: "aiplatform.googleapis.com/IndexEndpoint",
		Location: "region", Path: "projects/{{project}}/locations/{{location}}/indexEndpoints", Field: "indexEndpoints",
		Filter: true, Permission: "aiplatform.indexEndpoints.list", Endpoint: endpointVertexAI,
	},
	"google_vertex_ai_reasoning_engine": {
		Service: "aiplatform.googleapis.com", AssetType: "aiplatform.googleapis.com/ReasoningEngine",
		Location: "region", Path: "projects/{{project}}/locations/{{location}}/reasoningEngines", Field: "reasoningEngines",
		Filter: true, Permission: "aiplatform.reasoningEngines.list", Endpoint: endpointVertexAI,
	},
	"google_vertex_ai_tensorboard": {
		Service: "aiplatform.googleapis.com", AssetType: "aiplatform.googleapis.com/Tensorboard",
		Location: "region", Path: "projects/{{project}}/locations/{{location}}/tensorboards", Field: "tensorboards",
		Filter: true, Permission: "aiplatform.tensorboards.list", Endpoint: endpointVertexAI,
	},
	"google_document_ai_schema": {
		Service: "documentai.googleapis.com", AssetType: "documentai.googleapis.com/Schema",
		Location: "location", Path: "projects/{{project}}/locations/{{location}}/schemas", Field: "schemas",
		Permission: "documentai.schemas.list", Endpoint: endpointDocAI,
	},
	"google_transcoder_job": {
		Service: "transcoder.googleapis.com", AssetType: "transcoder.googleapis.com/Job",
		Location: "location", Path: "projects/{{project}}/locations/{{location}}/jobs", Field: "jobs",
		Filter: true, Permission: "transcoder.jobs.list", Endpoint: endpointTransc,
	},
	"google_service_directory_namespace": {
		Service: "servicedirectory.googleapis.com", AssetType: "servicedirectory.googleapis.com/Namespace",
		Location: "location", Path: "projects/{{project}}/locations/{{location}}/namespaces", Field: "namespaces",
		Filter: true, Permission: "servicedirectory.namespaces.list", Endpoint: endpointSvcDir,
	},
	"google_recaptcha_enterprise_key": {
		Service: "recaptchaenterprise.googleapis.com", AssetType: "recaptchaenterprise.googleapis.com/Key",
		Path: "projects/{{project}}/keys", Field: "keys",
		Permission: "recaptchaenterprise.keys.list", Endpoint: endpointReCAPTCH,
	},
	"google_active_directory_domain": {
		Service: "managedidentities.googleapis.com", AssetType: "managedidentities.googleapis.com/Domain",
		FixedLocation: "global", Path: "projects/{{project}}/locations/{{location}}/domains", Field: "domains",
		Filter: true, Permission: "managedidentities.domains.list", Endpoint: endpointAD,
	},
}

// NotCovered is each labelled server-assigned google type with no per-type
// lister, and why. It binds through Asset Inventory alone, and the
// collision claim does not cover it. google_assured_workloads_workload left
// this list in INTENTIUS/choudoufu#2274: its list is under the organization
// its own required argument names (handwrittenlist.go).
var NotCovered = map[string]string{
	"google_dialogflow_cx_intent": "its list is under its agent (parent), which is another resource's attribute at plan time",
}

// Covered returns how typeName is listed, and whether it is covered: a
// hand-written row of [typeLists], else a type derived from its Asset name
// and its service (servicelist.go).
func Covered(typeName string) (TypeList, bool) {
	if t, ok := typeLists[typeName]; ok {
		t.Type = typeName
		return t, true
	}
	t, ok := derivedLists[typeName]
	return t, ok
}

// CoveredTypes is every covered type, sorted: the hand-written ones and the
// derived ones.
func CoveredTypes() []string {
	out := make([]string, 0, len(typeLists)+len(derivedLists))
	for t := range typeLists {
		out = append(out, t)
	}
	for t := range derivedLists {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// listPageSize is the page size every list asks for. Each API caps it
// lower or higher as it will; a smaller page than asked is still paged.
const listPageSize = 100

// ListType is [TypeLister.ListType] over the type's own service,
// authenticated as this client's credential.
func (c *Client) ListType(ctx context.Context, typeName string, scope ListScope, estate string) ([]Asset, error) {
	t, ok := Covered(typeName)
	if !ok {
		return nil, fmt.Errorf("%s has no per-type lister", typeName)
	}
	if scope.Project == "" {
		return nil, fmt.Errorf("listing %s: no project", typeName)
	}
	location := scope.Location
	if t.FixedLocation != "" {
		location = t.FixedLocation
	}
	if t.Location != "" && location == "" {
		return nil, fmt.Errorf("listing %s in project %s: no %s is known to list under", typeName, scope.Project, t.Location)
	}
	if c.http == nil {
		return nil, fmt.Errorf("listing %s: this client has no HTTP transport", typeName)
	}
	if t.Derived {
		return c.listDerived(ctx, t, scope, estate)
	}
	base := c.attrs.endpointFor(t.Endpoint)
	base = strings.NewReplacer("{{region}}", location, "{{location}}", location).Replace(base)
	path := strings.NewReplacer("{{project}}", url.PathEscape(scope.Project), "{{location}}", url.PathEscape(location)).Replace(t.Path)
	listURL := strings.TrimRight(base, "/") + "/" + path
	if err := c.attrs.publicCall(t.Service, listURL); err != nil {
		return nil, err
	}

	var out []Asset
	pages, results := 0, 0
	token := ""
	for {
		q := url.Values{}
		q.Set("pageSize", fmt.Sprint(listPageSize))
		if t.Filter {
			q.Set("filter", Filter(c.keys, estate))
		}
		if token != "" {
			q.Set("pageToken", token)
		}
		items, next, err := c.listPage(ctx, listURL+"?"+q.Encode(), t.Field, scope.Project)
		if err != nil {
			err = classifyFor(scope.Project, c.creds, err, t.Service, t.Permission)
			if pages > 0 {
				return nil, &PartialError{Project: scope.Project, PagesRead: pages, ResultsRead: results, Err: err}
			}
			return nil, err
		}
		pages++
		results += len(items)
		for _, it := range items {
			if it.Name == "" || it.Labels[c.keys.Owner] != c.keys.OwnerValue(estate) {
				continue
			}
			out = append(out, Asset{
				AssetType:  t.AssetType,
				Name:       "//" + t.Service + "/" + strings.TrimPrefix(it.Name, "/"),
				Project:    "projects/" + scope.Project,
				Location:   location,
				Labels:     it.Labels,
				CreateTime: it.CreateTime,
				UpdateTime: it.UpdateTime,
			})
		}
		if next == "" {
			return out, nil
		}
		if next == token {
			return nil, &PartialError{Project: scope.Project, PagesRead: pages, ResultsRead: results,
				Err: fmt.Errorf("the %s list returned page token %q again, so paging would never end", typeName, token)}
		}
		token = next
	}
}

// Filter is the AIP-160 filter a list that takes one is sent: the estate's
// owner label under keys, exactly.
func Filter(keys markerkeys.Keys, estate string) string {
	return "labels." + keys.Owner + "=" + keys.OwnerValue(estate)
}

// listItem is the fields every covered type's list item has in common. A
// Compute Engine item's name is bare and its selfLink carries its path.
type listItem struct {
	Name       string            `json:"name"`
	SelfLink   string            `json:"selfLink"`
	Labels     map[string]string `json:"labels"`
	CreateTime string            `json:"createTime"`
	UpdateTime string            `json:"updateTime"`
	// Location is a GKE cluster's own location. ResourceLabels (GKE) and
	// Settings.UserLabels (Cloud SQL) are where those two APIs keep the
	// labels; listPage moves them into Labels.
	Location       string            `json:"location"`
	ResourceLabels map[string]string `json:"resourceLabels"`
	Settings       struct {
		UserLabels map[string]string `json:"userLabels"`
	} `json:"settings"`
}

// listPage GETs one page and returns its items and next page token. A
// non-2xx answer is the API's error, decoded as googleapi decodes it, so
// [classifyFor] reads its status and details.
func (c *Client) listPage(ctx context.Context, u, field, project string) ([]listItem, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	if c.attrs.UserProjectOverride {
		quota := c.attrs.BillingProject
		if quota == "" {
			quota = project
		}
		req.Header.Set("X-Goog-User-Project", quota)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if err := googleapi.CheckResponse(resp); err != nil {
		return nil, "", err
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	var page map[string]json.RawMessage
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, "", fmt.Errorf("reading the list response from %s: %w", u, err)
	}
	var items []listItem
	if raw, ok := page[field]; ok {
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, "", fmt.Errorf("reading %s from the list response from %s: %w", field, u, err)
		}
	}
	for i := range items {
		if len(items[i].Labels) > 0 {
			continue
		}
		if len(items[i].ResourceLabels) > 0 {
			items[i].Labels = items[i].ResourceLabels
		} else if len(items[i].Settings.UserLabels) > 0 {
			items[i].Labels = items[i].Settings.UserLabels
		}
	}
	var next string
	if raw, ok := page["nextPageToken"]; ok {
		if err := json.Unmarshal(raw, &next); err != nil {
			return nil, "", fmt.Errorf("reading nextPageToken from the list response from %s: %w", u, err)
		}
	}
	return items, next, nil
}

// endpointFor is the base path the provider would call e's product at: the
// block's custom endpoint argument, else its environment variable, else
// the default with the universe domain in place of googleapis.com.
func (a Attrs) endpointFor(e Endpoint) string {
	if v := a.Endpoints[e.Arg]; v != "" {
		return v
	}
	if a.UniverseDomain != "" && a.UniverseDomain != "googleapis.com" {
		return strings.Replace(e.Default, "googleapis.com", a.UniverseDomain, 1)
	}
	return e.Default
}

// typeListEndpoints is every endpoint argument the covered types read, once.
func typeListEndpoints() []Endpoint {
	seen := map[string]bool{}
	var out []Endpoint
	for _, name := range CoveredTypes() {
		t, _ := Covered(name)
		e := t.Endpoint
		if seen[e.Arg] {
			continue
		}
		seen[e.Arg] = true
		out = append(out, e)
	}
	return out
}
