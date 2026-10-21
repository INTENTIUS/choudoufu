// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

// TestGoogleRowsRenderFromConfiguration resolves one resource of each
// shape [GoogleTable] rows, with no provider schemas, and asserts the
// rendered import ID: the provider block's project, region and zone stand
// in wherever a resource names none of its own, which is the defaulting
// hashicorp/google's ReplaceVars applies to the same id format.
func TestGoogleRowsRenderFromConfiguration(t *testing.T) {
	cfg := loadConfig(t, "testdata/gcp-identity-rows", nil)
	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	want := map[string]string{
		"google_compute_network.vpc":              "projects/estate-prod/global/networks/estate-vpc",
		"google_compute_subnetwork.app":           "projects/estate-prod/regions/europe-west1/subnetworks/app",
		"google_compute_disk.data":                "projects/estate-data/zones/europe-west1-c/disks/data",
		"google_pubsub_topic.events":              "projects/estate-prod/topics/events",
		"google_service_account.runner":           "projects/estate-prod/serviceAccounts/ci-runner@estate-prod.iam.gserviceaccount.com",
		"google_project_iam_member.runner_logs":   "estate-prod roles/logging.logWriter serviceAccount:ci-runner@estate-prod.iam.gserviceaccount.com",
		"google_storage_bucket_iam_member.reader": "b/estate-artifacts roles/storage.objectViewer group:readers@example.com until 2027",
	}
	for addr, id := range want {
		got := resolutionAt(t, result, addr)
		if got.Class != ClassConcrete && got.Class != ClassParentDerived {
			t.Errorf("%s = %s (%s), want a resolved identity", addr, got.Class, got.Reason)
			continue
		}
		if got.ImportID != id {
			t.Errorf("%s import ID = %q, want %q", addr, got.ImportID, id)
		}
	}

	minted := resolutionAt(t, result, "google_recaptcha_enterprise_key.site")
	if minted.Class != ClassNeedsDiscovery {
		t.Errorf("a minted identity resolved to %s, want NEEDS_DISCOVERY", minted.Class)
	}

	orphan := resolutionAt(t, result, "google_pubsub_topic.orphan")
	if orphan.Class != ClassNeedsDiscovery {
		t.Fatalf("a topic with no project anywhere = %s, want NEEDS_DISCOVERY", orphan.Class)
	}
	if !strings.Contains(orphan.Reason, "Google Cloud project") {
		t.Errorf("the reason does not name the missing project: %q", orphan.Reason)
	}
}

// TestGoogleOptionalPlaceholderRow resolves a row whose id format names an
// optional argument with no default: an instance that writes it is named
// from configuration, and one that leaves it out is refused by name, the
// type keeping its row.
func TestGoogleOptionalPlaceholderRow(t *testing.T) {
	cfg := loadConfig(t, "testdata/gcp-optional-placeholder", nil)
	result, diags := Resolve(context.Background(), cfg)

	got := resolutionAt(t, result, "google_bigquery_connection.lake")
	if want := "projects/estate-prod/locations/EU/connections/lake"; got.Class != ClassConcrete || got.ImportID != want {
		t.Errorf("written location: %s %q, want CONCRETE %q", got.Class, got.ImportID, want)
	}

	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want the one refusal: %v", len(diags), diags.Err())
	}
	if d := diags[0].Description(); !strings.Contains(d.Detail, "google_bigquery_connection.nowhere") || !strings.Contains(d.Detail, `"location"`) {
		t.Errorf("the refusal does not name the instance and its missing location: %s", d.Detail)
	}
}

