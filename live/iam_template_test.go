// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const (
	iamRenderer = "../examples/record-store-bucket/iam/render-policy.sh"
	iamExamples = "../examples/record-store-bucket/iam"
	iamDocsPage = "../site/content/docs/use/iam.md"
	iamBucket   = "choudoufu-records-111122223333-us-east-2"
	iamKMSKey   = "arn:aws:kms:us-east-2:111122223333:key/1234abcd-12ab-34cd-56ef-1234567890ab"
)

func renderIAMPolicy(t *testing.T, args ...string) []byte {
	t.Helper()
	for _, bin := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("%s is not on PATH; this runs the real renderer and must not be skipped (a skipping guard is permanently green)", bin)
		}
	}
	out, err := exec.Command("bash", append([]string{iamRenderer}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("render-policy.sh %v: %v\n%s", args, err, out)
	}
	return out
}

// TestIAMTemplateHasOneSource is GitHub issue #1342's last acceptance item:
// the renderer is the single source. The committed examples are its output,
// and the documentation page shows the first of them byte for byte, so a
// reader who copies the page copies what the script would have given them.
func TestIAMTemplateHasOneSource(t *testing.T) {
	for file, args := range map[string][]string{
		"example-prod.json":                         {"prod", iamBucket},
		"example-prod-with-key-and-dependency.json": {"prod", iamBucket, "--kms", iamKMSKey, "--reads-outputs-of", "network"},
	} {
		want, err := os.ReadFile(filepath.Join(iamExamples, file))
		if err != nil {
			t.Fatal(err)
		}
		if got := renderIAMPolicy(t, args...); !bytes.Equal(got, want) {
			t.Errorf("%s is not what render-policy.sh %s prints; re-render it", file, strings.Join(args, " "))
		}
	}

	page, err := os.ReadFile(iamDocsPage)
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile("(?s)```json\n(.*?)\n```").FindSubmatch(page)
	if block == nil {
		t.Fatalf("%s shows no ```json block", iamDocsPage)
	}
	want, _ := os.ReadFile(filepath.Join(iamExamples, "example-prod.json"))
	if !bytes.Equal(append(block[1], '\n'), want) {
		t.Errorf("the policy on %s differs from example-prod.json: the page is what people copy, and it must be the renderer's output", iamDocsPage)
	}
}

type iamStatement struct {
	Sid       string
	Effect    string
	Action    any
	Resource  any
	Condition map[string]map[string]any
}

func (s iamStatement) actions() []string { return anyStrings(s.Action) }

func anyStrings(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		var out []string
		for _, e := range x {
			out = append(out, e.(string))
		}
		return out
	}
	return nil
}

// TestIAMTemplateKeepsTheMeasuredShape holds the policy to what was measured
// against real AWS on #1342. Each assertion is a way this policy has already
// been wrong, in the epic's first draft or in the documentation it would
// have been copied from, and each one bricks an estate while reviewing
// correctly.
func TestIAMTemplateKeepsTheMeasuredShape(t *testing.T) {
	var doc struct{ Statement []iamStatement }
	if err := json.Unmarshal(renderIAMPolicy(t, "prod", iamBucket, "--reads-outputs-of", "network"), &doc); err != nil {
		t.Fatal(err)
	}
	has := func(list []string, want string) bool {
		for _, e := range list {
			if e == want {
				return true
			}
		}
		return false
	}
	var sawList, sawWrite, sawDeny bool
	for _, st := range doc.Statement {
		acts := st.actions()
		for op, keys := range st.Condition {
			for key := range keys {
				// An ALLOW conditioned on the existing tag: deletes are denied
				// outright (the key does not work on DeleteObject) and every
				// If-Match write is denied (its GetObject check carries no tags).
				if st.Effect == "Allow" && strings.HasPrefix(key, "s3:ExistingObjectTag/") {
					t.Errorf("%s allows on %s (%s): measured on AWS, an estate under that can create records and never update or delete one", st.Sid, key, op)
				}
				if strings.HasSuffix(op, "IfExists") && strings.Contains(key, "ObjectTag/") {
					t.Errorf("%s uses %s on %s", st.Sid, op, key)
				}
			}
		}
		if has(acts, "s3:GetObject") && has(acts, "s3:DeleteObject") && st.Effect == "Allow" && len(st.Condition) != 0 {
			t.Errorf("%s: the read-and-delete allow carries a condition; it has to be scoped by prefix alone", st.Sid)
		}
		if has(acts, "s3:PutObject") && st.Effect == "Allow" {
			sawWrite = true
			if !has(acts, "s3:PutObjectTagging") {
				t.Errorf("%s allows s3:PutObject without s3:PutObjectTagging: every write carries tags, and AWS denies a tagged PutObject without it", st.Sid)
			}
			if st.Condition["StringEquals"]["s3:RequestObjectTag/tofu-estate"] != "prod" {
				t.Errorf("%s does not require s3:RequestObjectTag/tofu-estate = prod", st.Sid)
			}
		}
		if has(acts, "s3:ListBucket") {
			sawList = true
			prefixes := anyStrings(st.Condition["StringLike"]["s3:prefix"])
			if len(prefixes) != 4 {
				t.Errorf("%s lists %d prefixes, want the estate's three and the dependency's outputs: %v", st.Sid, len(prefixes), prefixes)
			}
			for _, p := range prefixes {
				if !strings.HasSuffix(p, "/*") {
					t.Errorf("%s: prefix %q does not end in \"/*\"; without the slash \"prod\" is also \"prod-eu\" (#1335)", st.Sid, p)
				}
			}
		}
		if st.Effect == "Deny" {
			sawDeny = true
			if st.Condition["Null"]["s3:ExistingObjectTag/tofu-estate"] != "false" {
				t.Errorf("%s has no Null:false guard, so it fires where the tag is absent from the request context, which is every If-Match write", st.Sid)
			}
			if got := anyStrings(st.Condition["StringNotEquals"]["s3:ExistingObjectTag/tofu-estate"]); !has(got, "prod") || !has(got, "network") {
				t.Errorf("%s accepts tags %v, want this estate's and its declared dependency's", st.Sid, got)
			}
			if has(acts, "s3:DeleteObject") || has(acts, "s3:PutObject") {
				t.Errorf("%s names a write or a delete: AWS supplies no existing-object tag for those, so the statement would claim a defence that does not exist", st.Sid)
			}
		}
	}
	if !sawList {
		t.Error("no s3:ListBucket statement: besides listing, it is what makes a key that does not exist answer 404 rather than AccessDenied")
	}
	if !sawWrite || !sawDeny {
		t.Errorf("write statement present: %v, deny statement present: %v", sawWrite, sawDeny)
	}
}

