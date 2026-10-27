// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package gcpsweep

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/pkg/identity"
)

// The derived per-type listers (INTENTIUS/choudoufu#2264 after the ruling on
// INTENTIUS/plunge#15: every covered GCP type, the same in both repos). The
// hand-written [typeLists] cover the ten server-assigned types. Every other
// covered type is derived the way plunge's pkg/backend/plunge/gcplist derives
// it, so the two repos list the same set:
//
//   - pkg/identity's [identity.GoogleAssetImports] gives the type's Asset name
//     ("projects/{{project}}/topics/{{name}}"), generated from the provider
//     source by tools/gcp-row-gen.
//   - Its host is in [Services], which says the service's REST root, the
//     provider block's custom endpoint argument for it, and the field its list
//     response carries the objects in.
//
// The list call is the Asset name's parent collection: the template with its
// last segment (the object's own name) dropped, "projects/{{project}}/topics"
// for a topic. That is AIP-132's standard List, and the Compute Engine and
// Cloud Storage JSON APIs serve the same collection paths. A bucket's Asset
// name is its bare name, and the JSON API lists a project's buckets at
// b?project=.
//
// The set of covered types is checked against plunge's by
// testdata/covered-types.txt, which plunge carries a copy of
// (TestCoveredTypesMatchTheSharedList).

// ListService is one Google Cloud service a derived type is listed through.
type ListService struct {
	// Host is the service host Asset names carry: "pubsub.googleapis.com".
	Host string
	// Root is the service's REST root with its version, as hashicorp/google
	// 8.6.0's default base path spells it: "https://pubsub.googleapis.com/v1/".
	Root string
	// EndpointArg is the provider block's custom endpoint argument for the
	// service, and EndpointEnv the environment variable the provider reads
	// when the block leaves it unset. The value replaces Root, version
	// included, as the provider reads it; floci-gcp sets it.
	EndpointArg string
	EndpointEnv string
	// Items is the list response's field holding the objects. "" is AIP-132's:
	// the collection's own name ("topics"). Compute Engine and Cloud Storage
	// use "items".
	Items string
	// Consistency is Google's statement of the list's consistency, when it
	// publishes one.
	Consistency string
}

// Services are the services derived types are listed through, by host. The
// hosts are plunge gcplist.Services' hosts; the endpoint arguments are
// hashicorp/google's spelling of the same arguments (live/e2e/lib/gcp-provider-endpoints.txt,
// with api_gateway_custom_endpoint from google-beta, which serves the API
// Gateway types).
var Services = map[string]ListService{}