// TestGoogleLocationRuleRows resolves the rows tools/gcp-row-gen derives
// from how a generated Create sets an optional, computed {{location}}
// (#2145 item 7): an encoder's tpgresource.GetRegion default stands in
// for an unwritten location, a Create that sets nothing refuses the
// instance that leaves it out, and a placeholder the schema lacks renders
// as the empty segment ReplaceVars writes.
func TestGoogleLocationRuleRows(t *testing.T) {
	cfg := loadConfig(t, "testdata/gcp-location-rule", nil)
	result, diags := Resolve(context.Background(), cfg)

	concrete := map[string]string{
		"google_artifact_registry_repository.defaulted": "projects/estate-prod/locations/europe-west1/repositories/images",
		"google_artifact_registry_repository.multi":     "projects/estate-prod/locations/us/repositories/mirror",
		"google_artifact_registry_rule.keep":            "projects/estate-prod/locations/europe-west1/repositories/images/rules/keep",
		"google_compute_snapshot_settings.settings":     "projects/estate-prod/global/snapshotSettings/",
	}
	for addr, id := range concrete {
		got := resolutionAt(t, result, addr)
		if got.Class != ClassConcrete || got.ImportID != id {
			t.Errorf("%s = %s %q (%s), want CONCRETE %q", addr, got.Class, got.ImportID, got.Reason, id)
		}
	}

	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want the one refusal: %v", len(diags), diags.Err())
	}
	if d := diags[0].Description(); !strings.Contains(d.Detail, "google_artifact_registry_rule.nowhere") || !strings.Contains(d.Detail, `"location"`) {
		t.Errorf("the refusal does not name the rule and its missing location: %s", d.Detail)
	}
}

// TestGoogleCreateNamedRow resolves the rows of types whose id embeds a
// name the API returns, where the create sends that name's last segment as
// the request's id field (#2145 item 3): an instance that sets the
// argument is named from configuration, in each shape the generator
// derives, and one that leaves it out needs the object to exist before it
// has a name. A type whose API writes the project back as a number keeps
// its markerless veto.
func TestGoogleCreateNamedRow(t *testing.T) {
	cfg := loadConfig(t, "testdata/gcp-create-named", nil)
	result, _ := Resolve(context.Background(), cfg)

	concrete := map[string]string{
		"google_data_loss_prevention_inspect_template.pii":        "projects/estate-prod/locations/global/inspectTemplates/pii",
		"google_ces_agent.triage":                                 "projects/estate-prod/locations/us/apps/support/agents/triage",
		"google_dialogflow_generator.summary":                     "projects/estate-prod/locations/global/generators/summary",
		"google_scc_v2_organization_notification_config.findings": "organizations/123456789/locations/global/notificationConfigs/findings",
	}
	for addr, id := range concrete {
		got := resolutionAt(t, result, addr)
		if got.Class != ClassConcrete || got.ImportID != id {
			t.Errorf("%s = %s %q (%s), want CONCRETE %q", addr, got.Class, got.ImportID, got.Reason, id)
		}
	}
	// The identity's name is what the provider keeps: the last segment
	// where its flatten keeps that, the whole name where it keeps it all.
	if v := resolutionAt(t, result, "google_data_loss_prevention_inspect_template.pii").IdentityValues; v["name"] != "pii" || v["parent"] != "projects/estate-prod/locations/global" {
		t.Errorf("inspect template identity values = %v, want name=pii and the parent", v)
	}
	if v := resolutionAt(t, result, "google_dialogflow_generator.summary").IdentityValues; v["name"] != concrete["google_dialogflow_generator.summary"] {
		t.Errorf("generator identity values = %v, want the whole name", v)
	}

	unnamed := resolutionAt(t, result, "google_data_loss_prevention_inspect_template.unnamed")
	if unnamed.Class != ClassNeedsDiscovery || unnamed.Cause != DiscoveryNameOmitted || len(unnamed.CauseArgs) != 1 || unnamed.CauseArgs[0] != "template_id" {
		t.Errorf("an inspect template with no template_id = %s %s %v, want NEEDS_DISCOVERY naming template_id", unnamed.Class, unnamed.Cause, unnamed.CauseArgs)
	}
	if !strings.Contains(unnamed.Reason, "google_data_loss_prevention_inspect_template.unnamed") {
		t.Errorf("the reason does not name the instance: %q", unnamed.Reason)
	}

	if !IsMarkerless("google_scc_v2_project_notification_config") {
		t.Error("google_scc_v2_project_notification_config left the markerless veto, but Google documents its name with a project number")
	}
}

