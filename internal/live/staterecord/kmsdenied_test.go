// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// The message real AWS sent when a key policy omitted the estate's role
// (GitHub issue #1345, us-east-2, 2026-09-18), account and key id replaced.
const realKMSDenial = `User: arn:aws:sts::111122223333:assumed-role/smoke-secure-estate/smoke is not authorized to perform: %s on resource: arn:aws:kms:us-east-2:111122223333:key/11111111-2222-3333-4444-555555555555 because no %s policy allows the %s action`

// deniedS3Store is an S3 store whose every request is answered 403
// AccessDenied with the given message, the way S3 relays a refusal.
func deniedS3Store(t *testing.T, message string) *S3Store {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>AccessDenied</Code><Message>%s</Message></Error>`, message)
	}))
	t.Cleanup(server.Close)
	client := s3.NewFromConfig(aws.Config{
		Region:           "us-east-1",
		Credentials:      credentials.NewStaticCredentialsProvider("test", "test", ""),
		RetryMaxAttempts: 1,
	}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(server.URL)
		o.UsePathStyle = true
	})
	store, err := NewS3Store(S3Config{Client: client, Bucket: "test-bucket"})
	if err != nil {
		t.Fatalf("NewS3Store: %v", err)
	}
	return store
}

// A KMS refusal is named as one on every operation that can meet it, with
// the key, the action, who was refused, and which policy to go and read.
func TestS3StoreNamesAKMSDenial(t *testing.T) {
	ctx := context.Background()
	const key = "arn:aws:kms:us-east-2:111122223333:key/11111111-2222-3333-4444-555555555555"

	ops := map[string]func(*S3Store) error{
		"get":    func(s *S3Store) error { _, _, _, err := s.Get(ctx, "a/b"); return err },
		"create": func(s *S3Store) error { _, err := s.PutIfAbsent(ctx, "a/b", []byte("x")); return err },
		"update": func(s *S3Store) error { _, err := s.PutIfVersion(ctx, "a/b", []byte("x"), `"v"`); return err },
		"delete": func(s *S3Store) error { return s.Delete(ctx, "a/b", `"v"`) },
	}
	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			store := deniedS3Store(t, fmt.Sprintf(realKMSDenial, "kms:GenerateDataKey", "resource-based", "kms:GenerateDataKey"))
			err := op(store)
			var denied *KMSDeniedError
			if !errors.As(err, &denied) {
				t.Fatalf("not reported as a KMS denial: %v", err)
			}
			if denied.Action != "kms:GenerateDataKey" || denied.KeyARN != key || denied.Where != KMSDeniedByKeyPolicy {
				t.Errorf("got action %q key %q where %q", denied.Action, denied.KeyARN, denied.Where)
			}
			if !strings.Contains(denied.Principal, "assumed-role/smoke-secure-estate") {
				t.Errorf("principal %q does not name the role", denied.Principal)
			}
			for _, want := range []string{"KMS key " + key, "key policy", "kms:Decrypt and kms:GenerateDataKey", "AccessDenied"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the message does not say %q:\n%s", want, err)
				}
			}
		})
	}
}

// Which policy AWS blamed decides where the operator is sent. Sending someone
// with a missing IAM statement to the key policy is its own lost afternoon.
func TestKMSDenialRemedyFollowsThePolicyBlamed(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name, message, where, says, neverSays string
	}{
		{"key policy", fmt.Sprintf(realKMSDenial, "kms:Decrypt", "resource-based", "kms:Decrypt"), KMSDeniedByKeyPolicy, "Add the estate's role to the key policy", "--kms"},
		{"identity policy", fmt.Sprintf(realKMSDenial, "kms:Decrypt", "identity-based", "kms:Decrypt"), KMSDeniedByIdentityPolicy, "--kms", "Add the estate's role to the key policy"},
		{"explicit deny", `User: arn:aws:sts::111122223333:assumed-role/r/s is not authorized to perform: kms:Decrypt on resource: arn:aws:kms:us-east-2:111122223333:key/k with an explicit deny in a resource-based policy`, KMSDeniedExplicitly, "denies it explicitly", "--kms"},
		{"unattributed", `User: arn:aws:sts::111122223333:assumed-role/r/s is not authorized to perform: kms:Decrypt on resource: arn:aws:kms:us-east-2:111122223333:key/k`, "", "Check the key policy first", "--kms"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := deniedS3Store(t, tc.message).Get(ctx, "a/b")
			var denied *KMSDeniedError
			if !errors.As(err, &denied) {
				t.Fatalf("not reported as a KMS denial: %v", err)
			}
			if denied.Where != tc.where {
				t.Errorf("where = %q, want %q", denied.Where, tc.where)
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("does not say %q:\n%s", tc.says, err)
			}
			if strings.Contains(err.Error(), tc.neverSays) {
				t.Errorf("says %q, which points at the wrong policy:\n%s", tc.neverSays, err)
			}
		})
	}
}

// An AccessDenied that is about S3 must not be blamed on a key. Most buckets
// have no customer managed key, and naming one there would be the same wrong
// turn in the other direction.
func TestAnS3DenialIsNotBlamedOnAKey(t *testing.T) {
	for _, message := range []string{
		`User: arn:aws:sts::111122223333:assumed-role/r/s is not authorized to perform: s3:GetObject on resource: "arn:aws:s3:::b/tofu-records/e/k" because no identity-based policy allows the s3:GetObject action`,
		`Access Denied`,
	} {
		_, _, _, err := deniedS3Store(t, message).Get(context.Background(), "a/b")
		if err == nil {
			t.Fatal("a 403 was not an error")
		}
		var denied *KMSDeniedError
		if errors.As(err, &denied) || strings.Contains(err.Error(), "KMS") {
			t.Errorf("an S3 denial was blamed on a KMS key:\n%s", err)
		}
	}
}

// The bulk read is its own GetObject call site and the first request of most
// runs, so it is the one most likely to meet the refusal.
func TestS3GetAllNamesAKMSDenial(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("list-type") == "2" {
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><ListBucketResult><IsTruncated>false</IsTruncated><Contents><Key>ns/one</Key></Contents></ListBucketResult>`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>AccessDenied</Code><Message>%s</Message></Error>`,
			fmt.Sprintf(realKMSDenial, "kms:Decrypt", "resource-based", "kms:Decrypt"))
	}))
	t.Cleanup(server.Close)
	client := s3.NewFromConfig(aws.Config{
		Region:           "us-east-1",
		Credentials:      credentials.NewStaticCredentialsProvider("test", "test", ""),
		RetryMaxAttempts: 1,
	}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(server.URL)
		o.UsePathStyle = true
	})
	store, err := NewS3Store(S3Config{Client: client, Bucket: "test-bucket"})
	if err != nil {
		t.Fatalf("NewS3Store: %v", err)
	}
	_, err = store.GetAll(context.Background(), "ns/")
	var denied *KMSDeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("the bulk read did not report a KMS denial: %v", err)
	}
	if denied.Action != "kms:Decrypt" {
		t.Errorf("action = %q", denied.Action)
	}
}

// AWS quotes the resource in some of these messages and not in others. An
// unquoted pattern swallowed the closing quote into KeyARN, which left the
// operator holding a string that matches no key anywhere.
func TestTheKeyARNSurvivesAWSQuotingTheResource(t *testing.T) {
	const key = "arn:aws:kms:us-east-2:111122223333:key/11111111-2222-3333-4444-555555555555"
	for _, message := range []string{
		`User: arn:aws:sts::111122223333:assumed-role/r/s is not authorized to perform: kms:Decrypt on resource: ` + key + ` because no resource-based policy allows the kms:Decrypt action`,
		`User: "arn:aws:sts::111122223333:assumed-role/r/s" is not authorized to perform: kms:Decrypt on resource: "` + key + `" because no resource-based policy allows the kms:Decrypt action`,
	} {
		_, _, _, err := deniedS3Store(t, message).Get(context.Background(), "a/b")
		var denied *KMSDeniedError
		if !errors.As(err, &denied) {
			t.Fatalf("not reported as a KMS denial: %v", err)
		}
		if denied.KeyARN != key {
			t.Errorf("KeyARN = %q, want %q", denied.KeyARN, key)
		}
		if !strings.Contains(denied.Principal, "assumed-role/r/s") || strings.Contains(denied.Principal, `"`) {
			t.Errorf("Principal = %q", denied.Principal)
		}
	}
}

