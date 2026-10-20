// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package absent answers one question for any caller that has just asked a
// provider to import a resource and been handed an error: is the provider
// saying "there is no such object", or failing to answer at all.
//
// Textual is all the answer can be. A diagnostic that has crossed the
// provider plugin protocol carries only Summary and Detail strings; nothing
// structured about the underlying provider error survives the wire.
//
// Two callers need exactly this, on two different paths to the same
// provider RPC: internal/live/projection/build.go's pre-walk projection
// (importAndRead) and internal/tofu/node_resource_plan_instance.go's
// plan-node seam (importState under a resolver-supplied target). Neither
// may import the other - internal/tofu must never import the fork's
// live-mode package, and projection already imports internal/tofu - and
// until GitHub issue #1064 each carried its own copy of the shapes, kept in
// sync by hand. This package is the leaf both import, the same shape
// internal/live/noimporter takes for the "no Importer at all" question.
package absent

import (
	"regexp"
	"strings"
)

// Signals are substrings of a provider's own ImportResourceState diagnostic
// that mean "no such object":
//
//   - "couldn't find resource" is the exact, hardcoded default message
//     terraform-plugin-sdk's retry.NotFoundError renders when a provider's
//     internal finder comes back empty and sets no more specific text. It
//     is a generic SDK convention used across the whole of
//     terraform-provider-aws wherever a resource's Read is built on a
//     "find the live object or report NotFoundError" finder.
//     aws_lambda_permission - whose import lookup calls GetPolicy on the
//     function, not the permission, and so 404s with this shape the moment
//     the function itself does not exist yet either - is a confirmed
//     instance (GitHub issue #297), not the only one this covers.
//   - "ResourceNotFoundException" is AWS's own API error code, for a
//     provider that surfaces the untranslated API error instead of going
//     through the generic finder convention above.
//
// The Azure answers are not here: an Azure 404 is only an absence when it
// is about the object being imported, which no substring can tell. See
// [Object] and [azureNotFound].
var Signals = []string{
	"couldn't find resource",
	"ResourceNotFoundException",
}

// Patterns are the shapes that need more than a substring to recognise
// safely:
//
//   - `<resource> "<name>" not found` is k8s.io/apimachinery's own NotFound
//     status message (errors.NewNotFound renders `%s %q not found`), which
//     hashicorp/kubernetes surfaces untranslated out of a type-specific
//     importer: `serviceaccounts "app" not found` (GitHub issue #1064, the
//     first type admitted through the object-metadata rule to reach a
//     greenfield plan). Matched as a shape, because "not found" on its own
//     is ordinary English a genuine failure could carry.
var Patterns = []*regexp.Regexp{
	regexp.MustCompile(`\b[a-z][a-z0-9.-]*(?:/[a-z0-9.-]+)? "[^"]+" not found\b`),
}

// Object names the object an import asked the provider for: the import
// ID, the identity's attribute values, or both. A not-found answer from
// Azure only counts when it is about this object (GitHub issue #2133,
// finding 7); the AWS and Kubernetes shapes do not consult it.
type Object struct {
	ID       string
	Identity map[string]string
}

// Matches reports whether one diagnostic's summary or detail is a
// not-found shape about object.
func Matches(summary, detail string, object Object) bool {
	for _, signal := range Signals {
		if strings.Contains(summary, signal) || strings.Contains(detail, signal) {
			return true
		}
	}
	for _, pattern := range Patterns {
		if pattern.MatchString(summary) || pattern.MatchString(detail) {
			return true
		}
	}
	return azureNotFound(summary, object) || azureNotFound(detail, object)
}