// TestGoogleLongFormComposed resolves the arguments hashicorp/google 8.6.0
// rewrites before they become part of the id (#2144), and asserts the id
// the provider itself builds: an IAM parent the updater parses with
// GetImportIdQualifiers and rebuilds, an argument of a type whose create
// keeps only the last path segment, and pubsub's computed subscription
// name. A value only an apply can know, a long form that conflicts with
// the project or location the resource sets, and one the rewrite leaves
// empty refuse the instance by name.
func TestGoogleLongFormComposed(t *testing.T) {
	cfg := loadConfig(t, "testdata/gcp-long-form", nil)
	result, diags := Resolve(context.Background(), cfg)

	const secret = " roles/secretmanager.secretAccessor group:"
	const sub = " roles/pubsub.subscriber group:"
	const run = " roles/run.invoker group:a@example.com"
	concrete := map[string]string{
		// The secret's .id parses back to the canonical parent.
		"google_secret_manager_secret_iam_member.byid": "projects/estate-prod/secrets/db-pass" + secret + "a@example.com",
		// The short form, unchanged.
		"google_secret_manager_secret_iam_member.byname": "projects/estate-prod/secrets/db-pass" + secret + "b@example.com",
		// The long form's own project wins over the provider's.
		"google_secret_manager_secret_iam_member.literal_long": "projects/other/secrets/db-pass" + secret + "a@example.com",
		// A long form that agrees with the location the resource sets.
		"google_cloud_run_v2_service_iam_member.location_agrees": "projects/estate-prod/locations/europe-west1/services/api" + run,
		// {location}/{name}, with the provider's project.
		"google_cloud_run_v2_service_iam_member.location_short": "projects/estate-prod/locations/us-east1/services/api" + run,
		// ReplaceVarsForId keeps the policy id's last segment.
		"google_compute_network_firewall_policy_rule.r": "projects/estate-prod/global/firewallPolicies/pol/rules/1000",
		// It trims "projects/" off the project and cuts the region's self
		// link to its last segment too.
		"google_compute_region_network_firewall_policy_rule.rr": "projects/estate-prod/regions/europe-west1/firewallPolicies/rpol/1000",
		// GetComputedSubscriptionName: a short name is placed into the
		// template with the resource's project, a full one kept whole.
		"google_pubsub_subscription_iam_member.short":         "projects/estate-prod/subscriptions/orders" + sub + "a@example.com",
		"google_pubsub_subscription_iam_member.other_project": "projects/other/subscriptions/orders" + sub + "a@example.com",
		"google_pubsub_subscription_iam_member.byid":          "projects/estate-prod/subscriptions/orders" + sub + "b@example.com",
		// A "/" in an argument the provider substitutes as written.
		"google_securityposture_posture.org": "organizations/123/locations/global/postures/baseline",
	}
	for addr, id := range concrete {
		got := resolutionAt(t, result, addr)
		if got.Class != ClassConcrete || got.ImportID != id {
			t.Errorf("%s = %s %q, want CONCRETE %q", addr, got.Class, got.ImportID, id)
		}
	}

	// The identity values say what the import ID says: the long form's
	// project, not the provider's, and the parent's short name.
	lit := resolutionAt(t, result, "google_secret_manager_secret_iam_member.literal_long")
	if lit.IdentityValues["project"] != "other" || lit.IdentityValues["secret_id"] != "db-pass" {
		t.Errorf("literal_long identity values = %v, want project=other secret_id=db-pass", lit.IdentityValues)
	}
	// A parent's .id and its short name are one parent, so a duplicate
	// between them is still caught: the values differ by member alone.
	for _, pair := range [][2]string{
		{"google_secret_manager_secret_iam_member.byid", "google_secret_manager_secret_iam_member.byname"},
		{"google_pubsub_subscription_iam_member.byid", "google_pubsub_subscription_iam_member.short"},
	} {
		a, b := resolutionAt(t, result, pair[0]).IdentityValues, resolutionAt(t, result, pair[1]).IdentityValues
		if len(a) != len(b) {
			t.Errorf("%s and %s identity values = %v and %v, want the same attributes", pair[0], pair[1], a, b)
		}
		for k, v := range a {
			if k != "member" && b[k] != v {
				t.Errorf("%s and %s differ in %s: %q and %q", pair[0], pair[1], k, v, b[k])
			}
		}
	}

	refused := map[string]string{
		"google_secret_manager_secret_iam_member.minted":           "Rewritten identity argument not known before apply|secret_id from google_recaptcha_enterprise_key.site.id",
		"google_compute_network_firewall_policy_rule.trailing":     "Rewritten identity argument not composable|firewall_policy to \"pol/\"",
		"google_secret_manager_secret_iam_member.project_conflict": "Rewritten identity argument not composable|secret_id to \"projects/other/secrets/db-pass\"",
		"google_cloud_run_v2_service_iam_member.location_conflict": "Rewritten identity argument not composable|name to \"projects/estate-prod/locations/europe-west1/services/api\"",
	}
	conflict := map[string]string{
		"google_secret_manager_secret_iam_member.project_conflict": `names project "other" where the resource sets project to "estate-dr"`,
		"google_cloud_run_v2_service_iam_member.location_conflict": `names location "europe-west1" where the resource sets location to "us-central1"`,
	}
	for addr, want := range refused {
		summary, arg, _ := strings.Cut(want, "|")
		for _, res := range result.All() {
			if res.Addr.String() == addr {
				t.Errorf("%s resolved to %s %q, want it refused", addr, res.Class, res.ImportID)
			}
		}
		found := false
		for _, d := range diags {
			desc := d.Description()
			if desc.Summary == summary && strings.Contains(desc.Detail, addr+" sets "+arg) && strings.Contains(desc.Detail, "#2144") &&
				strings.Contains(desc.Detail, conflict[addr]) {
				found = true
			}
		}
		if !found {
			t.Errorf("no %q refusal names %s and %s: %v", summary, addr, arg, diags.Err())
		}
	}
	if len(diags) != len(refused) {
		t.Errorf("got %d diagnostics, want the %d refusals: %v", len(diags), len(refused), diags.Err())
	}
}