func init() {
	for _, s := range []ListService{
		{Host: "compute.googleapis.com", Root: "https://compute.googleapis.com/compute/v1/",
			EndpointArg: "compute_custom_endpoint", EndpointEnv: "GOOGLE_COMPUTE_CUSTOM_ENDPOINT", Items: "items"},
		{Host: "storage.googleapis.com", Root: "https://storage.googleapis.com/storage/v1/",
			EndpointArg: "storage_custom_endpoint", EndpointEnv: "GOOGLE_STORAGE_CUSTOM_ENDPOINT", Items: "items",
			Consistency: "bucket listing is strongly consistent (https://cloud.google.com/storage/docs/consistency)"},
		{Host: "pubsub.googleapis.com", Root: "https://pubsub.googleapis.com/v1/",
			EndpointArg: "pubsub_custom_endpoint", EndpointEnv: "GOOGLE_PUBSUB_CUSTOM_ENDPOINT"},
		{Host: "run.googleapis.com", Root: "https://run.googleapis.com/v2/",
			EndpointArg: "cloud_run_v2_custom_endpoint", EndpointEnv: "GOOGLE_CLOUD_RUN_V2_CUSTOM_ENDPOINT"},
		{Host: "cloudfunctions.googleapis.com", Root: "https://cloudfunctions.googleapis.com/v2/",
			EndpointArg: "cloudfunctions2_custom_endpoint", EndpointEnv: "GOOGLE_CLOUDFUNCTIONS2_CUSTOM_ENDPOINT"},
		{Host: "secretmanager.googleapis.com", Root: "https://secretmanager.googleapis.com/v1/",
			EndpointArg: "secret_manager_custom_endpoint", EndpointEnv: "GOOGLE_SECRET_MANAGER_CUSTOM_ENDPOINT"},
		{Host: "artifactregistry.googleapis.com", Root: "https://artifactregistry.googleapis.com/v1/",
			EndpointArg: "artifact_registry_custom_endpoint", EndpointEnv: "GOOGLE_ARTIFACT_REGISTRY_CUSTOM_ENDPOINT"},
		{Host: "cloudkms.googleapis.com", Root: "https://cloudkms.googleapis.com/v1/",
			EndpointArg: "kms_custom_endpoint", EndpointEnv: "GOOGLE_KMS_CUSTOM_ENDPOINT"},
		{Host: "dns.googleapis.com", Root: "https://dns.googleapis.com/dns/v1/",
			EndpointArg: "dns_custom_endpoint", EndpointEnv: "GOOGLE_DNS_CUSTOM_ENDPOINT"},
		{Host: "redis.googleapis.com", Root: "https://redis.googleapis.com/v1/",
			EndpointArg: "redis_custom_endpoint", EndpointEnv: "GOOGLE_REDIS_CUSTOM_ENDPOINT"},
		{Host: "memcache.googleapis.com", Root: "https://memcache.googleapis.com/v1/",
			EndpointArg: "memcache_custom_endpoint", EndpointEnv: "GOOGLE_MEMCACHE_CUSTOM_ENDPOINT"},
		{Host: "file.googleapis.com", Root: "https://file.googleapis.com/v1/",
			EndpointArg: "filestore_custom_endpoint", EndpointEnv: "GOOGLE_FILESTORE_CUSTOM_ENDPOINT"},
		{Host: "spanner.googleapis.com", Root: "https://spanner.googleapis.com/v1/",
			EndpointArg: "spanner_custom_endpoint", EndpointEnv: "GOOGLE_SPANNER_CUSTOM_ENDPOINT"},
		{Host: "alloydb.googleapis.com", Root: "https://alloydb.googleapis.com/v1/",
			EndpointArg: "alloydb_custom_endpoint", EndpointEnv: "GOOGLE_ALLOYDB_CUSTOM_ENDPOINT"},
		{Host: "apigateway.googleapis.com", Root: "https://apigateway.googleapis.com/v1/",
			EndpointArg: "api_gateway_custom_endpoint", EndpointEnv: "GOOGLE_API_GATEWAY_CUSTOM_ENDPOINT"},
		{Host: "eventarc.googleapis.com", Root: "https://eventarc.googleapis.com/v1/",
			EndpointArg: "eventarc_custom_endpoint", EndpointEnv: "GOOGLE_EVENTARC_CUSTOM_ENDPOINT"},
		{Host: "certificatemanager.googleapis.com", Root: "https://certificatemanager.googleapis.com/v1/",
			EndpointArg: "certificate_manager_custom_endpoint", EndpointEnv: "GOOGLE_CERTIFICATE_MANAGER_CUSTOM_ENDPOINT"},
		{Host: "privateca.googleapis.com", Root: "https://privateca.googleapis.com/v1/",
			EndpointArg: "privateca_custom_endpoint", EndpointEnv: "GOOGLE_PRIVATECA_CUSTOM_ENDPOINT"},
		{Host: "clouddeploy.googleapis.com", Root: "https://clouddeploy.googleapis.com/v1/",
			EndpointArg: "clouddeploy_custom_endpoint", EndpointEnv: "GOOGLE_CLOUDDEPLOY_CUSTOM_ENDPOINT"},
		{Host: "networkservices.googleapis.com", Root: "https://networkservices.googleapis.com/v1/",
			EndpointArg: "network_services_custom_endpoint", EndpointEnv: "GOOGLE_NETWORK_SERVICES_CUSTOM_ENDPOINT"},
		{Host: "networksecurity.googleapis.com", Root: "https://networksecurity.googleapis.com/v1/",
			EndpointArg: "network_security_custom_endpoint", EndpointEnv: "GOOGLE_NETWORK_SECURITY_CUSTOM_ENDPOINT"},
		{Host: "networkconnectivity.googleapis.com", Root: "https://networkconnectivity.googleapis.com/v1/",
			EndpointArg: "network_connectivity_custom_endpoint", EndpointEnv: "GOOGLE_NETWORK_CONNECTIVITY_CUSTOM_ENDPOINT"},
		{Host: "dataproc.googleapis.com", Root: "https://dataproc.googleapis.com/v1/",
			EndpointArg: "dataproc_custom_endpoint", EndpointEnv: "GOOGLE_DATAPROC_CUSTOM_ENDPOINT"},
		{Host: "workstations.googleapis.com", Root: "https://workstations.googleapis.com/v1/",
			EndpointArg: "workstations_custom_endpoint", EndpointEnv: "GOOGLE_WORKSTATIONS_CUSTOM_ENDPOINT"},
		{Host: "datastream.googleapis.com", Root: "https://datastream.googleapis.com/v1/",
			EndpointArg: "datastream_custom_endpoint", EndpointEnv: "GOOGLE_DATASTREAM_CUSTOM_ENDPOINT"},
		{Host: "metastore.googleapis.com", Root: "https://metastore.googleapis.com/v1/",
			EndpointArg: "dataproc_metastore_custom_endpoint", EndpointEnv: "GOOGLE_DATAPROC_METASTORE_CUSTOM_ENDPOINT"},
		{Host: "managedkafka.googleapis.com", Root: "https://managedkafka.googleapis.com/v1/",
			EndpointArg: "managed_kafka_custom_endpoint", EndpointEnv: "GOOGLE_MANAGED_KAFKA_CUSTOM_ENDPOINT"},
		{Host: "parametermanager.googleapis.com", Root: "https://parametermanager.googleapis.com/v1/",
			EndpointArg: "parameter_manager_custom_endpoint", EndpointEnv: "GOOGLE_PARAMETER_MANAGER_CUSTOM_ENDPOINT"},
	} {
		Services[s.Host] = s
	}
	derivedLists = deriveTypeLists()
}

