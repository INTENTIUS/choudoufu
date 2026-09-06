// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// fakeSSMParameter is one stored parameter: its base64 value (SSMStore's
// own wire encoding) and the version this fake server assigns it.
type fakeSSMParameter struct {
	value   string
	version int64
}

// fakeSSMServer is a minimal SSM Parameter Store speaking the real AWS
// JSON 1.1 wire shape this package's client depends on: PutParameter
// (Overwrite true/false), GetParameter, DeleteParameter, and
// GetParametersByPath. It has no server-side notion of a version-scoped
// conditional write, because the real SSM API does not either — that is
// precisely the honest limitation [SSMStore]'s own doc comment documents.
type fakeSSMServer struct {
	mu     sync.Mutex
	params map[string]*fakeSSMParameter

	// interpose, when set, runs once right after a PutParameter's
	// read-side effects would be visible but before this call's own write
	// lands — the hook this test file's sequenced-race test uses to
	// simulate a second writer landing inside SSMStore's read-compare-write
	// window, deterministically rather than through real goroutines.
	interpose func()
}

func newFakeSSMServer(t *testing.T) (*httptest.Server, *fakeSSMServer) {
	t.Helper()
	f := &fakeSSMServer{params: map[string]*fakeSSMParameter{}}
	server := httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(server.Close)
	return server, f
}

func (f *fakeSSMServer) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	target := r.Header.Get("X-Amz-Target")
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)

	switch target {
	case "AmazonSSM.PutParameter":
		f.putParameter(w, body)
	case "AmazonSSM.GetParameter":
		f.getParameter(w, body)
	case "AmazonSSM.DeleteParameter":
		f.deleteParameter(w, body)
	case "AmazonSSM.GetParametersByPath":
		f.getParametersByPath(w, body)
	default:
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"__type":  "UnknownOperationException",
			"message": "unrecognized target " + target,
		})
	}
}

func (f *fakeSSMServer) writeError(w http.ResponseWriter, errType, message string) {
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"__type":  errType,
		"message": message,
	})
}

func (f *fakeSSMServer) putParameter(w http.ResponseWriter, body map[string]any) {
	name, _ := body["Name"].(string)
	value, _ := body["Value"].(string)
	overwrite, _ := body["Overwrite"].(bool)

	cur, exists := f.params[name]
	if exists && !overwrite {
		f.writeError(w, "ParameterAlreadyExists", "already exists")
		return
	}

	if f.interpose != nil {
		hook := f.interpose
		f.interpose = nil
		hook()
		// A concurrent writer may have changed cur/exists; reread.
		cur, exists = f.params[name]
	}

	newVersion := int64(1)
	if exists {
		newVersion = cur.version + 1
	}
	f.params[name] = &fakeSSMParameter{value: value, version: newVersion}
	_ = json.NewEncoder(w).Encode(map[string]any{"Version": newVersion, "Tier": "Standard"})
}

func (f *fakeSSMServer) getParameter(w http.ResponseWriter, body map[string]any) {
	name, _ := body["Name"].(string)
	cur, exists := f.params[name]
	if !exists {
		f.writeError(w, "ParameterNotFound", "not found")
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"Parameter": map[string]any{
			"Name":    name,
			"Value":   cur.value,
			"Type":    "String",
			"Version": cur.version,
		},
	})
}

func (f *fakeSSMServer) deleteParameter(w http.ResponseWriter, body map[string]any) {
	name, _ := body["Name"].(string)
	if f.interpose != nil {
		hook := f.interpose
		f.interpose = nil
		hook()
	}
	if _, exists := f.params[name]; !exists {
		f.writeError(w, "ParameterNotFound", "not found")
		return
	}
	delete(f.params, name)
	_ = json.NewEncoder(w).Encode(map[string]any{})
}

func (f *fakeSSMServer) getParametersByPath(w http.ResponseWriter, body map[string]any) {
	path, _ := body["Path"].(string)
	prefix := strings.TrimSuffix(path, "/") + "/"

	var out []map[string]any
	for name, p := range f.params {
		if strings.HasPrefix(name, prefix) {
			out = append(out, map[string]any{
				"Name":    name,
				"Value":   p.value,
				"Type":    "String",
				"Version": p.version,
			})
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"Parameters": out})
}