// TestGoogleQualifierGroupsNameComponents asserts that every named group
// in a [Component.Qualifiers] regex names a component at or before the one
// it parses, by argument or by cloud property. A group that names none
// would be a value the provider rebuilds its parent from and this package
// silently drops.
func TestGoogleQualifierGroupsNameComponents(t *testing.T) {
	for typ, entry := range GoogleTable {
		for q, comp := range entry.Components {
			if comp.LongForm != LongFormIAMParent && comp.LongForm != LongFormComputedName {
				continue
			}
			if len(comp.Qualifiers) == 0 {
				t.Errorf("%s: component %d is %s with no qualifiers", typ, q, comp.LongForm)
			}
			names := map[string]bool{}
			for _, c := range entry.Components[:q+1] {
				for _, a := range c.Attrs {
					names[a] = true
				}
				if c.Cloud != CloudNone {
					names[string(c.Cloud)] = true
				}
			}
			for _, pattern := range comp.Qualifiers {
				re, err := regexp.Compile(pattern)
				if err != nil {
					t.Errorf("%s: qualifier %q does not compile: %v", typ, pattern, err)
					continue
				}
				for _, name := range re.SubexpNames()[1:] {
					if name != "" && !names[name] {
						t.Errorf("%s: qualifier %q captures %q, which no component of the parent reads", typ, pattern, name)
					}
				}
			}
		}
	}
}