// TestIAMRendererRefusesWhatIsNotAnEstateName: for a list, a write and a
// delete the prefix is the whole defence, so a "*" or a "/" must not reach a
// Resource ARN through an argument.
func TestIAMRendererRefusesWhatIsNotAnEstateName(t *testing.T) {
	for _, bad := range []string{"prod*", "prod/eu", "*", "Prod", ""} {
		out, err := exec.Command("bash", iamRenderer, bad, iamBucket).CombinedOutput()
		if err == nil {
			t.Errorf("render-policy.sh accepted %q:\n%s", bad, out)
		}
	}
	if out, err := exec.Command("bash", iamRenderer, "prod", iamBucket, "--reads-outputs-of", "net*").CombinedOutput(); err == nil {
		t.Errorf("render-policy.sh accepted a dependency named net*:\n%s", out)
	}
}

const iamKeyStatementRenderer = "../examples/record-store-bucket/iam/render-key-statement.sh"

// TestKMSKeyStatementIsWhatTheRunNeeds holds the key policy statement the
// project ships (GitHub issue #1345) to what smoke claim 37 measured on real
// AWS: the two actions S3 makes on a caller's behalf, for named principals
// only. The committed example is the renderer's output, and the actions are
// the same two render-policy.sh --kms grants the role, since either half
// without the other is a refusal.
func TestKMSKeyStatementIsWhatTheRunNeeds(t *testing.T) {
	args := []string{"arn:aws:iam::111122223333:role/prod-estate", "arn:aws:iam::111122223333:role/records-operator"}
	got, err := exec.Command("bash", append([]string{iamKeyStatementRenderer}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("render-key-statement.sh: %v\n%s", err, got)
	}
	want, err := os.ReadFile(filepath.Join(iamExamples, "example-key-statement.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("example-key-statement.json is not what render-key-statement.sh prints; re-render it")
	}

	var st struct {
		Effect    string
		Principal struct{ AWS []string }
		Action    []string
	}
	if err := json.Unmarshal(got, &st); err != nil {
		t.Fatalf("the statement is not JSON: %v\n%s", err, got)
	}
	if st.Effect != "Allow" || strings.Join(st.Principal.AWS, " ") != strings.Join(args, " ") {
		t.Errorf("effect %q, principals %v", st.Effect, st.Principal.AWS)
	}

	var rolePolicy struct{ Statement []iamStatement }
	if err := json.Unmarshal(renderIAMPolicy(t, "prod", iamBucket, "--kms", iamKMSKey), &rolePolicy); err != nil {
		t.Fatal(err)
	}
	var roleKMS []string
	for _, s := range rolePolicy.Statement {
		for _, a := range s.actions() {
			if strings.HasPrefix(a, "kms:") {
				roleKMS = append(roleKMS, a)
			}
		}
	}
	sort.Strings(roleKMS)
	keyKMS := append([]string(nil), st.Action...)
	sort.Strings(keyKMS)
	if len(keyKMS) == 0 || strings.Join(keyKMS, " ") != strings.Join(roleKMS, " ") {
		t.Errorf("the key statement allows %v and the role's policy allows %v; they are two halves of one grant and must name the same actions", keyKMS, roleKMS)
	}
}

// TestKMSKeyStatementNamesPrincipals: the account root or a wildcard as the
// principal hands the key to every IAM policy in the account.
func TestKMSKeyStatementNamesPrincipals(t *testing.T) {
	for _, bad := range []string{"arn:aws:iam::111122223333:root", "*", "arn:aws:iam::111122223333:role/*", "111122223333", "arn:aws:sts::111122223333:assumed-role/x/y", ""} {
		if out, err := exec.Command("bash", iamKeyStatementRenderer, bad).CombinedOutput(); err == nil {
			t.Errorf("render-key-statement.sh accepted %q:\n%s", bad, out)
		}
	}
	if out, err := exec.Command("bash", iamKeyStatementRenderer).CombinedOutput(); err == nil {
		t.Errorf("render-key-statement.sh printed a statement naming nobody:\n%s", out)
	}
}
