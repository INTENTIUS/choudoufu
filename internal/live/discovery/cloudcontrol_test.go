// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/registry"
)

const ccEstate = "cc-e2e"

// ---------------------------------------------------------------------------
// Fixtures: a config with one block per fake type, hand-built rather than
// loaded from testdata/ - declaredInstances only ever dereferences
// block.DeclRange, so a literal *configs.Config is enough and avoids a new
// fixture directory for types that exist only to exercise the Cloud Control
// fallback.
// ---------------------------------------------------------------------------

// ccConfig builds a minimal *configs.Config declaring one resource block
// "<typeName>.x" per typeName given.
func ccConfig(typeNames ...string) *configs.Config {
	resources := make(map[string]*configs.Resource, len(typeNames))
	for _, tn := range typeNames {
		addr := tn + ".x"
		resources[addr] = &configs.Resource{
			Mode:      addrs.ManagedResourceMode,
			Type:      tn,
			Name:      "x",
			DeclRange: hcl.Range{Filename: "cloudcontrol_test.go"},
		}
	}
	return &configs.Config{
		Module: &configs.Module{ManagedResources: resources},
	}
}

func ccRoster(t *testing.T, mapped map[string]string, listable map[string]bool, taggable map[string]bool) *registry.Roster {
	t.Helper()

	type mappingRow struct {
		TFType  string  `json:"tf_type"`
		CFNType *string `json:"cfn_type"`
		Via     string  `json:"via"`
	}
	var rows []mappingRow
	for tf, cfn := range mapped {
		cfn := cfn
		rows = append(rows, mappingRow{TFType: tf, CFNType: &cfn, Via: "name"})
	}
	mappingJSON, err := json.Marshal(struct {
		Rows []mappingRow `json:"rows"`
	}{Rows: rows})
	if err != nil {
		t.Fatalf("marshaling test mapping fixture: %v", err)
	}

	type registryEntry struct {
		TypeName string `json:"type_name"`
		Tagging  struct {
			Taggable bool `json:"taggable"`
		} `json:"tagging"`
		Handlers struct {
			List bool `json:"list"`
		} `json:"handlers"`
	}
	seen := map[string]bool{}
	var types []registryEntry
	for _, cfn := range mapped {
		if seen[cfn] {
			continue
		}
		seen[cfn] = true
		e := registryEntry{TypeName: cfn}
		e.Handlers.List = listable[cfn]
		e.Tagging.Taggable = taggable == nil || taggable[cfn]
		types = append(types, e)
	}
	registryJSON, err := json.Marshal(struct {
		Types []registryEntry `json:"types"`
	}{Types: types})
	if err != nil {
		t.Fatalf("marshaling test registry fixture: %v", err)
	}

	r, err := registry.Parse(mappingJSON, registryJSON)
	if err != nil {
		t.Fatalf("registry.Parse: %v", err)
	}
	return r
}

// ---------------------------------------------------------------------------
// A fake Cloud Control endpoint, one per test: httptest.Server dispatching
// on X-Amz-Target and the request body's TypeName, mirroring
// internal/live/cloudcontrol's own fake-server test pattern.
// ---------------------------------------------------------------------------

type ccResource struct {
	identifier string
	properties map[string]any // nil means "Properties omitted from the wire response"
}

// ccServer is a fake Cloud Control endpoint. listResources is keyed by CFN
// type; getResource is keyed by "CFNType identifier" and its value is
// either a *ccResource (success) or a string error code (failure - reuse
// cloudcontrol.CodeUnsupportedOperation etc). Missing from getResource means
// ResourceNotFoundException. calls records every operation invoked, in
// order, as "Operation:TypeName[:Identifier]".
type ccServer struct {
	t             *testing.T
	listResources map[string][]ccResource
	listErr       map[string]string // CFN type -> error code
	getResource   map[string]any    // "CFNType identifier" -> *ccResource or string error code

	// listResourcesAfterFirst, when set for a CFN type, is what every
	// ListResources call after the first for that type returns instead of
	// listResources[type] - the discovery-level fixture for the
	// UnsupportedOperation fallback: the enumeration scan's own
	// ListResources call is the first, and the fallback's re-list
	// ([cloudcontrol.GetResourceByIdentity]) is a later one, and only a
	// second distinct answer can prove the fallback actually re-listed
	// rather than reusing what the scan already had.
	listResourcesAfterFirst map[string][]ccResource
	listCallCount           map[string]int

	calls []string
}

func newCCServer(t *testing.T) *ccServer {
	t.Helper()
	return &ccServer{
		t:                       t,
		listResources:           map[string][]ccResource{},
		listErr:                 map[string]string{},
		getResource:             map[string]any{},
		listResourcesAfterFirst: map[string][]ccResource{},
		listCallCount:           map[string]int{},
	}
}