// unusableKeyS3Store answers every request the way S3 relays a KMS exception:
// a 400 carrying the KMS error code under its "KMS." prefix.
func unusableKeyS3Store(t *testing.T, code, message string) *S3Store {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code><Message>%s</Message></Error>`, code, message)
	}))
	t.Cleanup(server.Close)
	client := s3.NewFromConfig(aws.Config{
		Region:           "us-east-1",
		Credentials:      credentials.NewStaticCredentialsProvider("test", "test", ""),
		RetryMaxAttempts: 1,
	}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(server.URL)
		o.UsePathStyle = true
	})
	store, err := NewS3Store(S3Config{Client: client, Bucket: "test-bucket"})
	if err != nil {
		t.Fatalf("NewS3Store: %v", err)
	}
	return store
}

// A key that is disabled, pending deletion or gone is not a denial, and every
// remedy KMSDeniedError offers is a policy edit that would not move it. These
// get their own error and their own advice. GitHub issue #1383.
func TestAKMSKeyThatCannotBeUsedIsNotReportedAsADenial(t *testing.T) {
	const key = "arn:aws:kms:us-east-2:111122223333:key/11111111-2222-3333-4444-555555555555"
	for _, tc := range []struct {
		code, says, neverSays string
	}{
		{"KMS.DisabledException", "Enable it", "key policy does not allow it"},
		{"KMS.KMSInvalidStateException", "pending deletion", "key policy does not allow it"},
		{"KMS.NotFoundException", "does not exist in this account and region", "key policy does not allow it"},
		{"KMS.SomethingNewAWSAdded", "the key's own state and not a permission", "key policy does not allow it"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			store := unusableKeyS3Store(t, tc.code, key+" is not usable in its current state.")
			_, _, _, err := store.Get(context.Background(), "a/b")

			var unusable *KMSKeyUnusableError
			if !errors.As(err, &unusable) {
				t.Fatalf("not reported as a KMS key-state failure: %v", err)
			}
			if unusable.Code != tc.code {
				t.Errorf("Code = %q, want %q", unusable.Code, tc.code)
			}
			if unusable.KeyARN != key {
				t.Errorf("KeyARN = %q, want %q", unusable.KeyARN, key)
			}
			var denied *KMSDeniedError
			if errors.As(err, &denied) {
				t.Errorf("a key-state failure was reported as a denial, which sends the operator to the policies: %v", err)
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("the message does not say %q:\n%s", tc.says, err)
			}
			if strings.Contains(err.Error(), tc.neverSays) {
				t.Errorf("the message offers a denial's remedy (%q), which cannot help here:\n%s", tc.neverSays, err)
			}
		})
	}
}

// The bulk read is its own call site, the same way it is for a denial.
func TestS3GetAllNamesAKMSKeyThatCannotBeUsed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("list-type") == "2" {
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><ListBucketResult><IsTruncated>false</IsTruncated><Contents><Key>ns/one</Key></Contents></ListBucketResult>`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>KMS.DisabledException</Code><Message>disabled</Message></Error>`))
	}))
	t.Cleanup(server.Close)
	client := s3.NewFromConfig(aws.Config{
		Region:           "us-east-1",
		Credentials:      credentials.NewStaticCredentialsProvider("test", "test", ""),
		RetryMaxAttempts: 1,
	}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(server.URL)
		o.UsePathStyle = true
	})
	store, err := NewS3Store(S3Config{Client: client, Bucket: "test-bucket"})
	if err != nil {
		t.Fatalf("NewS3Store: %v", err)
	}
	_, err = store.GetAll(context.Background(), "ns/")
	var unusable *KMSKeyUnusableError
	if !errors.As(err, &unusable) {
		t.Fatalf("the bulk read did not report the key's state: %v", err)
	}
}