// newTestSSMStore builds an [SSMStore] against a fresh [fakeSSMServer].
func newTestSSMStore(t *testing.T, keyPrefix string) (Store, *fakeSSMServer) {
	t.Helper()
	server, fake := newFakeSSMServer(t)
	client := ssm.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
	}, func(o *ssm.Options) {
		o.BaseEndpoint = aws.String(server.URL)
	})
	store, err := NewSSMStore(SSMConfig{Client: client, KeyPrefix: keyPrefix})
	if err != nil {
		t.Fatalf("NewSSMStore: %v", err)
	}
	return store, fake
}

func TestSSMStoreConformance(t *testing.T) {
	runConformance(t, func(t *testing.T) Store {
		t.Helper()
		store, _ := newTestSSMStore(t, "/choudoufu-test")
		return store
	})
}

func TestSSMStoreConformanceWithNestedKeyPrefix(t *testing.T) {
	runConformance(t, func(t *testing.T) Store {
		t.Helper()
		store, _ := newTestSSMStore(t, "/choudoufu-test/one/two")
		return store
	})
}

// TestSSMStoreConformanceWithNoKeyPrefix covers the shape
// internal/live/projection now builds this store with: no prefix of its
// own, because the namespace already lives in every key it is handed
// (issue #916). It used to normalize "" to "/" and render every parameter
// name with a leading "//".
func TestSSMStoreConformanceWithNoKeyPrefix(t *testing.T) {
	runConformance(t, func(t *testing.T) Store {
		t.Helper()
		store, _ := newTestSSMStore(t, "")
		return store
	})
}

// TestSSMStoreEmptyKeyPrefixRendersAtTheHierarchyRoot asserts the rendered
// parameter name by value, which the conformance run above cannot: that
// run reads back through the same renderer it wrote through, so it agrees
// with itself whatever name it produced. "//tofu-records/..." round-trips
// perfectly through this package and is not a name real SSM will accept.
func TestSSMStoreEmptyKeyPrefixRendersAtTheHierarchyRoot(t *testing.T) {
	store, err := NewSSMStore(SSMConfig{Client: &ssm.Client{}, KeyPrefix: ""})
	if err != nil {
		t.Fatalf("NewSSMStore: %v", err)
	}
	const key = "tofu-records/prod-networking/aws_instance/YXdz"
	if got, want := store.ParameterName(key), "/"+key; got != want {
		t.Errorf("ParameterName(%q) = %q, want %q", key, got, want)
	}
}

// TestSSMStorePayloadIsBase64OnTheWire proves the binary-safety encoding
// this store's doc comment promises: the parameter's raw Value is base64,
// not the caller's literal payload, even though Get hands the literal
// payload back.
func TestSSMStorePayloadIsBase64OnTheWire(t *testing.T) {
	store, fake := newTestSSMStore(t, "/choudoufu-test")
	ctx := context.Background()

	payload := []byte{0x00, 0xff, 0x10, 'h', 'i'} // deliberately non-UTF8-safe
	if _, err := store.PutIfAbsent(ctx, "k1", payload); err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}

	fake.mu.Lock()
	raw, exists := fake.params["/choudoufu-test/k1"]
	fake.mu.Unlock()
	if !exists {
		t.Fatal("the fake server has no record of /choudoufu-test/k1")
	}
	if raw.value == string(payload) {
		t.Fatal("the wire value equals the raw payload; expected base64 encoding")
	}
	decoded, err := base64.StdEncoding.DecodeString(raw.value)
	if err != nil {
		t.Fatalf("the wire value is not valid base64: %v", err)
	}
	if string(decoded) != string(payload) {
		t.Errorf("decoded wire value = %q, want %q", decoded, payload)
	}

	got, _, exists, err := store.Get(ctx, "k1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !exists || string(got) != string(payload) {
		t.Errorf("Get returned %q (exists=%v), want %q", got, exists, payload)
	}
}