func (s *ccServer) start() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(s.handle))
}

func (s *ccServer) handle(w http.ResponseWriter, r *http.Request) {
	target := r.Header.Get("X-Amz-Target")
	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.t.Fatalf("decoding request body: %v", err)
	}
	typeName := body["TypeName"]

	switch target {
	case "CloudApiService.ListResources":
		s.calls = append(s.calls, "ListResources:"+typeName)
		if code, ok := s.listErr[typeName]; ok {
			s.writeError(w, code)
			return
		}
		s.listCallCount[typeName]++
		population := s.listResources[typeName]
		if s.listCallCount[typeName] > 1 {
			if alt, ok := s.listResourcesAfterFirst[typeName]; ok {
				population = alt
			}
		}
		var descs []map[string]string
		for _, res := range population {
			d := map[string]string{"Identifier": res.identifier}
			if res.properties != nil {
				b, _ := json.Marshal(res.properties)
				d["Properties"] = string(b)
			}
			descs = append(descs, d)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ResourceDescriptions": descs})

	case "CloudApiService.GetResource":
		identifier := body["Identifier"]
		s.calls = append(s.calls, fmt.Sprintf("GetResource:%s:%s", typeName, identifier))
		key := typeName + " " + identifier
		v, ok := s.getResource[key]
		if !ok {
			s.writeError(w, cloudcontrol.CodeResourceNotFoundError)
			return
		}
		if code, ok := v.(string); ok {
			s.writeError(w, code)
			return
		}
		res := v.(ccResource)
		d := map[string]string{"Identifier": res.identifier}
		if res.properties != nil {
			b, _ := json.Marshal(res.properties)
			d["Properties"] = string(b)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ResourceDescription": d})

	default:
		s.t.Fatalf("unexpected X-Amz-Target %q", target)
	}
}

func (s *ccServer) writeError(w http.ResponseWriter, code string) {
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"__type":  "com.amazonaws.cloudformation#" + code,
		"message": code,
	})
}

func tagsProps(estate, address string) map[string]any {
	return map[string]any{
		"Tags": []map[string]string{
			{"Key": TagEstate, "Value": estate},
			{"Key": TagAddress, "Value": address},
		},
	}
}

// ---------------------------------------------------------------------------
// Enumeration source selection
// ---------------------------------------------------------------------------

// TestEnumerationSourceNativeStaysPrimary: a type the provider can list
// natively is scanned through the provider, never through Cloud Control,
// even when CloudControl and Roster are both configured and would happily
// map it. The fake CC server fails the test outright if it is ever hit.
func TestEnumerationSourceNativeStaysPrimary(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-1", "aws_vpc.x")

	srv := newCCServer(t)
	server := srv.start()
	defer server.Close()

	cc := cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL})
	roster := ccRoster(t,
		map[string]string{"aws_vpc": "AWS::EC2::VPC"},
		map[string]bool{"AWS::EC2::VPC": true},
		nil,
	)

	cfg := ccConfig("aws_vpc")
	req := Request{
		Estate:       estateName,
		Config:       cfg,
		Resolutions:  []identity.Resolution{{Addr: mustAddr(t, "aws_vpc.x"), Class: identity.ClassNeedsDiscovery}},
		Provider:     cloud,
		CloudControl: cc,
		Roster:       roster,
	}
	res, diags := Discover(context.Background(), req)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}
	if _, ok := res.BindingFor(mustAddr(t, "aws_vpc.x")); !ok {
		t.Errorf("aws_vpc.x did not bind via the native path:\n%s", res)
	}
	if len(srv.calls) != 0 {
		t.Errorf("the Cloud Control fake server was called (%v) for a type with a native list resource", srv.calls)
	}
	scan, ok := res.ScanFor("aws_vpc")
	if !ok || scan.Source != SourceProvider {
		t.Errorf("aws_vpc scan source = %q, want %q", scan.Source, SourceProvider)
	}
}

