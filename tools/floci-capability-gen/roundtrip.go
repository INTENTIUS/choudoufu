// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Cloud Control's write path, which internal/live/cloudcontrol deliberately
// does not have: that package is the discovery plane's READ transport
// (ListResources/GetResource) and widening it with a Create would put a
// write operation into the live path for a probe tool's benefit. The two
// calls below therefore live here, in the tool, speaking the same AWS JSON
// 1.0 wire shape by hand.
//
// Unsigned on purpose, and only ever pointed at an emulator: floci does not
// verify signatures (internal/live/cloudcontrol/doc.go's "Signing" section
// records the same), and this probe has no business creating anything
// anywhere that does.
const (
	targetCreateResource = "CloudApiService.CreateResource"
	targetRequestStatus  = "CloudApiService.GetResourceRequestStatus"

	// emptyDesiredState is the only desired state a generic sweep can send.
	// A per-type minimal create recipe would need a source of each type's
	// required properties, and there is none in this checkout: the
	// CloudFormation resource schemas are not vendored, and inventing them
	// per type is exactly the hand-written type list this generator must
	// not grow. An emulator fills the gap by naming the resource itself
	// (floci answers with a generated "cloudcontrol-resource-<hex>"
	// identifier), which is all the round trip needs - a real identifier to
	// then look for in the list. Against real AWS this would be rejected
	// for nearly every type, which is another reason this tool only ever
	// runs against an emulator.
	emptyDesiredState = "{}"

	// seedPollAttempts / seedPollInterval bound the wait for a create to
	// settle. A create still IN_PROGRESS when they run out is reported as
	// such and classified unverified, never guessed at.
	seedPollAttempts = 10
	seedPollInterval = 300 * time.Millisecond
)

// seedResult is what one CreateResource attempt settled to. status is Cloud
// Control's own OperationStatus ("SUCCESS", "FAILED", "IN_PROGRESS" when
// the poll budget ran out) or "ERROR" when the API refused the call
// outright; identifier is set only for a SUCCESS that named one.
type seedResult struct {
	status     string
	identifier string
	message    string
}

// ok reports whether this seed produced something ListResources could
// plausibly be asked to find.
func (r seedResult) ok() bool { return r.status == "SUCCESS" && r.identifier != "" }

// describe renders the seed outcome for a row's evidence text, so a reader
// can tell which of the several ways a seed can fail actually happened.
func (r seedResult) describe(cfnType string) string {
	switch {
	case r.status == "SUCCESS" && r.identifier == "":
		return fmt.Sprintf("CreateResource(%s, {}) reported SUCCESS but named no identifier", cfnType)
	case r.status == "IN_PROGRESS":
		return fmt.Sprintf("CreateResource(%s, {}) was still IN_PROGRESS after %s", cfnType, time.Duration(seedPollAttempts)*seedPollInterval)
	case r.message != "":
		return fmt.Sprintf("CreateResource(%s, {}) ended %s: %s", cfnType, r.status, r.message)
	default:
		return fmt.Sprintf("CreateResource(%s, {}) ended %s", cfnType, r.status)
	}
}

// ccSeeder makes the two write-path Cloud Control calls probeCloudControl
// needs to prove that a list answers rather than merely returning.
type ccSeeder struct {
	endpoint string
	client   *http.Client
}

func newSeeder(endpoint string) *ccSeeder {
	return &ccSeeder{endpoint: strings.TrimSuffix(endpoint, "/"), client: http.DefaultClient}
}

// wireProgressEvent is the ProgressEvent envelope both calls answer with.
type wireProgressEvent struct {
	ProgressEvent struct {
		Identifier      string `json:"Identifier"`
		RequestToken    string `json:"RequestToken"`
		OperationStatus string `json:"OperationStatus"`
		StatusMessage   string `json:"StatusMessage"`
		ErrorCode       string `json:"ErrorCode"`
	} `json:"ProgressEvent"`
	Type    string `json:"__type"`
	Message string `json:"message"`
}

// post makes one AWS JSON 1.0 call and decodes the ProgressEvent envelope.
// The returned error is transport-level only; an API-shaped refusal comes
// back in the envelope's __type/message, which the caller turns into a
// seedResult rather than an error, exactly the way classifyListResources
// treats a *cloudcontrol.APIError as a finding rather than a failure.
func (s *ccSeeder) post(ctx context.Context, target string, payload any) (wireProgressEvent, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return wireProgressEvent{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint+"/", bytes.NewReader(body))
	if err != nil {
		return wireProgressEvent{}, err
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", target)

	resp, err := s.client.Do(req)
	if err != nil {
		return wireProgressEvent{}, err
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return wireProgressEvent{}, err
	}
	var out wireProgressEvent
	if err := json.Unmarshal(raw, &out); err != nil {
		// A body that is not JSON at all (the HTML error page a crashed
		// handler serves) is a finding about this type, not a transport
		// failure - report it as a refused seed carrying the status code.
		return wireProgressEvent{
			Type:    "UnparseableResponse",
			Message: fmt.Sprintf("HTTP %d with a body that is not JSON", resp.StatusCode),
		}, nil
	}
	return out, nil
}

// seed creates one resource of cfnType with an empty desired state and
// waits for the request to settle. The returned error is transport-level
// only.
func (s *ccSeeder) seed(ctx context.Context, cfnType string) (seedResult, error) {
	created, err := s.post(ctx, targetCreateResource, map[string]string{
		"TypeName":     cfnType,
		"DesiredState": emptyDesiredState,
	})
	if err != nil {
		return seedResult{}, err
	}
	if created.Type != "" {
		return seedResult{status: "ERROR", message: errorText(created.Type, created.Message)}, nil
	}

	ev := created.ProgressEvent
	token := ev.RequestToken
	for i := 0; i < seedPollAttempts && ev.OperationStatus == "IN_PROGRESS" && token != ""; i++ {
		// The first poll goes out immediately. floci answers CreateResource
		// IN_PROGRESS and has already settled the request by the time the
		// next call arrives, so sleeping before the first status check
		// bought nothing and cost seedPollInterval per type across the whole
		// sweep - 645 listable types, three and a bit minutes of pure sleep.
		if i > 0 {
			select {
			case <-ctx.Done():
				return seedResult{}, ctx.Err()
			case <-time.After(seedPollInterval):
			}
		}
		status, err := s.post(ctx, targetRequestStatus, map[string]string{"RequestToken": token})
		if err != nil {
			return seedResult{}, err
		}
		if status.Type != "" {
			return seedResult{status: "ERROR", message: errorText(status.Type, status.Message)}, nil
		}
		ev = status.ProgressEvent
	}

	msg := ev.StatusMessage
	if ev.ErrorCode != "" {
		msg = errorText(ev.ErrorCode, ev.StatusMessage)
	}
	return seedResult{status: ev.OperationStatus, identifier: ev.Identifier, message: msg}, nil
}

// errorText joins an API error's code and message the way a reader of the
// evidence string wants them, tolerating either half being absent.
func errorText(code, message string) string {
	code = shortCode(code)
	switch {
	case code != "" && message != "":
		return code + ": " + message
	case code != "":
		return code
	default:
		return message
	}
}

// shortCode strips the shape-ID prefix AWS JSON puts in front of an error
// type, the same normalization internal/live/cloudcontrol's errorCode does.
func shortCode(wireType string) string {
	if i := strings.LastIndex(wireType, "#"); i >= 0 {
		return wireType[i+1:]
	}
	return wireType
}
