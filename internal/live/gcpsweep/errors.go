// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package gcpsweep

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/api/googleapi"
)

// Service is the API a sweep calls, which is what a project has to have
// enabled.
const Service = "cloudasset.googleapis.com"

// Permission is the one IAM permission a project's search needs. A
// PERMISSION_DENIED that does not name a permission of its own is reported
// as missing this one, since the search asks for nothing else.
const Permission = "cloudasset.assets.searchAllResources"

// ServiceDisabledError is a project on which cloudasset.googleapis.com is
// not enabled: HTTP 403, an ErrorInfo detail with reason SERVICE_DISABLED,
// and a message that reads "Cloud Asset API has not been used in project
// N before or it is disabled". The fix is enabling the API on the project,
// never a retry and never a grant.
type ServiceDisabledError struct {
	// Project is the project the search was for.
	Project string
	// Service is the disabled service the error names, normally [Service].
	Service string
	// ActivationURL is the console link the error's own metadata gives for
	// enabling it, or "" when it gave none.
	ActivationURL string
	// Err is the API's own error.
	Err error
}

func (e *ServiceDisabledError) Error() string {
	return fmt.Sprintf("%s is not enabled on project %s: %v", e.Service, e.Project, e.Err)
}

func (e *ServiceDisabledError) Unwrap() error { return e.Err }

// PermissionDeniedError is a search the credential is not allowed to make:
// HTTP 403 PERMISSION_DENIED for any reason but a disabled API. Permission
// is the permission the error's own ErrorInfo names when it names one,
// else [Permission].
type PermissionDeniedError struct {
	Project    string
	Permission string
	// Credentials is what the client authenticated with.
	Credentials Credentials
	Err         error
}

func (e *PermissionDeniedError) Error() string {
	return fmt.Sprintf("searching project %s as %s was denied: needs %s: %v", e.Project, e.Credentials, e.Permission, e.Err)
}

func (e *PermissionDeniedError) Unwrap() error { return e.Err }

// PartialError is a search that read at least one page and then failed:
// the pages already read are discarded rather than returned, because a
// sweep that reads them as the whole answer would miss every resource on
// the pages it never read, and a missed resource is an orphan nobody is
// told about. Err is the failure of page PagesRead+1, and may itself be a
// [ServiceDisabledError] or [PermissionDeniedError] (errors.As reaches it).
type PartialError struct {
	Project string
	// PagesRead is how many pages arrived before the failure, and
	// ResultsRead how many results they held, for a log line; the results
	// themselves are gone.
	PagesRead   int
	ResultsRead int
	Err         error
}

func (e *PartialError) Error() string {
	return fmt.Sprintf("searching project %s failed after %d page(s), so none of its %d result(s) are used: %v", e.Project, e.PagesRead, e.ResultsRead, e.Err)
}

func (e *PartialError) Unwrap() error { return e.Err }

// UncoveredProjectError is a project the sweep was asked to search that no
// client in its [Coverage] is set to search: no provider block's
// credential was matched to it. It is reported rather than skipped, since
// a skipped project is one whose orphans nobody hears about.
type UncoveredProjectError struct {
	Project string
}

func (e *UncoveredProjectError) Error() string {
	return fmt.Sprintf("no configured google provider credential covers project %s, so it was not searched", e.Project)
}

// classify turns the API's error for project's search into one of this
// package's typed errors when it is one, and returns it unchanged
// otherwise.
func classify(project string, creds Credentials, err error) error {
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != http.StatusForbidden {
		return err
	}
	info := errorInfos(gerr)
	for _, i := range info {
		if i.reason == "SERVICE_DISABLED" {
			svc := i.metadata["service"]
			if svc == "" {
				svc = Service
			}
			return &ServiceDisabledError{Project: project, Service: svc, ActivationURL: i.metadata["activationUrl"], Err: err}
		}
	}
	for _, item := range gerr.Errors {
		if item.Reason == "accessNotConfigured" {
			return &ServiceDisabledError{Project: project, Service: Service, Err: err}
		}
	}
	if strings.Contains(gerr.Message, "has not been used in project") || strings.Contains(gerr.Message, "it is disabled") {
		return &ServiceDisabledError{Project: project, Service: Service, Err: err}
	}
	perm := Permission
	for _, i := range info {
		if p := i.metadata["permission"]; p != "" {
			perm = p
			break
		}
	}
	return &PermissionDeniedError{Project: project, Permission: perm, Credentials: creds, Err: err}
}

// errorInfo is a google.rpc.ErrorInfo detail of an API error.
type errorInfo struct {
	reason   string
	domain   string
	metadata map[string]string
}

const errorInfoType = "type.googleapis.com/google.rpc.ErrorInfo"

// errorInfos reads the ErrorInfo details out of an API error's decoded
// details, which googleapi leaves as JSON maps.
func errorInfos(gerr *googleapi.Error) []errorInfo {
	var out []errorInfo
	for _, d := range gerr.Details {
		m, ok := d.(map[string]any)
		if !ok || m["@type"] != errorInfoType {
			continue
		}
		i := errorInfo{metadata: map[string]string{}}
		i.reason, _ = m["reason"].(string)
		i.domain, _ = m["domain"].(string)
		if md, ok := m["metadata"].(map[string]any); ok {
			for k, v := range md {
				if s, ok := v.(string); ok {
					i.metadata[k] = s
				}
			}
		}
		out = append(out, i)
	}
	return out
}
