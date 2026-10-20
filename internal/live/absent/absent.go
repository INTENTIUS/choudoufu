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
// The Azure entries (GitHub issue #1120, under #1122's ruling) are what
// hashicorp/azurerm 5.10.0 hands back from an importer that reads the
// object and gets a 404. Most azurerm importers only parse the id
// (pluginsdk.ImporterValidatingResourceId) and so never fail on a missing
// object: the read that follows sees the 404, clears the id and comes back
// null, which both callers already fold into absence. The importers in
// 117 service files built on ImporterValidatingResourceIdThen,
// ImporterValidatingIdentityThen or a typed CustomImporter may
// call the API, and wrap its error with "retrieving %s: %+v":
//
//   - "unexpected status 404 (404 Not Found)" is how
//     hashicorp/go-azure-sdk/sdk v0.20261005.1221110, the client azurerm
//     5.10.0 vendors, renders any 404 (sdk/client/client.go, Execute: the
//     "unexpected status %s with %s" error, status being "404 (404 Not
//     Found)"), followed by ARM's own code: ResourceNotFound,
//     ResourceGroupNotFound, ParentResourceNotFound.
//   - "StatusCode=404" is go-autorest v0.11.30's DetailedError
//     (autorest/error.go, Error), still behind azurerm's older clients:
//     keyvault's nested-item importer and the api management, monitor,
//     spring cloud and data factory resources that use them.
//
// azure/azapi 2.13.0 adds nothing here: azapi_resource's ImportState
// (internal/services/azapi_resource.go, the utils.ResponseErrorWasNotFound
// branch after withApiVersionFallback) answers a 404 with
// response.State.RemoveResource and no diagnostic, so a missing object is
// a null import, and azapi_data_plane_resource's ImportState only parses
// the id. Its non-404 failure, "Failed to retrieve resource", is a real
// failure and stays one.
var Signals = []string{
	"couldn't find resource",
	"ResourceNotFoundException",
	"unexpected status 404 (404 Not Found)",
	"StatusCode=404",
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
//   - ARM's not-found error codes, ResourceNotFound, ResourceGroupNotFound
//     and ParentResourceNotFound, as whole words: whichever client
//     rendered the 404, the code ARM put in the body is carried through
//     (go-azure-sdk's odata.Error.String puts it first, autorest's
//     ServiceError as Code="..."). A whole word so that it names the code
//     and not a longer identifier that happens to contain it.
//   - `<resource id> was not found`, the sentence hashicorp/azurerm
//     5.10.0's own importers write when they check for the 404 themselves:
//     "%s was not found" over a go-azure-sdk resource id, whose String()
//     ends in ")" (internal/services/cdn/cdn_frontdoor_custom_domain_association_import.go:29,
//     internal/services/compute/virtual_machine_data_disk_attachment_resource.go:47
//     and :70, internal/services/apimanagement/api_management_policy_fragment_resource.go:48),
//     or over a quoted name (internal/services/bot/bot_channels_registration_resource.go:64,
//     internal/services/bot/bot_web_app_resource.go:55). Matched only right
//     after a closing parenthesis or quote, for the same reason as the
//     Kubernetes shape: "was not found" alone is English.
var Patterns = []*regexp.Regexp{
	regexp.MustCompile(`\b[a-z][a-z0-9.-]*(?:/[a-z0-9.-]+)? "[^"]+" not found\b`),
	regexp.MustCompile(`\b(?:ResourceNotFound|ResourceGroupNotFound|ParentResourceNotFound)\b`),
	regexp.MustCompile(`[)"] was not found\b`),
}

// Matches reports whether one diagnostic's summary or detail is a
// not-found shape.
func Matches(summary, detail string) bool {
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
	return false
}