// derivedLists is every derived type, by Terraform type, computed once.
var derivedLists map[string]TypeList

func deriveTypeLists() map[string]TypeList {
	out := map[string]TypeList{}
	for tf := range identity.GoogleAssetImports {
		if _, handWritten := typeLists[tf]; handWritten {
			continue
		}
		if t, _, ok := derive(tf); ok {
			out[tf] = t
		}
	}
	// The covered types with no Asset name (handwrittenlist.go) are listed
	// the same way, from their written-out path.
	for tf, t := range handwrittenLists {
		t.Type = tf
		t.Derived = true
		out[tf] = t
	}
	return out
}

// Uncovered says why typeName has no per-type lister, or "" when it has one.
func Uncovered(typeName string) string {
	if _, ok := Covered(typeName); ok {
		return ""
	}
	if why, ok := NotCovered[typeName]; ok {
		return why
	}
	_, why, _ := derive(typeName)
	return why
}

// derive is typeName's list from its Asset name and its service, or why it
// has none. It is plunge gcplist.Lookup's rule, so both repos cover the same
// types.
func derive(typeName string) (TypeList, string, bool) {
	row, ok := identity.GoogleAssetImports[typeName]
	if !ok {
		return TypeList{}, fmt.Sprintf("pkg/identity has no Asset name for %s, so there is no collection to list it from", typeName), false
	}
	svc, ok := Services[row.Host]
	if !ok {
		return TypeList{}, fmt.Sprintf("there is no per-service lister for %s (%s)", row.Host, typeName), false
	}
	t := TypeList{
		Type:        typeName,
		Service:     row.Host,
		Asset:       row.Asset,
		Derived:     true,
		Consistency: svc.Consistency,
		Endpoint:    Endpoint{Arg: svc.EndpointArg, Env: svc.EndpointEnv, Default: svc.Root},
	}
	if row.Host == "storage.googleapis.com" {
		if row.Asset != "{{bucket}}" && row.Asset != "{{name}}" {
			return TypeList{}, fmt.Sprintf("%s's Asset name %q is not a bucket's", typeName, row.Asset), false
		}
		t.Path, t.Query = "b", map[string]string{"project": "{{project}}"}
		t.Field = svc.Items
		t.AssetType = "storage.googleapis.com/Bucket"
		t.Permission = "storage.buckets.list"
		return t, "", true
	}
	segs := strings.Split(row.Asset, "/")
	if len(segs) < 2 || !isPlaceholder(segs[len(segs)-1]) || strings.Contains(segs[len(segs)-2], "{{") {
		return TypeList{}, fmt.Sprintf("%s's Asset name %q does not end in a collection and a name", typeName, row.Asset), false
	}
	coll := segs[len(segs)-2]
	t.Path = strings.Join(segs[:len(segs)-1], "/")
	t.Field = svc.Items
	if t.Field == "" {
		t.Field = coll
	}
	t.AssetType = row.Host + "/" + assetKind(coll)
	t.Permission = strings.TrimSuffix(row.Host, ".googleapis.com") + "." + coll + ".list"
	return t, "", true
}