// TestGoogleIAMParseShapes resolves one IAM type of each parent shape
// tools/gcp-row-gen reads since #2145, and asserts the parent the
// updater's GetResourceId builds: camelCase qualifier groups respelled as
// arguments (a short name and a long form), and the three hand-written
// IdParseFunc shapes.
func TestGoogleIAMParseShapes(t *testing.T) {
	cfg := loadConfig(t, "testdata/gcp-iam-parse", nil)
	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	const g = " group:a@example.com"
	want := map[string]string{
		"google_dataproc_metastore_database_iam_member.short": "projects/estate-prod/locations/europe-west1/services/meta/databases/sales roles/metastore.user" + g,
		"google_dataproc_metastore_database_iam_member.long":  "projects/other/locations/us-east1/services/meta/databases/sales roles/metastore.user" + g,
		"google_iap_app_engine_service_iam_member.svc":        "projects/estate-prod/iap_web/appengine-estate-prod/services/default roles/iap.httpsResourceAccessor" + g,
		"google_spanner_database_iam_member.db":               "estate-prod/core/ledger roles/spanner.databaseReader" + g,
		"google_spanner_instance_iam_member.inst":             "estate-data/core roles/spanner.viewer" + g,
		"google_bigquery_dataset_iam_member.ds":               "projects/estate-prod/datasets/analytics roles/bigquery.dataViewer" + g,
		"google_dataproc_cluster_iam_member.cl":               "projects/estate-prod/regions/europe-west1/clusters/batch roles/dataproc.viewer" + g,
		"google_folder_iam_member.short":                      "folders/123 roles/viewer" + g,
		"google_folder_iam_member.long":                       "folders/456 roles/viewer" + g,
		"google_billing_account_iam_member.ba":                "012345-6789AB-CDEF01 roles/billing.viewer" + g,
	}
	for addr, id := range want {
		got := resolutionAt(t, result, addr)
		if got.Class != ClassConcrete || got.ImportID != id {
			t.Errorf("%s = %s %q, want CONCRETE %q", addr, got.Class, got.ImportID, id)
		}
	}
	long := resolutionAt(t, result, "google_dataproc_metastore_database_iam_member.long")
	if long.IdentityValues["service_id"] != "meta" || long.IdentityValues["project"] != "other" || long.IdentityValues["database"] != "sales" {
		t.Errorf("the long form's identity values = %v, want service_id=meta project=other database=sales", long.IdentityValues)
	}
	// The prefix CanonicalFolderId adds is the parent id's, not the
	// argument's: stock's state and identity keep folder as written.
	for addr, folder := range map[string]string{"google_folder_iam_member.short": "123", "google_folder_iam_member.long": "folders/456"} {
		if got := resolutionAt(t, result, addr).IdentityValues["folder"]; got != folder {
			t.Errorf("%s identity folder = %q, want %q as written", addr, got, folder)
		}
	}
}