// TestEnumerationSourceCloudControlFallback: a type absent from the
// provider's list schemas, but mapped and listable, is discovered through
// Cloud Control.
func TestEnumerationSourceCloudControlFallback(t *testing.T) {
	cloud := newFakeCloud() // does not know "aws_efs_file_system" at all

	srv := newCCServer(t)
	srv.listResources["AWS::EFS::FileSystem"] = []ccResource{
		{identifier: "fs-0123", properties: mergeProps(tagsProps(ccEstate, "aws_efs_file_system.x"), map[string]any{"FileSystemId": "fs-0123"})},
	}
	server := srv.start()
	defer server.Close()

	cc := cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL})
	roster := ccRoster(t,
		map[string]string{"aws_efs_file_system": "AWS::EFS::FileSystem"},
		map[string]bool{"AWS::EFS::FileSystem": true},
		nil,
	)

	req := Request{
		Estate:       ccEstate,
		Config:       ccConfig("aws_efs_file_system"),
		Resolutions:  []identity.Resolution{ccResolutionFor(t, "aws_efs_file_system")},
		Provider:     cloud,
		CloudControl: cc,
		Roster:       roster,
	}
	res, diags := Discover(context.Background(), req)
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Err())
	}
	b, ok := res.BindingFor(mustAddr(t, "aws_efs_file_system.x"))
	if !ok {
		t.Fatalf("aws_efs_file_system.x did not bind via the Cloud Control fallback:\n%s", res)
	}
	if b.ImportID != "fs-0123" {
		t.Errorf("ImportID = %q, want fs-0123", b.ImportID)
	}
	scan, ok := res.ScanFor("aws_efs_file_system")
	if !ok || scan.Source != SourceCloudControl || scan.CFNType != "AWS::EFS::FileSystem" {
		t.Errorf("scan = %+v, want Source=CLOUD_CONTROL CFNType=AWS::EFS::FileSystem", scan)
	}
}