// assetKind is the Cloud Asset kind a collection's objects are indexed as,
// spelled the way the asset types are: "topics" is Topic, "keyRings" KeyRing,
// "repositories" Repository, "addresses" Address, "batches" Batch. A listed object carries it
// so it reads like a search hit and [substrate.ControllerHeld] sees a Compute
// instance or disk, or a Cloud Run service, as it does a search hit.
func assetKind(coll string) string {
	s := coll
	switch {
	case strings.HasSuffix(s, "ies"):
		s = strings.TrimSuffix(s, "ies") + "y"
	case strings.HasSuffix(s, "sses"), strings.HasSuffix(s, "xes"), strings.HasSuffix(s, "ches"),
		strings.HasSuffix(s, "shes"), strings.HasSuffix(s, "uses"):
		s = strings.TrimSuffix(s, "es")
	case strings.HasSuffix(s, "s"):
		s = strings.TrimSuffix(s, "s")
	}
	if s == "" {
		return coll
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

var placeholderRe = regexp.MustCompile(`\{\{%?([a-zA-Z0-9_]+)\}\}`)

func isPlaceholder(seg string) bool {
	m := placeholderRe.FindStringSubmatchIndex(seg)
	return m != nil && m[0] == 0 && m[1] == len(seg)
}

// Placeholders lists the variables a derived type's list path and query
// need, in order, each once.
func (t TypeList) Placeholders() []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		for _, m := range placeholderRe.FindAllStringSubmatch(s, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, m[1])
			}
		}
	}
	add(t.Path)
	keys := make([]string, 0, len(t.Query))
	for k := range t.Query {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		add(t.Query[k])
	}
	return out
}

// ScopeVars are the placeholders that say where a list is made rather than
// under which parent object: a value given as a self link or a full name
// ("projects/p", ".../zones/z") counts by its last segment.
var ScopeVars = map[string]bool{"project": true, "region": true, "zone": true, "location": true}

// ProviderScope is the provider block's project, region and zone: what a
// list scope's variable falls back to when the resource's own block names
// none ([ProviderScope.Default]).
type ProviderScope struct {
	Project string
	Region  string
	Zone    string
}

// Default is the value the scope variable name takes when the resource's
// block leaves it unset. project, region and zone are the provider block's,
// as hashicorp/google defaults them. location is the provider block's
// region: a block with no location is listed in the provider's region, the
// one rule both engines apply (GitHub issue #2273, ruled 2026-10-26: plunge
// already fell back this way, and the google provider places a regional
// resource with no location there). Any other variable has no provider
// default, and "" is returned.
func (p ProviderScope) Default(name string) string {
	switch name {
	case "project":
		return p.Project
	case "region", "location":
		return p.Region
	case "zone":
		return p.Zone
	}
	return ""
}

// Scope fills a derived type's list path and query from vars, or names the
// variables it has no value for. The scope's Location is the region, zone or
// location the path is under, if any.
func (t TypeList) Scope(vars func(name string) (string, bool)) (ListScope, error) {
	var missing []string
	fill := func(tmpl string) string {
		return placeholderRe.ReplaceAllStringFunc(tmpl, func(m string) string {
			name := placeholderRe.FindStringSubmatch(m)[1]
			v, ok := vars(name)
			if !ok || v == "" {
				missing = append(missing, name)
				return m
			}
			return v
		})
	}
	s := ListScope{Collection: fill(t.Path)}
	if len(t.Query) > 0 {
		q := url.Values{}
		for k, v := range t.Query {
			q.Set(k, fill(v))
		}
		s.Query = q.Encode()
	}
	if len(missing) > 0 {
		return ListScope{}, fmt.Errorf("no value is known for %s", strings.Join(missing, ", "))
	}
	s.Project, _ = vars("project")
	for _, name := range []string{"location", "region", "zone"} {
		if !strings.Contains(t.Path, "{{"+name+"}}") {
			continue
		}
		s.Location, _ = vars(name)
		break
	}
	return s, nil
}

