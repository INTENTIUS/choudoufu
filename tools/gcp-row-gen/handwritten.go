// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"strings"
)

// decideHandwritten decides a resource whose file states no id format: a
// hand-written type, whose Create and importer createread.go read at code
// level (#2145 item 1). A row is built when the create's id is read whole
// from configuration and the importer reads that same id back; anything
// else is refused with what stopped it.
func decideHandwritten(d Decision, src Source, tfs []TypeFacts, unrowed unrowedFunc) Decision {
	at := strings.Join(src.CreateIDAt, ", ")
	if src.CreateID == "" {
		if src.CreateUnread == "" {
			return unrowed(true, "the provider source sets this type's id some other way than an id format template")
		}
		if src.CreateMinted {
			reason := fmt.Sprintf("the provider's create sets the id from what the API returns (%s: %s)", at, src.CreateUnread)
			if !d.Labels && src.Importer != "none" {
				d.Verdict, d.Reason = VerdictMarkerless, reason
				return d
			}
			if src.Importer == "none" {
				reason += ", and the type has no importer"
			}
			return unrowed(false, "%s", reason)
		}
		return unrowed(true, "the provider's create builds the id in code this generator does not read to the end (%s: %s)", at, src.CreateUnread)
	}
	names := placeholders(src.CreateID)
	if len(names) == 0 {
		return unrowed(false, "the provider's create sets the same id, %q, on every instance (%s)", src.CreateID, at)
	}
	if attrFlags(tfs, "project") != "" && !containsStr(names, "project") {
		return unrowed(false, "the provider's create sets the id %s (%s), which carries no {{project}} although the type takes a project argument, so two instances in different projects would share one id", src.CreateID, at)
	}
	switch src.Importer {
	case "passthrough":
	case "none":
		return unrowed(false, "the provider's create sets the id %s from configuration (%s), but the type has no importer, so a run cannot read the object back by that id", src.CreateID, at)
	case "func":
		r, ruled := importerRulings[d.Type]
		ruled = ruled && r.Hash == src.ImporterHash && r.Accepts == src.CreateID
		if ruled && r.Refuse != "" {
			return unrowed(false, "the provider's create sets the id %s (%s), and the importer at %s %s", src.CreateID, at, src.ImporterAt, r.Refuse)
		}
		if !importerReadsBack(src) && !ruled {
			return unrowed(false, "the provider's create sets the id %s (%s), and the importer at %s is not read as accepting that id and keeping it", src.CreateID, at, src.ImporterAt)
		}
	default:
		return unrowed(false, "the provider's create sets the id %s (%s), but its importer is not read (%s)", src.CreateID, at, src.ImporterUnread)
	}
	src.IDFormat, src.ImportIDFormat = src.CreateID, src.CreateID
	return decideResource(d, src, tfs, AssetNames{}, unrowed)
}

// importerReadsBack reports whether an importer of the type's own code
// parses the create's id with one of its ParseImportId regexes and leaves
// that same id: it sets none, or sets one built from the same template.
func importerReadsBack(src Source) bool {
	if src.ImporterUnread != "" || (src.ImporterID != "" && src.ImporterID != src.CreateID) {
		return false
	}
	for _, f := range src.ImporterFormats {
		if tmpl, ok := regexToTemplate(f); ok && tmpl == src.CreateID {
			return true
		}
	}
	return false
}

// importerRuling is one importer read by hand, for the custom importers no
// ParseImportId regex describes: the id it accepts and keeps, where it is,
// and the fingerprint (importerHash) of the code the ruling was made
// against. A provider release that changes that code changes the
// fingerprint, and [checkImporterRulings] fails the generator until the
// importer is read again.
//
// A ruling with Refuse set records an importer that does read the id back
// but leaves the object in a state a run must not adopt, and says why.
type importerRuling struct {
	Accepts string
	Hash    string
	At      string
	Why     string
	Refuse  string
}

