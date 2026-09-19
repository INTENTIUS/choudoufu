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
	Sid         string
	Effect      string
	Action      any
	Resource    any
	NotResource any
	Condition   map[string]map[string]any
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

// iamWantStatement is one statement as this test says it must be. It is a
// separate type from iamStatement so that Condition can be omitted where the
// renderer emits none, and so that nothing this test asserts is derived from
// the code that reads the renderer's output.
type iamWantStatement struct {
	Sid         string         `json:"Sid"`
	Effect      string         `json:"Effect"`
	Action      any            `json:"Action"`
	Resource    any            `json:"Resource,omitempty"`
	NotResource any            `json:"NotResource,omitempty"`
	Condition   map[string]any `json:"Condition,omitempty"`
}

// iamWantedPolicy is the whole document render-policy.sh must print for an
// estate, a bucket, an optional key and a list of estates whose outputs it
// reads. It is written out here rather than read from a committed example,
// because #1379's audit gutted the Deny, widened the write and the delete to
// every estate, and dropped the trailing slash from the object ARNs, and
// each time re-rendered the examples as TestIAMTemplateHasOneSource's own
// message instructs - after which everything was green. An example file can
// be re-rendered; this cannot.
func iamWantedPolicy(estate, bucket, kms string, others ...string) map[string]any {
	b := "arn:aws:s3:::" + bucket
	// Each prefix ends in "/", so "prod" is not also "prod-eu" (#1335).
	own := []string{"tofu-records/" + estate + "/", "tofu-hints/" + estate + "/", "tofu-outputs/" + estate + "/"}
	var theirs []string
	for _, o := range others {
		theirs = append(theirs, "tofu-outputs/"+o+"/")
	}
	objects := func(prefixes []string) []string {
		out := []string{}
		for _, p := range prefixes {
			out = append(out, b+"/"+p+"*")
		}
		return out
	}
	listPrefixes := []string{}
	for _, p := range append(append([]string{}, own...), theirs...) {
		listPrefixes = append(listPrefixes, p+"*")
	}

	st := []iamWantStatement{{
		Sid: "ListOwnNamespaces", Effect: "Allow",
		Action: "s3:ListBucket", Resource: b,
		Condition: map[string]any{"StringLike": map[string]any{"s3:prefix": listPrefixes}},
	}, {
		Sid: "ReadAndDeleteByPrefix", Effect: "Allow",
		Action: []string{"s3:GetObject", "s3:DeleteObject"}, Resource: objects(own),
	}, {
		Sid: "WriteOnlyObjectsTaggedAsThisEstate", Effect: "Allow",
		Action: []string{"s3:PutObject", "s3:PutObjectTagging"}, Resource: objects(own),
		Condition: map[string]any{"StringEquals": map[string]any{"s3:RequestObjectTag/tofu-estate": estate}},
	}}
	if len(theirs) > 0 {
		st = append(st, iamWantStatement{
			Sid: "ReadDeclaredDependenciesOutputs", Effect: "Allow",
			Action: "s3:GetObject", Resource: objects(theirs),
		})
	}
	reads := []string{"s3:GetObject", "s3:GetObjectVersion", "s3:GetObjectTagging", "s3:GetObjectVersionTagging", "s3:GetObjectAcl", "s3:GetObjectVersionAcl"}
	foreignTag := func(accepted any) map[string]any {
		return map[string]any{
			"StringNotEquals": map[string]any{"s3:ExistingObjectTag/tofu-estate": accepted},
			"Null":            map[string]any{"s3:ExistingObjectTag/tofu-estate": "false"},
		}
	}
	// The read Deny accepts this estate's tag and no other. With a declared
	// dependency it steps around that estate's outputs prefix, and a second
	// Deny accepts the dependency's tag there and nowhere else (#1381: it
	// used to be accepted bucket-wide, which left the dependency's RECORDS
	// on the prefix alone).
	denyRead := iamWantStatement{Sid: "DenyReadingAnotherEstatesObjects", Effect: "Deny", Action: reads, Condition: foreignTag(estate)}
	if len(theirs) > 0 {
		denyRead.NotResource = objects(theirs)
	} else {
		denyRead.Resource = b + "/*"
	}
	st = append(st, denyRead)
	if len(theirs) > 0 {
		st = append(st, iamWantStatement{
			Sid: "DenyReadingOtherTagsUnderDeclaredOutputs", Effect: "Deny", Action: reads,
			Resource: objects(theirs), Condition: foreignTag(append([]string{estate}, others...)),
		})
	}
	// Measured on real AWS (#1381): without this, a role whose prefix was
	// widened by mistake relabels a neighbour's object and then reads it.
	st = append(st, iamWantStatement{
		Sid: "DenyRelabellingAnotherEstatesObjects", Effect: "Deny",
		Action:   []string{"s3:PutObjectTagging", "s3:DeleteObjectTagging", "s3:PutObjectVersionTagging", "s3:DeleteObjectVersionTagging"},
		Resource: b + "/*", Condition: foreignTag(estate),
	}, iamWantStatement{
		Sid: "ReadTheBucketsAssertedSettings", Effect: "Allow",
		Action:   []string{"s3:GetBucketVersioning", "s3:GetLifecycleConfiguration", "s3:GetBucketPublicAccessBlock"},
		Resource: b,
	})
	if kms != "" {
		st = append(st, iamWantStatement{
			Sid: "UseTheBucketsKey", Effect: "Allow",
			Action: []string{"kms:Decrypt", "kms:GenerateDataKey"}, Resource: kms,
		})
	}
	return map[string]any{"Version": "2012-10-17", "Statement": st}
}

