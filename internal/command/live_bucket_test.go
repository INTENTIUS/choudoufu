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

func bucketFindings(failing ...staterecord.BucketSetting) []staterecord.BucketFinding {
	var out []staterecord.BucketFinding
	for _, s := range staterecord.BucketSettings {
		f := staterecord.BucketFinding{Setting: s, OK: true, Found: "fine"}
		for _, bad := range failing {
			if bad == s {
				f = staterecord.BucketFinding{Setting: s, Found: "wrong"}
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
		if r := buildLiveBucketReport("b", findings, rs); r.Correct {
			t.Errorf("%s: a bucket that fails versioning was reported correct", name)
		}
	}

	r := buildLiveBucketReport("b", findings, waived)
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

	if ok := buildLiveBucketReport("b", bucketFindings(), waived); !ok.Correct || !strings.HasSuffix(renderLiveBucketReport(ok), "bucket b: correct") {
		t.Errorf("a bucket that passes all three was not reported correct")
	}
}

// TestLiveBucketUnreadableIsNotAPass: a setting nobody could read is its own
// verdict, and it fails the bucket.
func TestLiveBucketUnreadableIsNotAPass(t *testing.T) {
	findings := bucketFindings()
	findings[2] = staterecord.BucketFinding{Setting: staterecord.BucketPublicAccessBlock, Unreadable: true, Found: "s3:GetBucketPublicAccessBlock was denied"}
	r := buildLiveBucketReport("b", findings, nil)
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
}