// The Azure shapes (GitHub issue #1120, under #1122's ruling; narrowed by
// #2133 finding 7) are what hashicorp/azurerm 5.10.0 hands back from an
// importer that reads the object and gets a 404. Most azurerm importers
// only parse the id (pluginsdk.ImporterValidatingResourceId) and so never
// fail on a missing object: the read that follows sees the 404, clears the
// id and comes back null, which both callers already fold into absence.
// The importers in 117 service files built on
// ImporterValidatingResourceIdThen, ImporterValidatingIdentityThen or a
// typed CustomImporter may call the API, and wrap its error with
// "retrieving %s: %+v".
//
// A bare 404 is not enough. ARM answers 404 for more than a missing object,
// and an importer's sub-call can 404 on some other object, so each shape
// below must also carry ARM's not-found code (ResourceNotFound,
// ResourceGroupNotFound or ParentResourceNotFound, never
// NoRegisteredProviderFound, InvalidResourceType or a data-plane code) and
// every name ARM's message quotes must belong to the [Object] being
// imported. A missing parent or resource group of that object is an
// absence of the object; a missing anything else is a failure.
//
//   - azureSDK404 is hashicorp/go-azure-sdk/sdk v0.20261005.1221110, the
//     client azurerm 5.10.0 vendors: sdk/client/client.go:596 (Execute)
//     returns "unexpected status %s with %s", status being "404 (404 Not
//     Found)" (:554-562) and the text "error: %s" over odata.Error
//     (:579), whose String puts Code first and Message second, joined by
//     ": " (sdk/odata/errors.go:105-118). A 404 with no OData body
//     ("received with no body", :589) names no code and is not an absence.
//   - azureAutorest404 is go-autorest v0.11.30, still behind azurerm's
//     older clients: DetailedError renders "StatusCode=%d -- Original
//     Error: %v" (autorest/error.go:97) over a RequestError, "autorest/azure:
//     Service returned an error. Status=%v %v" (autorest/azure/azure.go:189),
//     whose ServiceError renders Code=%q Message=%q (azure.go:60).
//   - azureSentence is the sentence azurerm's own importers write when they
//     check the 404 themselves with response.WasNotFound, which tests the
//     status alone (go-azure-helpers lang/response/response.go:26): "%s was
//     not found" over a resource id whose String() ends in ")"
//     (internal/services/cdn/cdn_frontdoor_custom_domain_association_import.go:29,
//     internal/services/compute/virtual_machine_data_disk_attachment_resource.go:47,
//     internal/services/apimanagement/api_management_policy_fragment_resource.go:48),
//     or over a quoted name (internal/services/bot/bot_channels_registration_resource.go:64,
//     internal/services/bot/bot_web_app_resource.go:55). The provider has
//     dropped ARM's code by then, so only the names can be checked: every
//     double-quoted value in the sentence must belong to the object. The
//     data disk importer's sentence names the virtual machine, whose
//     subscription, group and name are all segments of the attachment's id.
//
// azure/azapi 2.13.0 adds nothing here: azapi_resource's ImportState
// (internal/services/azapi_resource.go, the utils.ResponseErrorWasNotFound
// branch after withApiVersionFallback) answers a 404 with
// response.State.RemoveResource and no diagnostic, so a missing object is
// a null import, and azapi_data_plane_resource's ImportState only parses
// the id. Its non-404 failure, "Failed to retrieve resource", is a real
// failure and stays one.
var (
	azureSDK404      = regexp.MustCompile(`unexpected status 404 \(404 Not Found\) with error: (?:ResourceNotFound|ResourceGroupNotFound|ParentResourceNotFound): ([^\n]*)`)
	azureAutorest404 = regexp.MustCompile(`StatusCode=404 -- Original Error: autorest/azure: Service returned an error\. Status=404 Code="(?:ResourceNotFound|ResourceGroupNotFound|ParentResourceNotFound)" Message="((?:[^"\\]|\\.)*)"`)
	azureSentence    = regexp.MustCompile(`[)"] was not found\b`)

	singleQuoted = regexp.MustCompile(`'([^']+)'`)
	doubleQuoted = regexp.MustCompile(`"([^"]+)"`)
)

// azureNotFound reports whether text is one of the Azure shapes above and
// is about object.
func azureNotFound(text string, object Object) bool {
	known, haveID := object.names()
	if len(known) == 0 {
		return false
	}
	for _, shape := range []*regexp.Regexp{azureSDK404, azureAutorest404} {
		m := shape.FindStringSubmatch(text)
		if m == nil {
			continue
		}
		// ARM's message quotes what it could not find:
		// 'Microsoft.ContainerService/managedClusters/aks' and 'rg' for
		// ResourceNotFound, the group for ResourceGroupNotFound, the
		// parent for ParentResourceNotFound.
		quoted := singleQuoted.FindAllStringSubmatch(m[1], -1)
		if len(quoted) == 0 {
			continue
		}
		about := true
		for _, q := range quoted {
			segments := strings.Split(strings.ToLower(q[1]), "/")
			// With only an identity in hand there are no type segments
			// to compare, so the object's own name stands for the path.
			if !haveID {
				segments = segments[len(segments)-1:]
			}
			for _, segment := range segments {
				if !known[segment] {
					about = false
				}
			}
		}
		if about {
			return true
		}
	}
	if azureSentence.MatchString(text) {
		quoted := doubleQuoted.FindAllStringSubmatch(text, -1)
		if len(quoted) == 0 {
			return false
		}
		for _, q := range quoted {
			if !known[strings.ToLower(q[1])] {
				return false
			}
		}
		return true
	}
	return false
}

// names is every name the object answers to, lowercased because ARM ids
// and names compare case-insensitively (#1122's ruling): each segment of
// the import ID, and each identity value and its segments. haveID says
// whether an ARM id was among them.
func (o Object) names() (map[string]bool, bool) {
	known := map[string]bool{}
	add := func(s string) {
		for _, segment := range strings.Split(strings.ToLower(s), "/") {
			if segment != "" {
				known[segment] = true
			}
		}
	}
	add(o.ID)
	for _, v := range o.Identity {
		add(v)
	}
	return known, strings.HasPrefix(o.ID, "/")
}