// iamCanonical re-encodes JSON with sorted keys, so two documents compare as
// text. It decodes into any rather than into a struct: a struct would drop a
// field the renderer grew and the comparison would not see it.
func iamCanonical(t *testing.T, what string, raw []byte) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%s is not JSON: %v\n%s", what, err, raw)
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	return string(out)
}

// TestIAMTemplateIsExactlyThisPolicy pins every statement of all three
// renders. Any change to the renderer has to change this test deliberately,
// which is the point: the policy was measured against real AWS on #1342 and
// the examples it is compared against elsewhere are its own output.
func TestIAMTemplateIsExactlyThisPolicy(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want map[string]any
	}{
		{"no flags", []string{"prod", iamBucket}, iamWantedPolicy("prod", iamBucket, "")},
		{"--kms", []string{"prod", iamBucket, "--kms", iamKMSKey}, iamWantedPolicy("prod", iamBucket, iamKMSKey)},
		{"--reads-outputs-of", []string{"prod", iamBucket, "--reads-outputs-of", "network"}, iamWantedPolicy("prod", iamBucket, "", "network")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantRaw, err := json.Marshal(tc.want)
			if err != nil {
				t.Fatal(err)
			}
			want := iamCanonical(t, "the expected policy", wantRaw)
			got := iamCanonical(t, "render-policy.sh "+strings.Join(tc.args, " "), renderIAMPolicy(t, tc.args...))
			if got != want {
				t.Errorf("render-policy.sh %s does not print the measured policy.\nwant:\n%s\ngot:\n%s", strings.Join(tc.args, " "), want, got)
			}
		})
	}
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
	// The Resource of every statement, spelled out. The audit widened the
	// write and the delete from this estate's three prefixes to every
	// estate's, and separately dropped the trailing slash so that "prod" also
	// matched "prod-eu", and this test had nothing to say about either.
	b := "arn:aws:s3:::" + iamBucket
	own := []string{b + "/tofu-records/prod/*", b + "/tofu-hints/prod/*", b + "/tofu-outputs/prod/*"}
	wantResource := map[string][]string{
		"ListOwnNamespaces":                        {b},
		"ReadAndDeleteByPrefix":                    own,
		"WriteOnlyObjectsTaggedAsThisEstate":       own,
		"ReadDeclaredDependenciesOutputs":          {b + "/tofu-outputs/network/*"},
		"DenyReadingOtherTagsUnderDeclaredOutputs": {b + "/tofu-outputs/network/*"},
		"DenyRelabellingAnotherEstatesObjects":     {b + "/*"},
		"ReadTheBucketsAssertedSettings":           {b},
	}
	// The read Deny covers the whole bucket EXCEPT the declared dependency's
	// outputs, where a second Deny accepts that estate's tag. So it is pinned
	// by what it steps around, and it must have no Resource at all.
	wantNotResource := map[string][]string{
		"DenyReadingAnotherEstatesObjects": {b + "/tofu-outputs/network/*"},
	}
	// Which tags each Deny accepts, exactly. The dependency's tag is accepted
	// under its outputs prefix and nowhere else: accepted bucket-wide, it
	// left that estate's records on the prefix alone (#1381).
	wantAccepted := map[string][]string{
		"DenyReadingAnotherEstatesObjects":         {"prod"},
		"DenyReadingOtherTagsUnderDeclaredOutputs": {"prod", "network"},
		"DenyRelabellingAnotherEstatesObjects":     {"prod"},
	}
	// What each Deny must name. The two read Denies are the cross-estate
	// boundary for a read. The relabel Deny is what makes the tag worth
	// trusting: measured on real AWS, without it a role whose prefix was
	// widened by mistake retags a neighbour's object and then reads it.
	wantDenied := map[string][]string{
		"DenyReadingAnotherEstatesObjects":         {"s3:GetObject", "s3:GetObjectVersion", "s3:GetObjectTagging"},
		"DenyReadingOtherTagsUnderDeclaredOutputs": {"s3:GetObject", "s3:GetObjectVersion", "s3:GetObjectTagging"},
		"DenyRelabellingAnotherEstatesObjects":     {"s3:PutObjectTagging", "s3:DeleteObjectTagging"},
	}
	seen := map[string]bool{}

	var sawList, sawWrite, sawDeny bool
	for _, st := range doc.Statement {
		acts := st.actions()
		seen[st.Sid] = true
		want, known := wantResource[st.Sid]
		wantNot, knownNot := wantNotResource[st.Sid]
		switch {
		case !known && !knownNot:
			t.Errorf("%s is a statement this test knows nothing about; say here what its Resource must be", st.Sid)
		case knownNot:
			if got := anyStrings(st.NotResource); strings.Join(got, "\n") != strings.Join(wantNot, "\n") {
				t.Errorf("%s steps around\n  %v\nwant exactly\n  %v", st.Sid, got, wantNot)
			}
			if st.Resource != nil {
				t.Errorf("%s has both a Resource and a NotResource; IAM refuses that", st.Sid)
			}
		default:
			if got := anyStrings(st.Resource); strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("%s grants\n  %v\nwant exactly\n  %v", st.Sid, got, want)
			}
			if st.NotResource != nil {
				t.Errorf("%s has a NotResource nobody asked for", st.Sid)
			}
		}
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
			// The Deny is the whole of the cross-estate boundary. The audit
			// left it in place with its Action cut down to
			// s3:GetObjectTagging and its Resource narrowed to a prefix
			// nothing is written under, and the estate could then read every
			// other estate's records.
			needed, knownDeny := wantDenied[st.Sid]
			if !knownDeny {
				t.Errorf("%s is a Deny this test knows nothing about; say here which actions it must name and which tags it accepts", st.Sid)
			}
			for _, a := range needed {
				if !has(acts, a) {
					t.Errorf("%s does not deny %s; it names %v", st.Sid, a, acts)
				}
			}
			if st.Condition["Null"]["s3:ExistingObjectTag/tofu-estate"] != "false" {
				t.Errorf("%s has no Null:false guard, so it fires where the tag is absent from the request context, which is every If-Match write", st.Sid)
			}
			if got, want := anyStrings(st.Condition["StringNotEquals"]["s3:ExistingObjectTag/tofu-estate"]), wantAccepted[st.Sid]; strings.Join(got, " ") != strings.Join(want, " ") {
				t.Errorf("%s accepts tags %v, want exactly %v", st.Sid, got, want)
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
	for sid := range wantResource {
		if !seen[sid] {
			t.Errorf("%s is missing from the rendered policy", sid)
		}
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

// TestIAMRendererRefusesABucketOrKeyThatIsNotOne: the bucket and the key go
// into Resource ARNs as they are. Until GitHub issue #1381 only the estate
// names were checked, so a bucket of "*" rendered a policy for every bucket,
// "b/*" and a pasted ARN rendered nonsense with exit 0, and a bucket of
// "${aws:username}" reached the policy as a live IAM policy variable.
func TestIAMRendererRefusesABucketOrKeyThatIsNotOne(t *testing.T) {
	for _, bad := range []string{"*", "b/*", "", "arn:aws:s3:::dup", "${aws:username}", "Has Space", "UPPER", "a", "-leading", "trailing-"} {
		if out, err := exec.Command("bash", iamRenderer, "prod", bad).CombinedOutput(); err == nil {
			t.Errorf("render-policy.sh accepted the bucket %q:\n%s", bad, out)
		}
	}
	for _, bad := range []string{"*", "garbage", "arn:aws:kms:us-east-2:111122223333:key/*", "arn:aws:kms:us-east-2:111122223333:alias/mine", "arn:aws:s3:::a-bucket"} {
		if out, err := exec.Command("bash", iamRenderer, "prod", iamBucket, "--kms", bad).CombinedOutput(); err == nil {
			t.Errorf("render-policy.sh accepted --kms %q:\n%s", bad, out)
		}
	}
	// An estate that declares a dependency on itself would get its own
	// outputs listed twice and a second Deny over its own prefix.
	if out, err := exec.Command("bash", iamRenderer, "prod", iamBucket, "--reads-outputs-of", "prod").CombinedOutput(); err == nil {
		t.Errorf("render-policy.sh accepted --reads-outputs-of naming the estate itself:\n%s", out)
	}
	// And the controls: what must still be accepted, so a renderer that
	// refuses everything does not pass this test.
	for _, good := range [][]string{
		{"prod", iamBucket},
		{"prod", "my.dotted.bucket-name"},
		{"prod", iamBucket, "--kms", iamKMSKey},
		{"prod", iamBucket, "--kms", "arn:aws-us-gov:kms:us-gov-west-1:111122223333:key/1234abcd-12ab-34cd-56ef-1234567890ab"},
	} {
		if out, err := exec.Command("bash", append([]string{iamRenderer}, good...)...).CombinedOutput(); err != nil {
			t.Errorf("render-policy.sh refused %v: %v\n%s", good, err, out)
		}
	}
}
