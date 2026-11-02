// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"context"
	"net/url"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func taggedS3Store(t *testing.T, base map[string]string) (*S3Store, *fakeS3Server) {
	t.Helper()
	server, fake := newFakeS3Server(t)
	client := s3.NewFromConfig(aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
	}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(server.URL)
		o.UsePathStyle = true
	})
	store, err := NewS3Store(S3Config{Client: client, Bucket: "test-bucket", BaseTags: base})
	if err != nil {
		t.Fatalf("NewS3Store: %v", err)
	}
	return store, fake
}

// sentTags is what the fake server received for key, decoded.
func sentTags(t *testing.T, fake *fakeS3Server, key string) map[string]string {
	t.Helper()
	fake.mu.Lock()
	obj, ok := fake.objects["/test-bucket/"+key]
	fake.mu.Unlock()
	if !ok {
		t.Fatalf("no object at %q", key)
	}
	values, err := url.ParseQuery(obj.tagging)
	if err != nil {
		t.Fatalf("the Tagging value %q is not a query string: %v", obj.tagging, err)
	}
	out := map[string]string{}
	for k, v := range values {
		out[k] = v[0]
	}
	return out
}

func sameTags(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// TestS3StoreTagsEveryObjectItWrites is GitHub issue #1337's first
// acceptance item, on all three shapes of write: a create with only the
// store's base tags (the sentinel, the hint, an output), a create carrying a
// record's address, and an update - which must carry the full set again,
// because a PutObject replaces the object's tags rather than merging them.
func TestS3StoreTagsEveryObjectItWrites(t *testing.T) {
	ctx := context.Background()
	store, fake := taggedS3Store(t, map[string]string{"tofu-estate": "prod"})

	if _, err := store.PutIfAbsent(ctx, "tofu-records/prod/.store-sentinel", []byte("s")); err != nil {
		t.Fatal(err)
	}
	if got, want := sentTags(t, fake, "tofu-records/prod/.store-sentinel"), (map[string]string{"tofu-estate": "prod"}); !sameTags(got, want) {
		t.Errorf("an object with no address was tagged %v, want %v", got, want)
	}

	// A value with every character class an escaped address can carry, so
	// the encoding is exercised and not just the plumbing.
	address := "module.a:b@ck.aws_thing.x:k+1"
	recCtx := WithObjectTags(ctx, map[string]string{"tofu-address": address})
	want := map[string]string{"tofu-estate": "prod", "tofu-address": address}

	version, err := store.PutIfAbsent(recCtx, "tofu-records/prod/aws_thing/abc", []byte("v1"))
	if err != nil {
		t.Fatal(err)
	}
	if got := sentTags(t, fake, "tofu-records/prod/aws_thing/abc"); !sameTags(got, want) {
		t.Errorf("a record's create was tagged %v, want %v", got, want)
	}
	if _, err := store.PutIfVersion(recCtx, "tofu-records/prod/aws_thing/abc", []byte("v2"), version); err != nil {
		t.Fatal(err)
	}
	if got := sentTags(t, fake, "tofu-records/prod/aws_thing/abc"); !sameTags(got, want) {
		t.Errorf("a record's UPDATE was tagged %v, want %v: a PutObject replaces the tag set, so an update that sends none strips the object", got, want)
	}
}

// TestAnObjectWrittenWithoutTagsIsReadableAndTaggedByTheNextWrite is the
// fifth acceptance item. An older build wrote objects with no tags; they
// must still read, and the issue asks what happens on the next write: the
// write tags them, because every write carries the full set.
func TestAnObjectWrittenWithoutTagsIsReadableAndTaggedByTheNextWrite(t *testing.T) {
	ctx := context.Background()
	old, fake := taggedS3Store(t, nil)
	version, err := old.PutIfAbsent(ctx, "tofu-records/prod/aws_thing/old", []byte("written by an older build"))
	if err != nil {
		t.Fatal(err)
	}
	if got := sentTags(t, fake, "tofu-records/prod/aws_thing/old"); len(got) != 0 {
		t.Fatalf("the fixture is wrong: a store with no tags sent %v", got)
	}

	// The same bucket, opened by a build that tags.
	current := &S3Store{client: old.client, bucket: old.bucket, baseTags: map[string]string{"tofu-estate": "prod"}}
	payload, gotVersion, exists, err := current.Get(ctx, "tofu-records/prod/aws_thing/old")
	if err != nil || !exists || string(payload) != "written by an older build" || gotVersion != version {
		t.Fatalf("an untagged object did not read back: payload %q version %q exists %v err %v", payload, gotVersion, exists, err)
	}
	if _, err := current.PutIfVersion(WithObjectTags(ctx, map[string]string{"tofu-address": "aws_thing.old"}), "tofu-records/prod/aws_thing/old", []byte("rewritten"), version); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"tofu-estate": "prod", "tofu-address": "aws_thing.old"}
	if got := sentTags(t, fake, "tofu-records/prod/aws_thing/old"); !sameTags(got, want) {
		t.Errorf("the next write left the object tagged %v, want %v", got, want)
	}
}