// TestGoogleHandwrittenCreateRows resolves one resource per way a
// hand-written hashicorp/google type's create builds its id in code
// (#2145 item 1), and asserts the id that create builds: a variable bound
// from GetProject, d.Get as written, fmt.Sprintf, a package helper over
// GetLocation, a struct method, a struct built by a package function, an
// updater passed in at registration, a DCL ID(), ReplaceVarsForId in a
// second file, and a prefix ParseFolderId trims.
func TestGoogleHandwrittenCreateRows(t *testing.T) {
	cfg := loadConfig(t, "testdata/gcp-handwritten", nil)
	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	want := map[string]string{
		"google_app_engine_application.app":             "estate-prod",
		"google_compute_shared_vpc_host_project.host":   "host-prj",
		"google_compute_shared_vpc_service_project.svc": "host-prj/svc-prj",
		"google_container_cluster.regional":             "projects/estate-prod/locations/us-east1/clusters/gke",
		"google_container_cluster.defaulted":            "projects/estate-prod/locations/europe-west1/clusters/gke-default",
		"google_cloudfunctions_function.fn":             "projects/estate-prod/locations/europe-west1/functions/fn",
		"google_logging_project_sink.audit":             "projects/estate-prod/sinks/audit",
		"google_logging_folder_sink.long":               "folders/123/sinks/f",
		"google_logging_folder_sink.short":              "folders/456/sinks/g",
		"google_logging_project_exclusion.noisy":        "projects/estate-prod/exclusions/noisy",
		"google_dataproc_workflow_template.wt":          "projects/estate-prod/locations/europe-west1/workflowTemplates/wt",
		"google_gke_hub_feature_membership.fm":          "projects/estate-prod/locations/global/features/configmanagement/membershipId/m1",
		"google_project_organization_policy.serial":     "estate-prod:compute.disableSerialPortAccess",
		"google_organization_policy.org":                "1234/constraints/compute.skipDefaultNetworkCreation",
	}
	for addr, id := range want {
		got := resolutionAt(t, result, addr)
		if got.Class != ClassConcrete || got.ImportID != id {
			t.Errorf("%s = %s %q (%s), want CONCRETE %q", addr, got.Class, got.ImportID, got.Reason, id)
		}
	}
}

// TestGoogleEmptySegmentRows resolves the rows whose id format names an
// optional argument with no default that every import format captures
// with [^/]* (#2145 item 6): left unset or written as null, the argument
// renders as the empty segment stock's ReplaceVars writes, so a network
// endpoint naming only an IP address or only an FQDN has stock's import
// ID. An endpoint naming neither is refused, as the provider's own
// AtLeastOneOf refuses it, and google_spanner_backup_schedule, whose
// importer captures name with [^/]+, still refuses an unset name.
func TestGoogleEmptySegmentRows(t *testing.T) {
	cfg := loadConfig(t, "testdata/gcp-empty-segment", nil)
	result, diags := Resolve(context.Background(), cfg)

	concrete := map[string]string{
		"google_compute_global_network_endpoint.by_ip":     "estate-prod/neg/10.0.0.1//80",
		"google_compute_global_network_endpoint.by_fqdn":   "estate-prod/neg//api.example.com/443",
		"google_compute_global_network_endpoint.null_fqdn": "estate-prod/neg/10.0.0.9//80",
		"google_compute_region_network_endpoint.by_ip":     "estate-prod/europe-west1/rneg/10.0.0.2//8080",
		"google_compute_region_network_endpoint.by_fqdn":   "estate-prod/europe-west1/rneg//db.example.com/5432",
		"google_compute_network_endpoint.no_instance":      "estate-prod/europe-west1-b/zneg//10.0.0.3/80",
	}
	for addr, id := range concrete {
		got := resolutionAt(t, result, addr)
		if got.Class != ClassConcrete || got.ImportID != id {
			t.Errorf("%s = %s %q (%s), want CONCRETE %q", addr, got.Class, got.ImportID, got.Reason, id)
		}
	}

	refused := map[string][]string{
		"google_compute_global_network_endpoint.neither": {`"ip_address"`, `"fqdn"`},
		"google_spanner_backup_schedule.unnamed":         {`"name"`},
	}
	if len(diags) != len(refused) {
		t.Fatalf("got %d diagnostics, want %d refusals: %v", len(diags), len(refused), diags.Err())
	}
	for addr, names := range refused {
		found := false
		for _, d := range diags {
			desc := d.Description()
			if desc.Summary != "Identity argument not set" || !strings.Contains(desc.Detail, addr+" ") {
				continue
			}
			found = true
			for _, n := range names {
				if !strings.Contains(desc.Detail, n) {
					t.Errorf("the refusal of %s does not name %s: %s", addr, n, desc.Detail)
				}
			}
		}
		if !found {
			t.Errorf("%s was not refused with Identity argument not set: %v", addr, diags.Err())
		}
	}
}
