// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

func bucketFindings(failing ...staterecord.Setting) []staterecord.Finding {
	var out []staterecord.Finding
	for _, s := range staterecord.BucketSettings {
		f := staterecord.Finding{Setting: s, Outcome: staterecord.Passed, Found: "fine"}
		for _, bad := range failing {
			if bad == s {
				f = staterecord.Finding{Setting: s, Found: "wrong"}
			}
		}
		out = append(out, f)
	}
	return out
}

// TestLiveBucketReportsTheBucketNotTheConfiguration is GitHub issue #1341's
// second rule. A plan under allow_insecure = ["versioning"] proceeds; this
// report must still call a bucket with versioning off NOT correct, and name
// the waiver separately as hiding it. A verify that honoured the waiver would
// print green for exactly the bucket someone ran it to check.
func TestLiveBucketReportsTheBucketNotTheConfiguration(t *testing.T) {
	waived := &configs.LiveRecordStore{Type: "s3", Bucket: "b", AllowInsecure: []string{"versioning", "lifecycle"}}
	findings := bucketFindings(staterecord.BucketVersioning)

	for name, rs := range map[string]*configs.LiveRecordStore{"no configuration": nil, "no waiver": {Type: "s3", Bucket: "b"}, "versioning waived": waived} {
		if r := buildLiveBucketReport("b", "e", findings, rs); r.Correct {
			t.Errorf("%s: a bucket that fails versioning was reported correct", name)
		}
	}

	r := buildLiveBucketReport("b", "e", findings, waived)
	if len(r.Waived) != 2 {
		t.Fatalf("got %d waiver lines, want 2", len(r.Waived))
	}
	if !r.Waived[0].Hiding || r.Waived[0].Setting != "versioning" {
		t.Errorf("the versioning waiver is hiding a real failure and was not reported so: %+v", r.Waived[0])
	}
	if r.Waived[1].Hiding {
		t.Errorf("the lifecycle waiver hides nothing - the bucket passes it - and was reported as hiding: %+v", r.Waived[1])
	}

	text := renderLiveBucketReport(r)
	if !strings.HasSuffix(text, "bucket b: NOT correct") {
		t.Errorf("the last line is the verdict and must read NOT correct:\n%s", text)
	}
	for _, want := range []string{"versioning", "FAIL", "DOES fail it", "hiding nothing"} {
		if !strings.Contains(text, want) {
			t.Errorf("the table does not contain %q:\n%s", want, text)
		}
	}

	if ok := buildLiveBucketReport("b", "e", bucketFindings(), waived); !ok.Correct || !strings.HasSuffix(renderLiveBucketReport(ok), "bucket b: correct") {
		t.Errorf("a bucket that passes all three was not reported correct")
	}
}

// TestLiveBucketUnreadableIsNotAPass: a setting nobody could read is its own
// verdict, and it fails the bucket.
func TestLiveBucketUnreadableIsNotAPass(t *testing.T) {
	findings := bucketFindings()
	findings[2] = staterecord.Finding{Setting: staterecord.BucketPublicAccessBlock, Outcome: staterecord.Unreadable, Found: "s3:GetBucketPublicAccessBlock was denied"}
	r := buildLiveBucketReport("b", "e", findings, nil)
	if r.Correct || r.Settings[2].Verdict != "unreadable" {
		t.Errorf("an unreadable setting: correct=%v verdict=%q, want false and unreadable", r.Correct, r.Settings[2].Verdict)
	}
}

func TestParseLiveBucket(t *testing.T) {
	if _, diags := arguments.ParseLiveBucket([]string{"-bucket=b", "-estate=prod", "-region=us-east-2", "-json"}); diags.HasErrors() {
		t.Errorf("a full command line was refused: %s", diags.Err())
	}
	if _, diags := arguments.ParseLiveBucket(nil); diags.HasErrors() {
		t.Errorf("no options (read the configuration) was refused: %s", diags.Err())
	}
	if _, diags := arguments.ParseLiveBucket([]string{"-estate=prod"}); !diags.HasErrors() {
		t.Error("-estate without -bucket was accepted; it would contradict the configuration's own estate")
	}
	if _, diags := arguments.ParseLiveBucket([]string{"some-dir"}); !diags.HasErrors() {
		t.Error("a positional argument was accepted")
	}

	// GitHub issue #1381. The flag goes on the wire as ExpectedBucketOwner,
	// which IAM and S3 compare as a literal string, so anything that is not
	// twelve digits refuses all three reads with nothing saying the flag is
	// why.
	lb, diags := arguments.ParseLiveBucket([]string{"-bucket=b", "-bucket-owner=111122223333"})
	if diags.HasErrors() {
		t.Errorf("-bucket-owner with a real account id was refused: %s", diags.Err())
	}
	if lb.BucketOwner != "111122223333" {
		t.Errorf("BucketOwner = %q", lb.BucketOwner)
	}
	for _, bad := range []string{"1234-5678-9012", "11112222333", "arn:aws:iam::111122223333:root", "abcdefghijkl", "*"} {
		if _, diags := arguments.ParseLiveBucket([]string{"-bucket=b", "-bucket-owner=" + bad}); !diags.HasErrors() {
			t.Errorf("-bucket-owner=%q was accepted", bad)
		}
	}
	// The control: leaving it out is the ordinary case and checks nothing.
	if lb, diags := arguments.ParseLiveBucket([]string{"-bucket=b"}); diags.HasErrors() || lb.BucketOwner != "" {
		t.Errorf("omitting -bucket-owner: owner %q, diags %v", lb.BucketOwner, diags.Err())
	}
}

// GitHub issue #1377, the maintainer's ruling: live-bucket reports the bucket
// and says what it assumed. With no estate it credits only a lifecycle rule
// with no filter, and a run of some estate may credit more, so the two can
// disagree. The report has to say which question it answered.
func TestLiveBucketSaysWhatItAssumed(t *testing.T) {
	none := buildLiveBucketReport("b", "", bucketFindings(), nil)
	if none.CheckedAsEstate != "" {
		t.Fatalf("no estate was given and the report names %q", none.CheckedAsEstate)
	}
	text := renderLiveBucketReport(none)
	for _, want := range []string{"no estate", "no filter", "-estate=<name>"} {
		if !strings.Contains(text, want) {
			t.Errorf("the report checked with no estate and does not say %q:\n%s", want, text)
		}
	}
	// The verdict line stays last: `just verify` and the smoke claims read it
	// there.
	if !strings.HasSuffix(text, "bucket b: correct") {
		t.Errorf("the assumption line displaced the verdict line:\n%s", text)
	}

	// The control. A report checked as an estate names it, and does not
	// print the no-estate sentence, or that sentence would be true of every
	// report and say nothing.
	named := buildLiveBucketReport("b", "prod", bucketFindings(), nil)
	if named.CheckedAsEstate != "prod" {
		t.Fatalf("the report was checked as prod and names %q", named.CheckedAsEstate)
	}
	text = renderLiveBucketReport(named)
	if !strings.Contains(text, `as estate "prod"`) {
		t.Errorf("the report does not say it was checked as estate prod:\n%s", text)
	}
	if strings.Contains(text, "no estate") {
		t.Errorf("a report checked as an estate carries the no-estate sentence:\n%s", text)
	}
	if !strings.HasSuffix(text, "bucket b: correct") {
		t.Errorf("the assumption line displaced the verdict line:\n%s", text)
	}

	// An assumption is not a verdict.
	failing := buildLiveBucketReport("b", "", bucketFindings(staterecord.BucketLifecycle), nil)
	if failing.Correct {
		t.Error("a failing lifecycle reads as correct")
	}
}