// objectName is a listed object's Asset path. An AIP resource's name is its
// full relative name already. A Compute Engine object's name is bare, and its
// selfLink carries the path. Anything else is named under the collection,
// except a bucket, whose Asset name is its bare name. plunge gcplist's
// objectName is the same rule.
func (t TypeList) objectName(it listItem, coll string) string {
	if t.NameByLocation {
		// GKE, listed at locations/-: each cluster names its own location.
		if it.Name == "" || it.Location == "" {
			return ""
		}
		return strings.TrimSuffix(coll, "/locations/-/clusters") + "/locations/" + it.Location + "/clusters/" + it.Name
	}
	if strings.Contains(it.Name, "/") {
		return strings.TrimPrefix(strings.TrimPrefix(it.Name, "//"+t.Service+"/"), "/")
	}
	if i := strings.Index(it.SelfLink, "/projects/"); i >= 0 {
		return it.SelfLink[i+1:]
	}
	if it.Name == "" {
		return ""
	}
	if t.Service == "storage.googleapis.com" {
		return it.Name
	}
	return coll + "/" + it.Name
}

// Names reports whether a, a Cloud Asset Inventory hit, is an object of t: of
// t's asset type for a hand-written type, and named by t's Asset template for
// a derived one, whose asset type is only spelled from its collection.
func (t TypeList) Names(a Asset) bool {
	if !t.Derived {
		return a.AssetType == t.AssetType
	}
	_, ok := importIDOf(t, a.Name)
	return ok
}

// listDerived is [Client.ListType] for a derived type: every page of its
// collection at scope, kept by the estate's label client side. A list path
// with no Collection in scope is filled from its project and location.
func (c *Client) listDerived(ctx context.Context, t TypeList, scope ListScope, estate string) ([]Asset, error) {
	if scope.Collection == "" {
		filled, err := t.Scope(func(name string) (string, bool) {
			switch name {
			case "project":
				return scope.Project, scope.Project != ""
			case "location", "region", "zone":
				return scope.Location, scope.Location != ""
			}
			return "", false
		})
		if err != nil {
			return nil, fmt.Errorf("listing %s in %s: %w", t.Type, scope, err)
		}
		scope = filled
	}
	base := c.attrs.endpointFor(t.Endpoint)
	base = strings.NewReplacer("{{region}}", scope.Location, "{{location}}", scope.Location).Replace(base)
	listURL := strings.TrimRight(base, "/") + "/" + scope.Collection
	if err := c.attrs.publicCall(t.Service, listURL); err != nil {
		return nil, err
	}
	host := t.Service
	if t.AssetHost != "" {
		host = t.AssetHost
	}
	permission := t.Permission
	var out []Asset
	pages, results := 0, 0
	token := ""
	for {
		q, _ := url.ParseQuery(scope.Query)
		if token != "" {
			q.Set("pageToken", token)
		}
		u := listURL
		if len(q) > 0 {
			u += "?" + q.Encode()
		}
		items, next, err := c.listPage(ctx, u, t.Field, scope.Project)
		if err != nil {
			err = classifyFor(scope.Project, c.creds, err, t.Service, permission)
			if pages > 0 {
				return nil, &PartialError{Project: scope.Project, PagesRead: pages, ResultsRead: results, Err: err}
			}
			return nil, err
		}
		pages++
		results += len(items)
		for _, it := range items {
			name := t.objectName(it, scope.Collection)
			if name == "" || it.Labels[c.keys.Owner] != c.keys.OwnerValue(estate) {
				continue
			}
			location := scope.Location
			if t.NameByLocation {
				location = it.Location
			}
			out = append(out, Asset{
				AssetType:  t.AssetType,
				Name:       "//" + host + "/" + name,
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
				Err: fmt.Errorf("the %s list returned page token %q again, so paging would never end", t.Type, token)}
		}
		token = next
	}
}
