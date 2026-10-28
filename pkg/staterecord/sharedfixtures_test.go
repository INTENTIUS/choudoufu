// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// The shared bucket fixtures (GitHub issues chant#4558 and choudoufu#2293).
// chant's bucket check and this package's contract check give the same
// verdict on the same store answer, and these three files are where both
// are held to it. They are vendored unmodified from INTENTIUS/chant
// packages/core/src/bucket-check/fixtures/{s3,gcs,azure}.json at
// 0efd96d6dd23ef04563c50423bc3095386db35db; testdata/bucket-check/SOURCE.txt
// says how to refresh them.
const sharedFixturesDir = "testdata/bucket-check"

type sharedFixtureFile struct {
	Schema int                 `json:"schema"`
	Store  string              `json:"store"`
	Cases  []sharedFixtureCase `json:"cases"`
}

type sharedFixtureCase struct {
	Name       string                     `json:"name"`
	Bucket     string                     `json:"bucket"`
	Container  string                     `json:"container"`
	Namespaces []string                   `json:"namespaces"`
	Input      map[string]json.RawMessage `json:"input"`
	Expect     []sharedFixtureVerdict     `json:"expect"`
}

type sharedFixtureVerdict struct {
	Setting    string `json:"setting"`
	Verdict    string `json:"verdict"`
	Unwaivable bool   `json:"unwaivable"`
}

// sharedFixtureError is a failed read: {"error": {"code", "status"}}.
type sharedFixtureError struct {
	Error *struct {
		Code   string `json:"code"`
		Status int    `json:"status"`
	} `json:"error"`
}

func loadSharedFixtures(t *testing.T, name string) sharedFixtureFile {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(sharedFixturesDir, name))
	if err != nil {
		t.Fatal(err)
	}
	var f sharedFixtureFile
	if err := json.Unmarshal(body, &f); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if f.Schema != 1 {
		t.Fatalf("%s: schema %d; this test reads schema 1", name, f.Schema)
	}
	if len(f.Cases) == 0 {
		t.Fatalf("%s: no cases; a fixture set that is empty passes for the wrong reason", name)
	}
	return f
}

// sharedVerdict is a finding as live-bucket -json reports it
// (internal/command's buildLiveBucketReport).
func sharedVerdict(f Finding) string {
	switch {
	case f.Outcome == Unreadable:
		return "unreadable"
	case f.Outcome == NotChecked:
		return "not_checked"
	case f.Outcome == Warned:
		return "warn"
	case !f.OK():
		return "fail"
	}
	return "ok"
}

func assertSharedVerdicts(t *testing.T, findings []Finding, expect []sharedFixtureVerdict) {
	t.Helper()
	if len(findings) < len(expect) {
		t.Fatalf("got %d findings, the fixture expects %d", len(findings), len(expect))
	}
	for i, want := range expect {
		got := findings[i]
		if string(got.Setting) != want.Setting {
			t.Errorf("finding %d is %q, the fixture expects %q in that place", i, got.Setting, want.Setting)
			continue
		}
		if v := sharedVerdict(got); v != want.Verdict {
			t.Errorf("%s: verdict %q (found %q), the fixture expects %q", want.Setting, v, got.Found, want.Verdict)
		}
		if got.Unwaivable != want.Unwaivable {
			t.Errorf("%s: unwaivable %v, the fixture expects %v", want.Setting, got.Unwaivable, want.Unwaivable)
		}
	}
}

// ---- S3 ------------------------------------------------------------------

// sharedS3API answers the three reads from one fixture case's input.
type sharedS3API struct {
	t     *testing.T
	input map[string]json.RawMessage
}

func (a sharedS3API) read(key string, out any) error {
	raw, ok := a.input[key]
	if !ok {
		a.t.Fatalf("the fixture case has no %q input", key)
	}
	var e sharedFixtureError
	if err := json.Unmarshal(raw, &e); err == nil && e.Error != nil {
		var err error = &smithy.GenericAPIError{Code: e.Error.Code}
		if e.Error.Code == "" {
			err = &smithyhttp.ResponseError{Response: &smithyhttp.Response{Response: &http.Response{StatusCode: e.Error.Status}}}
		}
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		a.t.Fatalf("decoding %q: %v", key, err)
	}
	return nil
}

func (a sharedS3API) GetBucketVersioning(_ context.Context, _ *s3.GetBucketVersioningInput, _ ...func(*s3.Options)) (*s3.GetBucketVersioningOutput, error) {
	out := &s3.GetBucketVersioningOutput{}
	return out, a.read("versioning", out)
}