// TestSSMStorePutIfVersionDetectsRaceAfterTheFact is the sequenced-CAS-
// mismatch test the task asked for, exercising the read-compare-write
// window [SSMStore.PutIfVersion]'s doc comment describes: a second writer
// lands its own PutParameter between this store's read and its write (via
// fakeSSMServer.interpose, run deterministically inside the fake's
// handler rather than through real goroutines), and the store's
// after-the-fact version check must turn that into a *VersionConflictError
// — even though, as documented, its own write has already landed on top
// of the interposed one by the time it notices.
func TestSSMStorePutIfVersionDetectsRaceAfterTheFact(t *testing.T) {
	store, fake := newTestSSMStore(t, "/choudoufu-test")
	ctx := context.Background()

	v1, err := store.PutIfAbsent(ctx, "k1", []byte("v1"))
	if err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}

	// Arrange for a second writer's PutParameter to land in between this
	// store's read (inside PutIfVersion, via Get) and its own
	// PutParameter call.
	fake.mu.Lock()
	fake.interpose = func() {
		fake.params["/choudoufu-test/k1"] = &fakeSSMParameter{value: "aW50ZXJwb3NlZA==", version: fake.params["/choudoufu-test/k1"].version + 1} // "interposed"
	}
	fake.mu.Unlock()

	_, err = store.PutIfVersion(ctx, "k1", []byte("from-this-call"), v1)
	if !isVersionConflict(err) {
		t.Fatalf("PutIfVersion racing an interposed writer: got %v (%T), want *VersionConflictError", err, err)
	}

	// Per the documented weaker-race semantics: the write already landed
	// on top of the interposed one, clobbering it. This assertion is not
	// approval of that outcome — it is this test proving the doc comment
	// told the truth about what SSM does and does not guarantee.
	payload, _, exists, err := store.Get(ctx, "k1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !exists {
		t.Fatal("exists = false; the record should not have vanished")
	}
	if string(payload) != "from-this-call" {
		t.Errorf("payload = %q, want %q (SSM's PutIfVersion cannot prevent the clobber, only report it)", payload, "from-this-call")
	}
}

// TestSSMStoreDeleteHasNoAfterTheFactDetection documents (and proves) the
// even-weaker Delete case: a race landing inside the window between
// SSMStore.Delete's own read and its DeleteParameter call is entirely
// invisible, because DeleteParameter returns nothing to compare against
// (unlike PutParameterOutput.Version, which is what gives PutIfVersion its
// weaker but real after-the-fact detection). The fake's interpose hook
// fires exactly at the DeleteParameter call, simulating a second writer's
// update landing strictly after Delete's read-compare already passed.
func TestSSMStoreDeleteHasNoAfterTheFactDetection(t *testing.T) {
	store, fake := newTestSSMStore(t, "/choudoufu-test")
	ctx := context.Background()

	v1, err := store.PutIfAbsent(ctx, "k1", []byte("v1"))
	if err != nil {
		t.Fatalf("PutIfAbsent: %v", err)
	}

	fake.mu.Lock()
	fake.interpose = func() {
		fake.params["/choudoufu-test/k1"] = &fakeSSMParameter{
			value:   base64.StdEncoding.EncodeToString([]byte("raced-in")),
			version: fake.params["/choudoufu-test/k1"].version + 1,
		}
	}
	fake.mu.Unlock()

	// Delete's own Get reads v1 and its compare passes before the
	// interposed write ever happens; by the time its DeleteParameter call
	// reaches the fake, the race has already landed — and DeleteParameter
	// carries no version to notice it with, so the delete proceeds and
	// reports success.
	if err := store.Delete(ctx, "k1", v1); err != nil {
		t.Fatalf("Delete: got %v, want nil — DeleteParameter has no version parameter, so a race in this window is invisible to it and the delete must appear to succeed", err)
	}

	// The raced-in write is gone too: unlike PutIfVersion, which at least
	// reports the clobber after the fact, this Delete destroyed it without
	// any signal at all.
	_, _, exists, err := store.Get(ctx, "k1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if exists {
		t.Error("exists = true after Delete; want the record gone, silently, proving no after-the-fact detection exists for this case")
	}
}