// importerRulings are those rulings, at v8.6.0.
var importerRulings = map[string]importerRuling{
	"google_logging_project_sink": {
		Accepts: "projects/{{project}}/sinks/{{name}}", Hash: "2195425fdfe5",
		At:  "google/services/logging/resource_logging_sink.go:286",
		Why: `resourceLoggingSinkImportState parses the id with ParseLoggingSinkId, regex "(.+)/(.+)/sinks/(.+)" over a resource type in LoggingSinkResourceTypes (logging_utils.go:40), sets the parent argument from it and sets no id`,
	},
	"google_logging_folder_sink": {
		Accepts: "folders/{{folder}}/sinks/{{name}}", Hash: "2195425fdfe5",
		At:  "google/services/logging/resource_logging_sink.go:286",
		Why: `as google_logging_project_sink: "folders" is one of LoggingSinkResourceTypes`,
	},
	"google_logging_organization_sink": {
		Accepts: "organizations/{{org_id}}/sinks/{{name}}", Hash: "2195425fdfe5",
		At:  "google/services/logging/resource_logging_sink.go:286",
		Why: `as google_logging_project_sink: "organizations" is one of LoggingSinkResourceTypes`,
	},
	"google_logging_billing_account_sink": {
		Accepts: "billingAccounts/{{billing_account}}/sinks/{{name}}", Hash: "2195425fdfe5",
		At:  "google/services/logging/resource_logging_sink.go:286",
		Why: `as google_logging_project_sink: "billingAccounts" is one of LoggingSinkResourceTypes`,
	},
	"google_logging_project_exclusion": {
		Accepts: "projects/{{project}}/exclusions/{{name}}", Hash: "a56c734fcd51",
		At:  "google/services/logging/resource_logging_exclusion.go:176",
		Why: `resourceLoggingExclusionImportState runs ProjectLoggingExclusionIdParseFunc (logging_exclusion_project.go), which parses the id with ParseLoggingExclusionId, regex "(.+)/(.+)/exclusions/(.+)", requires the resource type "projects", sets project from it and sets no id`,
	},
	"google_logging_folder_exclusion": {
		Accepts: "folders/{{folder}}/exclusions/{{name}}", Hash: "04f7d81ca2c8",
		At:  "google/services/logging/resource_logging_exclusion.go:176",
		Why: `as google_logging_project_exclusion, through FolderLoggingExclusionIdParseFunc, which requires "folders"`,
	},
	"google_logging_organization_exclusion": {
		Accepts: "organizations/{{org_id}}/exclusions/{{name}}", Hash: "f98fe9e1d52f",
		At:  "google/services/logging/resource_logging_exclusion.go:176",
		Why: `as google_logging_project_exclusion, through OrganizationLoggingExclusionIdParseFunc, which requires "organizations"`,
	},
	"google_logging_billing_account_exclusion": {
		Accepts: "billingAccounts/{{billing_account}}/exclusions/{{name}}", Hash: "a213c0f3517f",
		At:  "google/services/logging/resource_logging_exclusion.go:176",
		Why: `as google_logging_project_exclusion, through BillingAccountLoggingExclusionIdParseFunc, which requires "billingAccounts"`,
	},
	"google_organization_policy": {
		Accepts: "{{org_id}}/{{constraint}}", Hash: "6f5bdf2d7729",
		At:  "google/services/resourcemanager/resource_google_organization_policy.go:317",
		Why: `resourceGoogleOrganizationPolicyImportState splits the id once on "/" into org_id and constraint and sets no id; the create itself writes "organizations/" + org_id (line 224), so an org_id that works carries no "/"`,
	},
	"google_logging_project_bucket_config": {
		Accepts: "projects/{{project}}/locations/{{location}}/buckets/{{bucket_id}}", Hash: "c3f6287685f1",
		At:     "google/services/logging/resource_logging_bucket_config.go:160",
		Why:    `resourceLoggingBucketConfigImportState("project") parses the id with loggingBucketConfigIDRegex and sets project to "projects/" plus the project ID`,
		Refuse: `sets project to "projects/<id>" where the create took the bare project ID; project is required, ForceNew and not diff-suppressed (resource_logging_project_bucket_config.go:33), so every plan after a read would replace the bucket`,
	},
	"google_project_usage_export_bucket": {
		Accepts: "{{project}}", Hash: "789ef399575b",
		At:  "google/services/compute/resource_project_usage_export_bucket.go:234",
		Why: `resourceProjectUsageBucketImportState sets project to the whole id and sets no id`,
	},
}

// checkImporterRulings fails when a ruling no longer matches the source it
// was made against: the importer's fingerprint moved, the create's id is
// not the one ruled on, or the importer no longer needs a ruling at all.
func checkImporterRulings(ev Evidence) error {
	var errs []string
	for _, t := range sortedKeys(importerRulings) {
		r := importerRulings[t]
		byProv, ok := ev.Types[t]
		if !ok {
			errs = append(errs, fmt.Sprintf("%s: ruled on, but the source registers no such type", t))
			continue
		}
		for _, p := range sortedProviders(byProv) {
			s := byProv[p]
			switch {
			case s.IDFormat != "" || s.Importer != "func":
				errs = append(errs, fmt.Sprintf("%s (%s): ruled on as a hand-written importer, but the source now reads it as %q", t, p, s.Importer))
			case s.ImporterHash != r.Hash:
				errs = append(errs, fmt.Sprintf("%s (%s): the importer at %s has changed since it was ruled on (fingerprint %s, ruled %s); read it again", t, p, s.ImporterAt, s.ImporterHash, r.Hash))
			case s.CreateID != r.Accepts:
				errs = append(errs, fmt.Sprintf("%s (%s): ruled to accept %s, but the create now sets %q", t, p, r.Accepts, s.CreateID))
			case r.Refuse == "" && importerReadsBack(s):
				errs = append(errs, fmt.Sprintf("%s (%s): the importer's ParseImportId already reads the id back; the ruling is not needed", t, p))
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("importer rulings out of step with the source:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}
