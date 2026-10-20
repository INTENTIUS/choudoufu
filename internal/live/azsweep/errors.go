// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package azsweep

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// The diagnostic summaries a failed sweep is reported under, one per typed
// error, named so that the discovery leg that consumes this client can
// register each in internal/live/discovery/refusals.go (the refusalscan
// lockstep finds a Summary-prefixed constant in the package that emits
// the diagnostic) without retyping the words.
const (
	// SummaryResourceGraphNotRegistered refuses the sweep: a subscription
	// the estate's resources resolve to has not registered the
	// Microsoft.ResourceGraph resource provider (#1122).
	SummaryResourceGraphNotRegistered = "Microsoft.ResourceGraph not registered"
	// SummarySubscriptionUnreadable warns of an incomplete sweep: the
	// credential cannot read a subscription, so its tagged resources are
	// unknown, never zero.
	SummarySubscriptionUnreadable = "Azure subscription not readable by the sweep"
	// SummaryResourceGraphThrottled refuses the sweep: Resource Graph kept
	// throttling past the retries its headers allow, and a plan made
	// without the subscription would propose no removal in it (#1120
	// unit 11, internal/live/discovery's resourcegraph.go).
	SummaryResourceGraphThrottled = "Azure Resource Graph throttled the sweep"
	// SummarySubscriptionUncovered warns of an incomplete sweep: no
	// configured azurerm or azapi credential covers the subscription.
	SummarySubscriptionUncovered = "Azure subscription not covered by any provider block"
	// SummarySweepUnavailable is every other failure: the query failed
	// in a way none of the above names, or a later page failed.
	SummarySweepUnavailable = "Azure sweep unavailable"
)

// Summary is the diagnostic summary err is reported under: the summary of
// the first typed error in its chain, or [SummarySweepUnavailable].
func Summary(err error) string {
	var nr *NotRegisteredError
	var ur *UnreadableSubscriptionError
	var th *ThrottledError
	var uc *UncoveredSubscriptionError
	switch {
	case err == nil:
		return ""
	case errors.As(err, &nr):
		return SummaryResourceGraphNotRegistered
	case errors.As(err, &ur):
		return SummarySubscriptionUnreadable
	case errors.As(err, &th):
		return SummaryResourceGraphThrottled
	case errors.As(err, &uc):
		return SummarySubscriptionUncovered
	}
	return SummarySweepUnavailable
}

// ResourceProvider is the resource provider namespace the sweep calls,
// which a subscription has to have registered.
const ResourceProvider = "Microsoft.ResourceGraph"

// NotRegisteredError is a subscription that has not registered
// Microsoft.ResourceGraph. The fix is registering it (`az provider
// register --namespace Microsoft.ResourceGraph`), never a retry and never
// a grant, and the sweep is refused until then: a sweep that skipped the
// subscription would hide every orphan in it.
type NotRegisteredError struct {
	Subscription string
	// Code is ARM's error code, one of [unregisteredCodes].
	Code    string
	Message string
}

func (e *NotRegisteredError) Error() string {
	return fmt.Sprintf("subscription %s has not registered the %s resource provider (%s: %s)", e.Subscription, ResourceProvider, e.Code, e.Message)
}

// UnreadableSubscriptionError is a subscription the credential cannot
// query: no role assignment gives it read on the subscription, the
// subscription does not exist, or it belongs to a tenant the token was
// not issued for. The sweep of that subscription is incomplete, and is
// warned about by name rather than read as empty.
type UnreadableSubscriptionError struct {
	Subscription string
	Credentials  Credentials
	Status       int
	Code         string
	Message      string
}

func (e *UnreadableSubscriptionError) Error() string {
	return fmt.Sprintf("subscription %s cannot be read as %s (HTTP %d %s: %s), so its resources were not swept", e.Subscription, e.Credentials, e.Status, e.Code, e.Message)
}

// ThrottledError is a query Resource Graph still answered 429 after
// [MaxThrottleRetries] waits, or asked for a wait longer than
// [MaxThrottleWait].
type ThrottledError struct {
	Subscription string
	Attempts     int
	// RetryAfter is the last wait the service asked for.
	RetryAfter time.Duration
}

func (e *ThrottledError) Error() string {
	return fmt.Sprintf("Resource Graph throttled the query for subscription %s %d time(s), last asking for a %s wait, so its resources were not swept", e.Subscription, e.Attempts, e.RetryAfter)
}