func (a sharedS3API) GetBucketLifecycleConfiguration(_ context.Context, _ *s3.GetBucketLifecycleConfigurationInput, _ ...func(*s3.Options)) (*s3.GetBucketLifecycleConfigurationOutput, error) {
	out := &s3.GetBucketLifecycleConfigurationOutput{}
	return out, a.read("lifecycle", out)
}

func (a sharedS3API) GetPublicAccessBlock(_ context.Context, _ *s3.GetPublicAccessBlockInput, _ ...func(*s3.Options)) (*s3.GetPublicAccessBlockOutput, error) {
	out := &s3.GetPublicAccessBlockOutput{}
	return out, a.read("publicAccessBlock", out)
}

func TestSharedBucketFixturesS3(t *testing.T) {
	f := loadSharedFixtures(t, "s3.json")
	for _, tc := range f.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			findings, err := CheckBucketContract(context.Background(), sharedS3API{t: t, input: tc.Input}, tc.Bucket, "", tc.Namespaces)
			if err != nil {
				t.Fatalf("CheckBucketContract: %v", err)
			}
			assertSharedVerdicts(t, findings, tc.Expect)
		})
	}
}

// ---- GCS -----------------------------------------------------------------

func TestSharedBucketFixturesGCS(t *testing.T) {
	f := loadSharedFixtures(t, "gcs.json")
	for _, tc := range f.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			server, fake := newFakeGCSServer(t)
			whole, err := json.Marshal(tc.Input)
			if err != nil {
				t.Fatal(err)
			}
			var e sharedFixtureError
			if err := json.Unmarshal(whole, &e); err == nil && e.Error != nil {
				if e.Error.Status != http.StatusForbidden {
					t.Fatalf("the fixture's error status %d has no fake here; only 403 does", e.Error.Status)
				}
				fake.forbidden = true
			} else {
				var attrs map[string]any
				if err := json.Unmarshal(whole, &attrs); err != nil {
					t.Fatal(err)
				}
				fake.bucketAttrs = attrs
			}
			// The fake serves one bucket by name; the fixture's own name
			// decides nothing about a verdict.
			findings, err := CheckGCSBucketContract(context.Background(), newFakeGCSClient(t, server), "test-bucket", tc.Namespaces)
			if err != nil {
				t.Fatalf("CheckGCSBucketContract: %v", err)
			}
			assertSharedVerdicts(t, findings, tc.Expect)
		})
	}
}

// ---- Azure ---------------------------------------------------------------

// sharedAzureAPI answers the four ARM reads from one fixture case's input. A
// document the case leaves out is the empty one ARM would never send; a
// check that needs it reads zero values.
type sharedAzureAPI struct {
	t     *testing.T
	input map[string]json.RawMessage
}

func (a sharedAzureAPI) read(key string, out any) error {
	raw, ok := a.input[key]
	if !ok {
		return nil
	}
	var e sharedFixtureError
	if err := json.Unmarshal(raw, &e); err == nil && e.Error != nil {
		return &azcore.ResponseError{StatusCode: e.Error.Status, ErrorCode: e.Error.Code}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		a.t.Fatalf("decoding %q: %v", key, err)
	}
	return nil
}

func (a sharedAzureAPI) BlobServiceProperties(context.Context) (armstorage.BlobServiceProperties, error) {
	var out armstorage.BlobServiceProperties
	return out, a.read("blobServices", &out)
}

func (a sharedAzureAPI) ManagementPolicy(context.Context) (armstorage.ManagementPolicy, error) {
	var out armstorage.ManagementPolicy
	return out, a.read("managementPolicy", &out)
}

func (a sharedAzureAPI) Account(context.Context) (armstorage.Account, error) {
	var out armstorage.Account
	return out, a.read("account", &out)
}

func (a sharedAzureAPI) Container(context.Context) (armstorage.BlobContainer, error) {
	var out armstorage.BlobContainer
	return out, a.read("container", &out)
}

func TestSharedBucketFixturesAzure(t *testing.T) {
	f := loadSharedFixtures(t, "azure.json")
	for _, tc := range f.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			findings, err := CheckAzureContract(context.Background(), sharedAzureAPI{t: t, input: tc.Input}, tc.Container, tc.Namespaces)
			if err != nil {
				t.Fatalf("CheckAzureContract: %v", err)
			}
			assertSharedVerdicts(t, findings, tc.Expect)
		})
	}
}
