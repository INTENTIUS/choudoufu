// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
)

// deniedSweepServer is GitHub issue #1513's Cloud Control: the tagging
// API answers empty, and each principal is refused Cloud Control
// ListResources with AccessDeniedException on one service's types of its
// own ([deniedSweepServices]) and answered empty on the rest, so the two
// provider configurations' denials are disjoint and a warning built from
// either alone is visibly short of the run's.
type deniedSweepServer struct {
	mu     sync.Mutex
	denied map[string][]string // principal -> CFN types denied
}

func (s *deniedSweepServer) handler(w http.ResponseWriter, r *http.Request) {
	target := r.Header.Get("X-Amz-Target")
	if strings.HasSuffix(target, ".GetResources") {
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		_ = json.NewEncoder(w).Encode(map[string]any{"ResourceTagMappingList": []any{}})
		return
	}
	if target != "CloudApiService.ListResources" {
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		_ = json.NewEncoder(w).Encode(map[string]any{"ResourceDescriptions": []any{}})
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	cfn, _ := body["TypeName"].(string)
	principal := principalOf(r.Header.Get("Authorization"))
	if !strings.HasPrefix(cfn, "AWS::"+deniedSweepServices[principal]+"::") {
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		_ = json.NewEncoder(w).Encode(map[string]any{"ResourceDescriptions": []any{}})
		return
	}
	s.mu.Lock()
	s.denied[principal] = append(s.denied[principal], cfn)
	s.mu.Unlock()
	action := deniedSweepAction(principal, cfn)
	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"__type": "com.amazonaws.cloudformation#AccessDeniedException",
		"message": fmt.Sprintf("User: arn:aws:sts::%s:assumed-role/plan/run is not authorized to perform: %s on resource: * because no identity-based policy allows the %s action",
			principal, action, action),
	})
}

// deniedSweepServices is the CFN service each principal is refused on:
// the fixture's default configuration (account 111111111111) on X-Ray, its
// other_account alias (222222222222) on Access Analyzer.
var deniedSweepServices = map[string]string{
	"111111111111": "XRay",
	"222222222222": "AccessAnalyzer",
}

// deniedSweepAction is the action a denial names: the CFN service, lower
// cased, with a verb that differs per principal (List for the first
// account, Describe for the second), so each configuration's grant pattern
// is its own.
func deniedSweepAction(principal, cfn string) string {
	parts := strings.Split(cfn, "::")
	service := "unknown"
	if len(parts) == 3 {
		service = strings.ToLower(parts[1])
	}
	verb := "List"
	if principal == "222222222222" {
		verb = "Describe"
	}
	return service + ":" + verb + "Things"
}

// TestLivePlan_deniedSweepIsOneWarningAcrossProviderConfigurations is
// GitHub issue #1513: #1052's ruling is one warning for every Cloud
// Control listing the run was refused, and a live-plan runs discovery once
// per provider configuration. Two configurations, two accounts, each
// refused on its own listings with its own action: the run raises one
// "Incomplete sweep" warning whose pattern covers both, and the log names
// every denied type with the configuration whose credential was refused.
func TestLivePlan_deniedSweepIsOneWarningAcrossProviderConfigurations(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan-two-accounts"), td)
	t.Chdir(td)

	srv := &deniedSweepServer{denied: map[string][]string{}}
	server := httptest.NewServer(http.HandlerFunc(srv.handler))
	t.Cleanup(server.Close)
	t.Setenv("TOFU_LIVE_CLOUDCONTROL", "")
	t.Setenv("AWS_ENDPOINT_URL", server.URL)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "us-east-1")

	var logBuf bytes.Buffer
	prevLog := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(prevLog) })

	cloud := newStatelessTestCloud()
	cloud.putMarked("aws_s3_bucket", "tofu-two-accounts-home", "two-accounts-unit", "aws_s3_bucket.home", map[string]string{
		"id": "tofu-two-accounts-home", "bucket": "tofu-two-accounts-home",
	})
	cloud.putMarked("aws_s3_bucket", "tofu-two-accounts-other", "two-accounts-unit", "aws_s3_bucket.other_account", map[string]string{
		"id": "tofu-two-accounts-other", "bucket": "tofu-two-accounts-other",
	})

	c, done := newLivePlanCommand(t, cloud)
	code := c.Run([]string{"-no-color", "-estate=two-accounts-unit"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	srv.mu.Lock()
	denied := map[string][]string{}
	for p, types := range srv.denied {
		denied[p] = append([]string(nil), types...)
	}
	srv.mu.Unlock()
	t.Logf("denied per principal: %v", denied)
	for _, p := range []string{"111111111111", "222222222222"} {
		if len(denied[p]) == 0 {
			t.Fatalf("no Cloud Control listing was made as %s, so this run does not have two configurations each refused; denied: %v", p, denied)
		}
	}

	all := output.All()
	n := strings.Count(all, "Incomplete sweep for undeclared resources")
	if n != 1 {
		t.Fatalf("want one \"Incomplete sweep for undeclared resources\" warning for the run, got %d:\n%s", n, all)
	}

	var cfnTypes []string
	seen := map[string]bool{}
	for _, types := range denied {
		for _, cfn := range types {
			if !seen[cfn] {
				seen[cfn] = true
				cfnTypes = append(cfnTypes, cfn)
			}
		}
	}
	sort.Strings(cfnTypes)
	flat := strings.Join(strings.Fields(all), " ")
	want := fmt.Sprintf("Cloud Control ListResources was denied for %d of the types the sweep covers (%s) through provider configurations aws and aws.other_account, so a resource of any of those types this estate owns but no longer declares WILL NOT be proposed for destruction by this run. Each denial names the read its type's list handler makes. Grant their roles accessanalyzer:Describe*, xray:List*, then re-run. Every denied type, the configuration it went through and the action it named is one [WARN] line in the log: run with TF_LOG=WARN, or TF_LOG_PATH to write it to a file.",
		len(cfnTypes), strings.Join(cfnTypes, ", "))
	if len(cfnTypes) > 5 {
		t.Fatalf("the fixture's denied set grew past the five the warning names (%v); widen the expectation", cfnTypes)
	}
	if !strings.Contains(flat, want) {
		t.Errorf("the one warning is not the run's, over both configurations.\nwant: %s\noutput:\n%s", want, all)
	}

	// The log names every denied type with the configuration it went
	// through, which is what says whose credential to widen.
	logged := logBuf.String()
	for p, types := range denied {
		label := "aws"
		if p == "222222222222" {
			label = "aws.other_account"
		}
		for _, cfn := range types {
			line := fmt.Sprintf("sweep denied: Cloud Control ListResources on %s (for ", cfn)
			found := false
			for _, l := range strings.Split(logged, "\n") {
				if strings.Contains(l, line) && strings.Contains(l, ", through provider "+label+") needs "+deniedSweepAction(p, cfn)+":") {
					found = true
				}
			}
			if !found {
				t.Errorf("no log line names %s denied through %s needing %s", cfn, label, deniedSweepAction(p, cfn))
			}
		}
	}
}