// PartialError is a query that read at least one page and then failed:
// the pages already read are discarded rather than returned, for
// internal/live/gcpsweep's PartialError reason (a missed page is an orphan
// nobody is told about). Err may itself be one of the typed errors above.
type PartialError struct {
	Subscription string
	PagesRead    int
	RowsRead     int
	Err          error
}

func (e *PartialError) Error() string {
	return fmt.Sprintf("querying subscription %s failed after %d page(s), so none of its %d row(s) are used: %v", e.Subscription, e.PagesRead, e.RowsRead, e.Err)
}

func (e *PartialError) Unwrap() error { return e.Err }

// UncoveredSubscriptionError is a subscription the sweep was asked to
// query that no client in its [Coverage] is set to query.
type UncoveredSubscriptionError struct {
	Subscription string
}

func (e *UncoveredSubscriptionError) Error() string {
	return fmt.Sprintf("no configured azurerm or azapi provider credential covers subscription %s, so it was not swept", e.Subscription)
}

// APIError is any other non-success answer, with ARM's error body.
type APIError struct {
	Subscription string
	Status       int
	Code         string
	Message      string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("Resource Graph query for subscription %s failed: HTTP %d %s: %s", e.Subscription, e.Status, e.Code, e.Message)
}

// unregisteredCodes are the error codes ARM answers a call to a resource
// provider the subscription has not registered with. They are
// azure-sdk-for-go's own list (sdk/azcore/arm/runtime/policy_register_rp.go,
// unregisteredRPCodes, the codes its RP-registration policy acts on),
// matched case-insensitively as azcore does.
var unregisteredCodes = []string{
	"MissingSubscriptionRegistration",
	"MissingRegistrationForResourceProvider",
	"Subscription Not Registered",
	"SubscriptionNotRegistered",
}

// unreadableCodes are ARM and Resource Graph codes that name the
// subscription itself as out of the credential's reach, whatever the
// HTTP status they arrive with:
//
//   - NoValidSubscriptionsInQueryRequest is Resource Graph's answer, as
//     a detail under a 400 BadRequest, to a request none of whose
//     subscriptions the caller can read. With one subscription per
//     request it is exactly "this subscription is unreadable".
//   - AuthorizationFailed is ARM's 403 for a principal with no role
//     granting the action on the scope.
//   - SubscriptionNotFound and InvalidSubscriptionId are a subscription
//     that does not exist or is not visible to the tenant.
//   - InvalidAuthenticationTokenTenant is a 401 for a token from a tenant
//     other than the subscription's: the credential is good, and this
//     subscription is not in its tenant.
//
// These are from Azure's documentation and the ARM error catalogue and
// await measurement on floci-az (live/ADDING-A-PLATFORM.md, requirement 2).
var unreadableCodes = []string{
	"NoValidSubscriptionsInQueryRequest",
	"AuthorizationFailed",
	"SubscriptionNotFound",
	"InvalidSubscriptionId",
	"InvalidAuthenticationTokenTenant",
}

// armError is ARM's error envelope, which Resource Graph answers in.
type armError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"details"`
	} `json:"error"`
}

// classify turns a non-success answer for subscription's query into one of
// this package's typed errors.
func classify(subscription string, creds Credentials, status int, body []byte) error {
	var env armError
	_ = json.Unmarshal(body, &env)
	codes := []string{env.Error.Code}
	msgs := []string{env.Error.Message}
	for _, d := range env.Error.Details {
		codes = append(codes, d.Code)
		msgs = append(msgs, d.Message)
	}
	message := strings.TrimSpace(strings.Join(nonEmpty(msgs), " "))
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	for _, c := range codes {
		if oneOf(c, unregisteredCodes) {
			return &NotRegisteredError{Subscription: subscription, Code: c, Message: message}
		}
	}
	for _, c := range codes {
		if oneOf(c, unreadableCodes) {
			return &UnreadableSubscriptionError{Subscription: subscription, Credentials: creds, Status: status, Code: c, Message: message}
		}
	}
	// A 403 with any other code is still this credential refused on this
	// subscription's scope: the token was accepted (or the answer would
	// be 401) and the subscription's RBAC said no.
	if status == http.StatusForbidden {
		return &UnreadableSubscriptionError{Subscription: subscription, Credentials: creds, Status: status, Code: env.Error.Code, Message: message}
	}
	return &APIError{Subscription: subscription, Status: status, Code: env.Error.Code, Message: message}
}

func oneOf(code string, set []string) bool {
	if code == "" {
		return false
	}
	for _, s := range set {
		if strings.EqualFold(code, s) {
			return true
		}
	}
	return false
}

func nonEmpty(ss []string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