// TestObjectTagsInContext: a later WithObjectTags keeps what an earlier one
// set and wins on a shared key, and a context nobody tagged carries none.
func TestObjectTagsInContext(t *testing.T) {
	if got := ObjectTags(context.Background()); got != nil {
		t.Errorf("an untagged context carries %v", got)
	}
	ctx := WithObjectTags(context.Background(), map[string]string{"a": "1", "b": "1"})
	ctx = WithObjectTags(ctx, map[string]string{"b": "2", "c": "2"})
	if got, want := ObjectTags(ctx), (map[string]string{"a": "1", "b": "2", "c": "2"}); !sameTags(got, want) {
		t.Errorf("ObjectTags = %v, want %v", got, want)
	}
	// A later set wins over an earlier one on a shared key, and the rendering
	// is the same bytes for the same tags. Which set the store passes last is
	// TestS3StoreBaseTagsWinOverTheContext's business, not this function's.
	if got, want := encodeObjectTagging(map[string]string{"k": "base", "z": "9"}, map[string]string{"k": "per write"}), "k=per+write&z=9"; got != want {
		t.Errorf("encodeObjectTagging = %q, want %q", got, want)
	}
	if got := encodeObjectTagging(nil, nil); got != "" {
		t.Errorf("no tags rendered as %q, want nothing: an empty Tagging header is not the same as none", got)
	}
}

// TestS3StoreBaseTagsWinOverTheContext pins which set the store puts last.
// store.go's contract for the estate tag is that "the tag can never name a
// different estate" than the one the store was opened for, and that is only
// true if [S3Config.BaseTags] wins on every key it defines. Per-write tags
// still carry every key BaseTags leaves alone, which is the address half.
//
// The mutation this catches: swapping the two arguments to
// encodeObjectTagging in PutIfVersion, which lets a caller's context tag
// rewrite the estate on the object and makes the IAM condition that reads
// that tag admit on the caller's word.
func TestS3StoreBaseTagsWinOverTheContext(t *testing.T) {
	store, fake := taggedS3Store(t, map[string]string{"tofu-estate": "prod"})
	ctx := WithObjectTags(context.Background(), map[string]string{
		"tofu-estate":  "somewhere-else",
		"tofu-address": "aws_thing.x",
	})
	if _, err := store.PutIfAbsent(ctx, "tofu-records/prod/aws_thing/abc", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	got := sentTags(t, fake, "tofu-records/prod/aws_thing/abc")
	if got["tofu-estate"] != "prod" {
		t.Errorf("the object went to S3 tagged tofu-estate=%q: a per-write tag overwrote the estate the store was opened for", got["tofu-estate"])
	}
	if got["tofu-address"] != "aws_thing.x" {
		t.Errorf("tofu-address = %q, want the per-write value: BaseTags must only win on keys it defines", got["tofu-address"])
	}
}