// TestEnumerationSourceNeitherRefuses covers "neither": a type with no
// native list resource, and either unmapped or mapped-but-not-listable,
// keeps today's ProblemTypeNotListable refusal - the same outcome whether
// or not a Cloud Control client is configured.
func TestEnumerationSourceNeitherRefuses(t *testing.T) {
	tests := []struct {
		name   string
		mapped map[string]string
		listbl map[string]bool
	}{
		{"unmapped entirely", nil, nil},
		{"mapped but not listable", map[string]string{"aws_no_list": "AWS::Example::NoList"}, map[string]bool{"AWS::Example::NoList": false}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cloud := newFakeCloud()
			srv := newCCServer(t)
			server := srv.start()
			defer server.Close()

			cc := cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL})
			roster := ccRoster(t, tt.mapped, tt.listbl, nil)

			req := Request{
				Estate:       ccEstate,
				Config:       ccConfig("aws_no_list"),
				Resolutions:  []identity.Resolution{ccResolutionFor(t, "aws_no_list")},
				Provider:     cloud,
				CloudControl: cc,
				Roster:       roster,
			}
			res, diags := Discover(context.Background(), req)
			if !diags.HasErrors() {
				t.Fatalf("expected the existing refusal, got no errors:\n%s", res)
			}
			problems := res.ProblemsOfKind(ProblemTypeNotListable)
			if len(problems) != 1 || problems[0].TypeName != "aws_no_list" {
				t.Errorf("want one type-not-listable problem for aws_no_list, got %v", res.Problems)
			}
			if len(srv.calls) != 0 {
				t.Errorf("the Cloud Control fake server was called (%v) for a type with neither enumeration source", srv.calls)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Client-side tag filtering: via listed Properties, and via GetResource
// refinement.
// ---------------------------------------------------------------------------

func TestCloudControlTagsFromListedProperties(t *testing.T) {
	srv := newCCServer(t)
	srv.listResources["AWS::EFS::FileSystem"] = []ccResource{
		{identifier: "fs-tagged", properties: tagsProps(ccEstate, "aws_efs_file_system.x")},
	}
	server := srv.start()
	defer server.Close()

	res := runCCDiscovery(t, srv, server, "aws_efs_file_system", "AWS::EFS::FileSystem")
	if _, ok := res.BindingFor(mustAddr(t, "aws_efs_file_system.x")); !ok {
		t.Fatalf("did not bind via listed Properties.Tags:\n%s", res)
	}
	scan, _ := res.ScanFor("aws_efs_file_system")
	if scan.Refined != 0 {
		t.Errorf("Refined = %d, want 0 (Tags were already in the listing)", scan.Refined)
	}
	for _, c := range srv.calls {
		if strings.HasPrefix(c, "GetResource") {
			t.Errorf("GetResource was called (%v) even though the listing already carried Tags", srv.calls)
		}
	}
}

func TestCloudControlTagsViaGetResourceRefinement(t *testing.T) {
	srv := newCCServer(t)
	// The listing carries no Tags at all - Cloud Control's real behavior for
	// many types, per the issue.
	srv.listResources["AWS::EFS::FileSystem"] = []ccResource{
		{identifier: "fs-untagged-in-list", properties: map[string]any{"FileSystemId": "fs-untagged-in-list"}},
	}
	srv.getResource["AWS::EFS::FileSystem fs-untagged-in-list"] = ccResource{
		identifier: "fs-untagged-in-list",
		properties: tagsProps(ccEstate, "aws_efs_file_system.x"),
	}
	server := srv.start()
	defer server.Close()

	res := runCCDiscovery(t, srv, server, "aws_efs_file_system", "AWS::EFS::FileSystem")
	if _, ok := res.BindingFor(mustAddr(t, "aws_efs_file_system.x")); !ok {
		t.Fatalf("did not bind via the GetResource refinement:\n%s", res)
	}
	scan, _ := res.ScanFor("aws_efs_file_system")
	if scan.Refined != 1 {
		t.Errorf("Refined = %d, want 1", scan.Refined)
	}
}

// ---------------------------------------------------------------------------
// The UnsupportedOperation fallback, reached through the refinement path:
// GetResource refuses the type outright, ListResources on the same
// identifier still finds it.
// ---------------------------------------------------------------------------

func TestCloudControlUnsupportedOperationFallback(t *testing.T) {
	srv := newCCServer(t)
	cfnType := "AWS::EFS::FileSystem"
	// The enumeration scan's own ListResources call (the 1st) carries no
	// Tags, which is what triggers the refinement in the first place.
	srv.listResources[cfnType] = []ccResource{
		{identifier: "fs-floci", properties: map[string]any{"FileSystemId": "fs-floci"}},
	}
	// GetResource is refused outright for the type - floci's exact shape -
	// so the refinement falls back to a fresh ListResources
	// ([cloudcontrol.GetResourceByIdentity]), the 2nd call, which this time
	// carries the marker.
	srv.listResourcesAfterFirst[cfnType] = []ccResource{
		{identifier: "fs-floci", properties: tagsProps(ccEstate, "aws_efs_file_system.x")},
	}
	srv.getResource["AWS::EFS::FileSystem fs-floci"] = cloudcontrol.CodeUnsupportedOperation
	server := srv.start()
	defer server.Close()

	res := runCCDiscovery(t, srv, server, "aws_efs_file_system", cfnType)
	if _, ok := res.BindingFor(mustAddr(t, "aws_efs_file_system.x")); !ok {
		t.Fatalf("did not bind via the UnsupportedOperation -> list-and-match fallback:\n%s", res)
	}

	wantCalls := []string{
		"ListResources:AWS::EFS::FileSystem",        // the enumeration scan
		"GetResource:AWS::EFS::FileSystem:fs-floci", // the refinement, refused
		"ListResources:AWS::EFS::FileSystem",        // the fallback's own re-list
	}
	if len(srv.calls) != len(wantCalls) {
		t.Fatalf("calls = %v, want %v", srv.calls, wantCalls)
	}
	for i, want := range wantCalls {
		if srv.calls[i] != want {
			t.Errorf("calls[%d] = %q, want %q", i, srv.calls[i], want)
		}
	}
}

// ---------------------------------------------------------------------------
// The composite-identifier rule: never emit a raw "|" string.
// ---------------------------------------------------------------------------

func TestCloudControlComposesCompositeIdentifier(t *testing.T) {
	// aws_route_table_association is a real identity.DefaultTable entry
	// whose Components join two parts with "/": idlessAttr(subnet_id,
	// gateway_id), sep("/"), idlessAttr(route_table_id).
	srv := newCCServer(t)
	srv.listResources["AWS::EC2::SubnetRouteTableAssociation"] = []ccResource{
		{
			identifier: "subnet-abc|rtb-def",
			properties: tagsProps(ccEstate, "aws_route_table_association.x"),
		},
	}
	server := srv.start()
	defer server.Close()

	res := runCCDiscovery(t, srv, server, "aws_route_table_association", "AWS::EC2::SubnetRouteTableAssociation")
	b, ok := res.BindingFor(mustAddr(t, "aws_route_table_association.x"))
	if !ok {
		t.Fatalf("did not bind the composite identity:\n%s", res)
	}
	if b.ImportID != "subnet-abc/rtb-def" {
		t.Errorf("ImportID = %q, want the composed subnet-abc/rtb-def, not the raw pipe-joined form", b.ImportID)
	}
}

func TestCloudControlRefusesUncomposableIdentifier(t *testing.T) {
	tests := []struct {
		name       string
		typeName   string
		cfnType    string
		identifier string
	}{
		{"no identity table entry at all", "aws_no_table_entry", "AWS::Example::NoTableEntry", "part1|part2"},
		{"table entry, wrong part count", "aws_route_table_association", "AWS::EC2::SubnetRouteTableAssociation", "one|two|three"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newCCServer(t)
			srv.listResources[tt.cfnType] = []ccResource{
				{identifier: tt.identifier, properties: tagsProps(ccEstate, tt.typeName+".x")},
			}
			server := srv.start()
			defer server.Close()

			res := runCCDiscovery(t, srv, server, tt.typeName, tt.cfnType)
			if _, ok := res.BindingFor(mustAddr(t, tt.typeName+".x")); ok {
				t.Fatalf("an uncomposable identifier still produced a binding:\n%s", res)
			}
			problems := res.ProblemsOfKind(ProblemUncomposableIdentifier)
			if len(problems) != 1 {
				t.Fatalf("want one uncomposable-identifier problem, got %d:\n%s", len(problems), res)
			}
			for _, b := range res.Bindings {
				if b.ImportID == tt.identifier {
					t.Errorf("the raw pipe-joined identifier %q was used as an import ID", tt.identifier)
				}
			}
		})
	}
}

// composeCloudControlIdentifier and resolveCloudControlImportID directly, as
// pure-function table tests: the composite rule's core logic, independent
// of the rest of the scan.
func TestComposeCloudControlIdentifier(t *testing.T) {
	assoc, ok := identity.LookupType("aws_route_table_association")
	if !ok {
		t.Fatal("aws_route_table_association must be in identity.DefaultTable for this test")
	}
	tests := []struct {
		name   string
		ti     identity.TypeIdentity
		parts  []string
		want   string
		wantOK bool
	}{
		{"exact part count", assoc, []string{"subnet-1", "rtb-1"}, "subnet-1/rtb-1", true},
		{"too few parts", assoc, []string{"subnet-1"}, "", false},
		{"too many parts", assoc, []string{"subnet-1", "rtb-1", "extra"}, "", false},
		{"no components at all", identity.TypeIdentity{}, []string{"a"}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := composeCloudControlIdentifier(tt.ti, tt.parts)
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("composeCloudControlIdentifier(...) = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// TestImportsWholeARNString pins issue #298's general shape - not just
// aws_ecs_task_definition, but every ServerAssigned type whose
// ImportSyntax is a single, unseparated placeholder ending "ARN" (the
// shape tools/row-gen's tryOpaqueOverride writes only when the provider's
// own "## Import" section documents that exact ARN example) - against a
// sample of [identity.DefaultTable] rows confirmed by reading each type's
// cached doc directly under ~/Library/Caches/choudoufu/importdocs-gen.
func TestImportsWholeARNString(t *testing.T) {
	tests := []struct {
		typeName string
		want     bool
	}{
		// Each of these documents a `terraform import`/`id =` example that
		// is itself the type's own ARN, even though (for several) the
		// type's identity SCHEMA names something else for the newer
		// identity-object import path - which is exactly why signal 1
		// (IdentityAttrs[0]=="arn") alone would miss them.
		{"aws_ecs_task_definition", true},                     // family+revision identity schema, bare-ARN legacy import
		{"aws_ram_resource_share", true},                      // ImportSyntax "ARN"
		{"aws_datasync_location_efs", true},                   // ImportSyntax "ARN", IdentityAttrs [id, arn]
		{"aws_detective_graph", true},                         // graph_arn identity schema, ImportSyntax GRAPHARN
		{"aws_billing_view", true},                            // ImportSyntax "ARN"
		{"aws_evidently_project", true},                       // ImportSyntax "ARN"
		{"aws_networkfirewall_vpc_endpoint_association", true}, // ImportSyntax VPCENDPOINTASSOCIATIONARN
		// aws_ivs_channel imports by ARN via signal 1
		// (IdentityAttrs[0] == "arn") rather than ImportSyntax at all.
		{"aws_ivs_channel", true},
		// Regression guard, #124: an ARN-shaped Cloud Control identifier
		// whose provider import wants the bare resource id, not the ARN -
		// ImportSyntax is WORKSPACEID, not ARN-shaped.
		{"aws_prometheus_workspace", false},
		// Regression guard, #295: a compound, "/"-separated multi-segment
		// ImportSyntax (ID/NAME/SCOPE) must not be mistaken for a single
		// ARN token, even though IdentityAttrs does carry "arn" - just not
		// first.
		{"aws_wafv2_web_acl", false},
		// A compound ":"-separated ImportSyntax naming no ARN at all.
		{"aws_guardduty_ipset", false},
		// An ordinary composite type with no ARN involved at all.
		{"aws_route_table_association", false},
	}
	for _, tt := range tests {
		t.Run(tt.typeName, func(t *testing.T) {
			ti, ok := identity.LookupType(tt.typeName)
			if !ok {
				t.Fatalf("identity.LookupType(%q) not found - has the admission table moved?", tt.typeName)
			}
			if got := importsWholeARNString(ti); got != tt.want {
				t.Errorf("importsWholeARNString(%q) = %v, want %v (ImportSyntax=%q, IdentityAttrs=%v)",
					tt.typeName, got, tt.want, ti.ImportSyntax, ti.IdentityAttrs)
			}
		})
	}
}

func TestResolveCloudControlImportID(t *testing.T) {
	tests := []struct {
		name       string
		typeName   string
		identifier string
		want       string
		wantOK     bool
	}{
		{"single-part identity, no table entry needed", "aws_efs_file_system", "fs-0123", "fs-0123", true},
		{"single empty part refuses", "aws_efs_file_system", "", "", false},
		{"composite with a table entry", "aws_route_table_association", "subnet-1|rtb-1", "subnet-1/rtb-1", true},
		{"composite with no table entry", "aws_totally_unknown_type", "a|b", "", false},
		// The arn-vs-id split (#124's aps cohort): a registry that pins
		// primaryIdentifier to the read-only Arn hands an ARN to a type the
		// provider imports by server-assigned id - the resource segment is
		// the import ID, exactly as joinTaggedResource already rules for a
		// Tagging API ResourceARN.
		{"ARN identifier, id-identity type extracts the resource id", "aws_prometheus_workspace",
			"arn:aws:aps:us-east-1:000000000000:workspace/ws-C6DCB907", "ws-C6DCB907", true},
		// aws_ivs_channel genuinely imports by ARN (IdentityAttrs[0] ==
		// "arn"), so its identifier stays whole.
		{"ARN identifier, arn-identity type keeps the ARN", "aws_ivs_channel",
			"arn:aws:ivs:us-east-1:000000000000:channel/abcDEF123", "arn:aws:ivs:us-east-1:000000000000:channel/abcDEF123", true},
		// Issue #298: aws_ecs_task_definition's identity SCHEMA (consulted
		// by importIdentity on the native ListResource path) is
		// family+revision, not arn - so the IdentityAttrs[0]=="arn" check
		// alone never catches it - but its ImportSyntax (TASKDEFINITIONARN)
		// says the provider's legacy ID-string import IS the whole ARN,
		// confirmed against ecs_task_definition.html.markdown's "## Import"
		// section. Before the fix this returned the ARN's resource-id
		// segment, "sitemaps-generator:1", which the provider's
		// ImportResourceState rejects outright ("Expected ID in format of
		// arn:PARTITION:ecs:REGION:ACCOUNTID:task-definition/FAMILY:REVISION").
		{"ARN identifier, family+revision identity type with ARN-shaped ImportSyntax keeps the ARN", "aws_ecs_task_definition",
			"arn:aws:ecs:eu-west-1:000000000000:task-definition/sitemaps-generator:1",
			"arn:aws:ecs:eu-west-1:000000000000:task-definition/sitemaps-generator:1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := resolveCloudControlImportID(tt.typeName, tt.identifier)
			if ok != tt.wantOK || got != tt.want {
				t.Errorf("resolveCloudControlImportID(%q, %q) = (%q, %v), want (%q, %v)", tt.typeName, tt.identifier, got, ok, tt.want, tt.wantOK)
			}
			if ok && got == tt.identifier && len(tt.identifier) > 0 && containsPipe(tt.identifier) {
				t.Errorf("a pipe-joined identifier was returned unchanged")
			}
		})
	}
}

// TestImportIDFromARN is issue #295 at the layer that actually matters, and
// pins the SECOND fix a live floci crossing forced: a first pass here took
// the ARN's trailing segment alone (WAFv2's bare "id" attribute value,
// confirmed correct against `aws wafv2 list-web-acls`) and that still
// failed against a real emulator - "Unexpected format of ID (\"<uuid>\"),
// expected ID/NAME/SCOPE", straight from the AWS provider's own
// ImportResourceState, because that resource's Importer does not accept
// its bare id at all. [arnCompositeImportID] rebuilds the full string each
// provider's own "## Import" doc section names, and this test pins that
// string exactly - not just that some non-empty value came out.
func TestImportIDFromARN(t *testing.T) {
	tests := []struct {
		name     string
		typeName string
		arn      string
		wantID   string
		wantAttr string
		wantOK   bool
	}{
		{
			// Confirmed against a live floci crossing (issue #295): applied
			// as `arn:aws:wafv2:us-east-1:000000000000:global/webacl/cdn_poc_govuk/39984a03-9d79-46db-bd03-1fefbc57f2e3`,
			// `aws wafv2 list-web-acls --scope CLOUDFRONT` answers
			// Id=39984a03-9d79-46db-bd03-1fefbc57f2e3, and importing with
			// exactly this composed string is what let live-plan bind and
			// read the object back without error.
			name:     "wafv2 web acl, global scope: id/name/CLOUDFRONT, not the bare id and not the ARN's own \"global\" spelling",
			typeName: "aws_wafv2_web_acl",
			arn:      "arn:aws:wafv2:us-east-1:000000000000:global/webacl/cdn_poc_govuk/9a032e21-0000-4c6d-8c8c-000000000000",
			wantID:   "9a032e21-0000-4c6d-8c8c-000000000000/cdn_poc_govuk/CLOUDFRONT",
			wantAttr: "id",
			wantOK:   true,
		},
		{
			name:     "wafv2 ip set, regional scope: id/name/REGIONAL",
			typeName: "aws_wafv2_ip_set",
			arn:      "arn:aws:wafv2:us-east-1:000000000000:regional/ipset/my-set/9a032e21-0000-4c6d-8c8c-000000000000",
			wantID:   "9a032e21-0000-4c6d-8c8c-000000000000/my-set/REGIONAL",
			wantAttr: "id",
			wantOK:   true,
		},
		{
			name:     "guardduty ip set: detectorID:id, the literal \"ipset\" segment dropped",
			typeName: "aws_guardduty_ipset",
			arn:      "arn:aws:guardduty:us-east-1:000000000000:detector/12abc34d567e8fa901bc2d34eexample/ipset/98abc34d567e8fa901bc2d34eexample",
			wantID:   "12abc34d567e8fa901bc2d34eexample:98abc34d567e8fa901bc2d34eexample",
			wantAttr: "id",
			wantOK:   true,
		},
		{
			name:     "guardduty threat intel set: same detectorID:id shape, different literal segment",
			typeName: "aws_guardduty_threatintelset",
			arn:      "arn:aws:guardduty:us-east-1:000000000000:detector/12abc34d567e8fa901bc2d34eexample/threatintelset/98abc34d567e8fa901bc2d34eexample",
			wantID:   "12abc34d567e8fa901bc2d34eexample:98abc34d567e8fa901bc2d34eexample",
			wantAttr: "id",
			wantOK:   true,
		},
		{
			// No row in arnCompositeImportID for transfer/agreement on
			// purpose: ParseARN's unmodified ResourceID already IS the
			// documented "server_id/agreement_id" import string. An early
			// draft of this fix added a row that cut it down to the bare
			// agreement id alone, which would have broken an import that
			// worked before #295 touched this function at all.
			name:     "transfer agreement: unmodified ResourceID already is server_id/agreement_id",
			typeName: "aws_transfer_agreement",
			arn:      "arn:aws:transfer:us-east-1:000000000000:agreement/s-0123456789abcdef0/a-0123456789abcdef0",
			wantID:   "s-0123456789abcdef0/a-0123456789abcdef0",
			wantAttr: "id",
			wantOK:   true,
		},
		{
			name:     "cloudwatch logs log group: id starts with /, must not be touched by any composite rule",
			typeName: "aws_cloudwatch_log_group",
			arn:      "arn:aws:logs:us-east-1:123456789012:log-group:/estate/app",
			wantID:   "/estate/app",
			wantAttr: "id",
			wantOK:   true,
		},
		{
			name:     "elasticloadbalancing v2 load balancer: arn-primary short-circuits before the shape table is even consulted",
			typeName: "aws_lb",
			arn:      "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/main/50dc6c495c0c9188",
			wantID:   "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/main/50dc6c495c0c9188",
			wantAttr: "arn",
			wantOK:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ti, ok := identity.LookupType(tt.typeName)
			if !ok {
				t.Fatalf("%s must be in identity.DefaultTable for this test", tt.typeName)
			}
			gotID, gotAttr, gotOK := importIDFromARN(ti, tt.arn)
			if gotOK != tt.wantOK || gotID != tt.wantID || gotAttr != tt.wantAttr {
				t.Errorf("importIDFromARN(%s, %q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.typeName, tt.arn, gotID, gotAttr, gotOK, tt.wantID, tt.wantAttr, tt.wantOK)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// List failure and the sweep's untaggable gap, for parity with the native
// path's equivalents.
// ---------------------------------------------------------------------------

func TestCloudControlListResourcesFailure(t *testing.T) {
	srv := newCCServer(t)
	srv.listErr["AWS::EFS::FileSystem"] = cloudcontrol.CodeThrottlingError
	server := srv.start()
	defer server.Close()

	res := runCCDiscovery(t, srv, server, "aws_efs_file_system", "AWS::EFS::FileSystem")
	problems := res.ProblemsOfKind(ProblemListFailed)
	if len(problems) != 1 || problems[0].TypeName != "aws_efs_file_system" {
		t.Errorf("want one list-failed problem for aws_efs_file_system, got %v", res.Problems)
	}
	if containsAddr(res.Unbound, "aws_efs_file_system.x") {
		t.Error("a list failure was reported as merely absent")
	}
}

// TestCloudControlContinuationGapIsMalformed is the Cloud Control path's
// sibling of markers_test.go's TestContinuationGapIsMalformed: this path
// duplicates scanType's gap check (see cloudcontrol.go's own GatherAddress
// call) rather than sharing it, so a gapped continuation chain arriving
// through Cloud Control has to be pinned here too - a fix to one copy is
// not a fix to both.
func TestCloudControlContinuationGapIsMalformed(t *testing.T) {
	srv := newCCServer(t)
	srv.listResources["AWS::EFS::FileSystem"] = []ccResource{
		{identifier: "fs-gap", properties: map[string]any{
			"Tags": []map[string]string{
				{"Key": TagEstate, "Value": ccEstate},
				{"Key": TagAddress, "Value": strings.Repeat("a", 256)},
				{"Key": ContinuationTag(3), "Value": "b"}, // tofu-address-2 is missing.
			},
		}},
	}
	server := srv.start()
	defer server.Close()

	res := runCCDiscovery(t, srv, server, "aws_efs_file_system", "AWS::EFS::FileSystem")
	problems := res.ProblemsOfKind(ProblemMalformedMarker)
	if len(problems) != 1 {
		t.Fatalf("want exactly one malformed-marker problem for the gapped chain, got %d:\n%s", len(problems), res)
	}
	if !strings.Contains(problems[0].Detail, "continuation") {
		t.Errorf("the malformed-marker detail does not mention the continuation gap: %q", problems[0].Detail)
	}
	if len(res.Bindings) != 0 {
		t.Errorf("a gapped continuation chain bound to something:\n%s", res)
	}
}

func TestCloudControlSweepGapNotTaggable(t *testing.T) {
	srv := newCCServer(t)
	server := srv.start()
	defer server.Close()

	cc := cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL})
	roster := ccRoster(t,
		map[string]string{"aws_efs_file_system": "AWS::EFS::FileSystem"},
		map[string]bool{"AWS::EFS::FileSystem": true},
		map[string]bool{"AWS::EFS::FileSystem": false}, // untaggable
	)

	req := Request{
		Estate:       ccEstate,
		Config:       ccConfig("aws_efs_file_system"),
		Resolutions:  nil,
		Provider:     newFakeCloud(),
		CloudControl: cc,
		Roster:       roster,
		Sweep:        true,
		SweepTypes:   []string{"aws_efs_file_system"},
	}
	res, diags := Discover(context.Background(), req)
	if diags.HasErrors() {
		t.Fatalf("a not-taggable sweep gap must not be an error: %s", diags.Err())
	}
	var found bool
	for _, g := range res.SweepGaps {
		if g.TypeName == "aws_efs_file_system" && g.Reason == SweepGapNotTaggable {
			found = true
		}
	}
	if !found {
		t.Errorf("want a SweepGapNotTaggable for aws_efs_file_system, got %v", res.SweepGaps)
	}
	if len(srv.calls) != 0 {
		t.Errorf("an untaggable swept type should never reach ListResources, got calls %v", srv.calls)
	}
}

// ---------------------------------------------------------------------------
// Shared plumbing
// ---------------------------------------------------------------------------

func ccResolutionFor(t *testing.T, typeName string) identity.Resolution {
	t.Helper()
	return identity.Resolution{
		Addr:  mustAddr(t, typeName+".x"),
		Class: identity.ClassNeedsDiscovery,
	}
}

// runCCDiscovery wires one type through the Cloud Control fallback with a
// mapped+listable Roster and asserts the run produced no diagnostics
// errors, returning the result for the caller to inspect further.
func runCCDiscovery(t *testing.T, srv *ccServer, server *httptest.Server, typeName, cfnType string) *Result {
	t.Helper()

	// MaxAttempts: 1 - TestCloudControlListResourcesFailure drives this
	// through a server that answers ThrottlingException on every call; this
	// helper is about discovery's handling of a list failure, not the
	// client's retry policy (internal/live/cloudcontrol/retry_test.go), so
	// it must not sit through real backoff sleeps.
	cc := cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL, MaxAttempts: 1})
	roster := ccRoster(t,
		map[string]string{typeName: cfnType},
		map[string]bool{cfnType: true},
		nil,
	)
	req := Request{
		Estate:       ccEstate,
		Config:       ccConfig(typeName),
		Resolutions:  []identity.Resolution{ccResolutionFor(t, typeName)},
		Provider:     newFakeCloud(),
		CloudControl: cc,
		Roster:       roster,
	}
	res, diags := Discover(context.Background(), req)
	if diags.HasErrors() {
		t.Logf("diagnostics (not necessarily an error for this test): %s", diags.Err())
	}
	return res
}

func mergeProps(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func containsPipe(s string) bool {
	for _, r := range s {
		if r == '|' {
			return true
		}
	}
	return false
}
