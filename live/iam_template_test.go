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

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/projection"
)

const (
	iamRenderer = "../examples/record-store-bucket/iam/render-policy.sh"
	iamExamples = "../examples/record-store-bucket/iam"
	iamDocsPage = "../examples/record-store-bucket/iam/README.md"
	iamBucket   = "choudoufu-records-111122223333-us-east-2"
	iamKMSKey   = "arn:aws:kms:us-east-2:111122223333:key/1234abcd-12ab-34cd-56ef-1234567890ab"
	// The kms:ViaService value the grant on iamKMSKey must carry (GitHub
	// issue #1381): the S3 endpoint in the key's own region, so the key is
	// usable through S3 and not directly. Written out rather than built from
	// iamKMSKey, so this test says what the endpoint is instead of repeating
	// the renderer's rule for making one.
	iamKMSViaService = "s3.us-east-2.amazonaws.com"
	// The other two partitions. GovCloud shares the ".amazonaws.com" service
	// principal suffix; China does not, and that is the whole reason the
	// suffix is a case and not a constant.
	iamGovBucket = "choudoufu-records-111122223333-us-gov-west-1"
	iamGovKMSKey = "arn:aws-us-gov:kms:us-gov-west-1:111122223333:key/1234abcd-12ab-34cd-56ef-1234567890ab"
	iamCNBucket  = "choudoufu-records-111122223333-cn-north-1"
	iamCNKMSKey  = "arn:aws-cn:kms:cn-north-1:111122223333:key/1234abcd-12ab-34cd-56ef-1234567890ab"
	// The account that must own the bucket (GitHub issue #1381). The same
	// twelve digits the bucket name above embeds, which is the ordinary case:
	// the project derives the name from the account it is standing the bucket
	// up in, and that is exactly why the name alone defends nothing.
	iamAccount = "111122223333"
)

func renderIAMPolicy(t *testing.T, args ...string) []byte {
	t.Helper()
	for _, bin := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("%s is not on PATH; this runs the real renderer and must not be skipped (a skipping guard is permanently green)", bin)
		}
	}
	// Stdout alone. A render without --account writes a warning to stderr
	// (#1381), and CombinedOutput would put that warning inside the JSON that
	// TestIAMTemplateHasOneSource compares byte for byte against the
	// documentation page.
	cmd := exec.Command("bash", append([]string{iamRenderer}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("render-policy.sh %v: %v\n%s", args, err, stderr.String())
	}
	return out
}

// TestIAMRendererWarnsWithoutAnAccount: the pin is optional, so every
// invocation written before it keeps working, and a render that leaves the
// bucket owner unpinned says so. It says it on stderr, because stdout is the
// policy and the documentation page is compared to it byte for byte.
func TestIAMRendererWarnsWithoutAnAccount(t *testing.T) {
	run := func(args ...string) (stdout, stderr string) {
		t.Helper()
		cmd := exec.Command("bash", append([]string{iamRenderer}, args...)...)
		var outBuf, errBuf bytes.Buffer
		cmd.Stdout, cmd.Stderr = &outBuf, &errBuf
		if err := cmd.Run(); err != nil {
			t.Fatalf("render-policy.sh %v: %v\n%s", args, err, errBuf.String())
		}
		return outBuf.String(), errBuf.String()
	}

	stdout, stderr := run("prod", iamBucket)
	if !strings.Contains(stderr, "--account") || !strings.Contains(stderr, "bucket name is global") {
		t.Errorf("a render with no --account said this on stderr:\n%s", stderr)
	}
	var doc any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Errorf("stdout is not JSON on its own, so the warning leaked into the policy: %v\n%s", err, stdout)
	}

	_, stderr = run("prod", iamBucket, "--account", iamAccount)
	if stderr != "" {
		t.Errorf("a render WITH --account still wrote to stderr:\n%s", stderr)
	}
}

// TestIAMTemplateHasOneSource is GitHub issue #1342's last acceptance item:
// the renderer is the single source. The committed examples are its output,
// and the documentation page shows the first of them byte for byte, so a
// reader who copies the page copies what the script would have given them.
func TestIAMTemplateHasOneSource(t *testing.T) {
	for file, args := range map[string][]string{
		"example-prod.json":                         {"prod", iamBucket},
		"example-prod-with-key-and-dependency.json": {"prod", iamBucket, "--kms", iamKMSKey, "--reads-outputs-of", "network"},
		"example-prod-with-account.json":            {"prod", iamBucket, "--account", iamAccount},
		// GitHub issue #1370: the rendering for a role that plans and never
		// applies.
		"example-prod-read-only.json": {"prod", iamBucket, "--read-only"},
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

// iamRender is one invocation of render-policy.sh, as [iamWantedPolicy] reads
// it. The zero value of each optional field is what the renderer does with
// the flag absent: the commercial partition, the default records namespace,
// no key, no owner pin, no declared dependency.
type iamRender struct {
	estate string
	bucket string

	// partition is --partition, "" meaning the renderer's default "aws".
	partition string

	// keyPrefix is --key-prefix ALREADY carrying its trailing delimiter,
	// "" meaning the default "tofu-records/<estate>/".
	keyPrefix string

	// kms is --kms, and viaService is the kms:ViaService value the grant on
	// it must require. viaService is written out rather than derived from
	// kms, so this test states the endpoint instead of repeating whatever
	// rule the renderer used to build it (#1381).
	kms        string
	viaService string

	account string
	others  []string

	// readOnly is --read-only (GitHub issue #1370): the rendering for a role
	// that plans and never applies.
	readOnly bool
}

// iamWantedPolicy is the whole document render-policy.sh must print for an
// estate, a bucket, an optional key and a list of estates whose outputs it
// reads. It is written out here rather than read from a committed example,
// because #1379's audit gutted the Deny, widened the write and the delete to
// every estate, and dropped the trailing slash from the object ARNs, and
// each time re-rendered the examples as TestIAMTemplateHasOneSource's own
// message instructs - after which everything was green. An example file can
// be re-rendered; this cannot.
func iamWantedPolicy(r iamRender) map[string]any {
	estate, bucket, kms, account, others := r.estate, r.bucket, r.kms, r.account, r.others
	partition := r.partition
	if partition == "" {
		partition = "aws"
	}
	b := "arn:" + partition + ":s3:::" + bucket
	// Each prefix ends in "/", so "prod" is not also "prod-eu" (#1335). The
	// three are projection.BucketNamespaces in order, and --key-prefix moves
	// the first of them and neither of the other two.
	records := "tofu-records/" + estate + "/"
	if r.keyPrefix != "" {
		records = r.keyPrefix
	}
	own := []string{records, "tofu-hints/" + estate + "/", "tofu-outputs/" + estate + "/"}
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
	}}
	// GitHub issue #1370. A role that plans and never applies reads the three
	// namespaces and does nothing else to them: no delete, because deleting a
	// record is what an apply does when a block goes away, and no write,
	// because the only write a plan ever attempted is the store sentinel and
	// since #1416 a denial on that write is carried past to the List rather
	// than ending the run.
	if r.readOnly {
		st = append(st, iamWantStatement{
			Sid: "ReadByPrefix", Effect: "Allow",
			Action: "s3:GetObject", Resource: objects(own),
		})
	} else {
		st = append(st, iamWantStatement{
			Sid: "ReadAndDeleteByPrefix", Effect: "Allow",
			Action: []string{"s3:GetObject", "s3:DeleteObject"}, Resource: objects(own),
		}, iamWantStatement{
			Sid: "WriteOnlyObjectsTaggedAsThisEstate", Effect: "Allow",
			Action: []string{"s3:PutObject", "s3:PutObjectTagging"}, Resource: objects(own),
			Condition: map[string]any{"StringEquals": map[string]any{"s3:RequestObjectTag/tofu-estate": estate}},
		})
	}
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
	//
	// The relabel Deny is in the read-only rendering too, although that
	// rendering allows none of the four actions it names. Same rule as the
	// three read actions no Allow grants: a Deny written for the actions of
	// the day stops covering the boundary the moment somebody widens the
	// Allow list.
	st = append(st, iamWantStatement{
		Sid: "DenyRelabellingAnotherEstatesObjects", Effect: "Deny",
		Action:   []string{"s3:PutObjectTagging", "s3:DeleteObjectTagging", "s3:PutObjectVersionTagging", "s3:DeleteObjectVersionTagging"},
		Resource: b + "/*", Condition: foreignTag(estate),
	})
	// The bucket contract is read on an estate's first contact with its store
	// and before an apply, and a read-only run is neither (#1370). Granting
	// the three reads to a role that makes neither call would be granting
	// what nothing uses.
	if !r.readOnly {
		st = append(st, iamWantStatement{
			Sid: "ReadTheBucketsAssertedSettings", Effect: "Allow",
			Action:   []string{"s3:GetBucketVersioning", "s3:GetLifecycleConfiguration", "s3:GetBucketPublicAccessBlock"},
			Resource: b,
		})
	}
	if kms != "" {
		// #1381: the grant is for S3 asking the key on this role's behalf,
		// and kms:ViaService is what says so. No encryption-context
		// condition: S3 sets that context to the bucket ARN with Bucket Keys
		// on and to the object ARN with them off, and a wrong literal denies
		// every write.
		//
		// A reader gets kms:Decrypt alone (#1370): kms:GenerateDataKey is
		// what S3 asks for on a PUT, and this role makes none.
		kmsActions := []string{"kms:Decrypt", "kms:GenerateDataKey"}
		if r.readOnly {
			kmsActions = []string{"kms:Decrypt"}
		}
		st = append(st, iamWantStatement{
			Sid: "UseTheBucketsKey", Effect: "Allow",
			Action: kmsActions, Resource: kms,
			Condition: map[string]any{"StringEquals": map[string]any{"kms:ViaService": r.viaService}},
		})
	}
	// GitHub issue #1381: with an account, every Allow also requires
	// aws:ResourceAccount, MERGED into the StringEquals it already had. IAM
	// takes one StringEquals object per statement, so a second one would
	// replace the first and the write statement would stop requiring the tag.
	// No Deny gets it: a Deny that stopped applying once the account was
	// wrong would stop applying in the case it exists for.
	if account != "" {
		for i := range st {
			if st[i].Effect != "Allow" {
				continue
			}
			cond := map[string]any{}
			for op, v := range st[i].Condition {
				cond[op] = v
			}
			eq := map[string]any{}
			if existing, ok := cond["StringEquals"].(map[string]any); ok {
				for k, v := range existing {
					eq[k] = v
				}
			}
			eq["aws:ResourceAccount"] = account
			cond["StringEquals"] = eq
			st[i].Condition = cond
		}
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
		{"no flags", []string{"prod", iamBucket},
			iamWantedPolicy(iamRender{estate: "prod", bucket: iamBucket})},
		{"--kms", []string{"prod", iamBucket, "--kms", iamKMSKey},
			iamWantedPolicy(iamRender{estate: "prod", bucket: iamBucket, kms: iamKMSKey, viaService: iamKMSViaService})},
		{"--reads-outputs-of", []string{"prod", iamBucket, "--reads-outputs-of", "network"},
			iamWantedPolicy(iamRender{estate: "prod", bucket: iamBucket, others: []string{"network"}})},
		{"--account", []string{"prod", iamBucket, "--account", iamAccount},
			iamWantedPolicy(iamRender{estate: "prod", bucket: iamBucket, account: iamAccount})},
		{"--account with everything", []string{"prod", iamBucket, "--account", iamAccount, "--kms", iamKMSKey, "--reads-outputs-of", "network"},
			iamWantedPolicy(iamRender{estate: "prod", bucket: iamBucket, kms: iamKMSKey, viaService: iamKMSViaService, account: iamAccount, others: []string{"network"}})},
		// GitHub issue #1381. The partition was the literal "aws", so an
		// estate in GovCloud or in China got a policy over resources in the
		// commercial partition, which match nothing.
		{"--partition aws-us-gov", []string{"prod", iamGovBucket, "--partition", "aws-us-gov", "--kms", iamGovKMSKey},
			iamWantedPolicy(iamRender{estate: "prod", bucket: iamGovBucket, partition: "aws-us-gov", kms: iamGovKMSKey, viaService: "s3.us-gov-west-1.amazonaws.com"})},
		// The China partition is the one whose service principal is not
		// ".amazonaws.com".
		{"--partition aws-cn", []string{"prod", iamCNBucket, "--partition", "aws-cn", "--kms", iamCNKMSKey},
			iamWantedPolicy(iamRender{estate: "prod", bucket: iamCNBucket, partition: "aws-cn", kms: iamCNKMSKey, viaService: "s3.cn-north-1.amazonaws.com.cn"})},
		// GitHub issue #1381. An estate whose record_store sets key_prefix
		// writes its records somewhere else, and the renderer could not
		// express that at all, so its first run was denied.
		{"--key-prefix", []string{"prod", iamBucket, "--key-prefix", "team/prod"},
			iamWantedPolicy(iamRender{estate: "prod", bucket: iamBucket, keyPrefix: "team/prod/"})},
		// Written without the trailing delimiter above and with it here:
		// staterecord.NamespacePrefix gives both spellings the same one
		// delimiter, and neither spelling is a mistake.
		{"--key-prefix with a trailing slash", []string{"prod", iamBucket, "--key-prefix", "team/prod/"},
			iamWantedPolicy(iamRender{estate: "prod", bucket: iamBucket, keyPrefix: "team/prod/"})},
		// GitHub issue #1370. The rendering for a role that plans and never
		// applies, and the same rendering under every other flag, because a
		// plan role in GovCloud, under a key, under a key_prefix or with a
		// declared dependency is the ordinary case and not a special one.
		{"--read-only", []string{"prod", iamBucket, "--read-only"},
			iamWantedPolicy(iamRender{estate: "prod", bucket: iamBucket, readOnly: true})},
		{"--read-only --kms", []string{"prod", iamBucket, "--read-only", "--kms", iamKMSKey},
			iamWantedPolicy(iamRender{estate: "prod", bucket: iamBucket, readOnly: true, kms: iamKMSKey, viaService: iamKMSViaService})},
		{"--read-only with everything", []string{"prod", iamBucket, "--read-only", "--account", iamAccount, "--kms", iamKMSKey, "--reads-outputs-of", "network", "--key-prefix", "team/prod"},
			iamWantedPolicy(iamRender{estate: "prod", bucket: iamBucket, readOnly: true, account: iamAccount, kms: iamKMSKey, viaService: iamKMSViaService, others: []string{"network"}, keyPrefix: "team/prod/"})},
		{"--read-only --partition aws-cn", []string{"prod", iamCNBucket, "--read-only", "--partition", "aws-cn", "--kms", iamCNKMSKey},
			iamWantedPolicy(iamRender{estate: "prod", bucket: iamCNBucket, readOnly: true, partition: "aws-cn", kms: iamCNKMSKey, viaService: "s3.cn-north-1.amazonaws.com.cn"})},
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
	// Run twice. The second render is #1381's owner pin, and every assertion
	// below has to hold with it on: adding a condition to eight Allow
	// statements is exactly the kind of change that quietens a shape test by
	// accident, and the read-and-delete assertion did have to be taught the
	// difference between its own condition and the pin.
	t.Run("no --account", func(t *testing.T) {
		iamMeasuredShape(t, "", false, "prod", iamBucket, "--reads-outputs-of", "network")
	})
	t.Run("--account", func(t *testing.T) {
		iamMeasuredShape(t, iamAccount, false, "prod", iamBucket, "--account", iamAccount, "--reads-outputs-of", "network")
	})
	// GitHub issue #1370. Every assertion below holds for the read-only
	// rendering too, and the ones that cannot (there is no write statement to
	// find) say so rather than being skipped.
	t.Run("--read-only", func(t *testing.T) {
		iamMeasuredShape(t, "", true, "prod", iamBucket, "--read-only", "--reads-outputs-of", "network")
	})
	t.Run("--read-only --account", func(t *testing.T) {
		iamMeasuredShape(t, iamAccount, true, "prod", iamBucket, "--read-only", "--account", iamAccount, "--reads-outputs-of", "network")
	})
}

// iamMeasuredShape is TestIAMTemplateKeepsTheMeasuredShape's body for one
// render. account is what every Allow must pin as aws:ResourceAccount, or ""
// when the render pinned nothing; readOnly says whether the render carried
// --read-only, which changes which statements must be there and which must
// not (#1370).
func iamMeasuredShape(t *testing.T, account string, readOnly bool, args ...string) {
	t.Helper()
	var doc struct{ Statement []iamStatement }
	if err := json.Unmarshal(renderIAMPolicy(t, args...), &doc); err != nil {
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
	// GitHub issue #1370. The read-only rendering reads the same three
	// namespaces under one statement, and the two it replaces plus the
	// bucket-configuration reads must be gone: a role that plans never
	// writes, never deletes and never asserts the bucket contract.
	if readOnly {
		delete(wantResource, "ReadAndDeleteByPrefix")
		delete(wantResource, "WriteOnlyObjectsTaggedAsThisEstate")
		delete(wantResource, "ReadTheBucketsAssertedSettings")
		wantResource["ReadByPrefix"] = own
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
	//
	// The last three of the read actions are granted by no Allow this
	// renderer writes (#1381). They are denied anyway, so that a role
	// somebody later widens with one of them still cannot use it on another
	// estate's object: a Deny written for the actions of the day is a Deny
	// that stops covering the boundary the moment the Allow list grows.
	reads := []string{"s3:GetObject", "s3:GetObjectVersion", "s3:GetObjectTagging", "s3:GetObjectVersionTagging", "s3:GetObjectAcl", "s3:GetObjectVersionAcl"}
	wantDenied := map[string][]string{
		"DenyReadingAnotherEstatesObjects":         reads,
		"DenyReadingOtherTagsUnderDeclaredOutputs": reads,
		"DenyRelabellingAnotherEstatesObjects":     {"s3:PutObjectTagging", "s3:DeleteObjectTagging"},
	}
	seen := map[string]bool{}

	var sawList, sawWrite, sawDeny bool
	var sawPin int
	for _, st := range doc.Statement {
		acts := st.actions()
		seen[st.Sid] = true

		// #1381. The pin goes on every Allow and on no Deny, and it is merged
		// into the condition a statement already had rather than replacing
		// it: IAM takes one StringEquals object per statement.
		pinned, _ := st.Condition["StringEquals"]["aws:ResourceAccount"].(string)
		elsewhere := ""
		for op, keys := range st.Condition {
			for key := range keys {
				if key == "aws:ResourceAccount" && op != "StringEquals" {
					elsewhere = op
				}
			}
		}
		if elsewhere != "" {
			t.Errorf("%s carries aws:ResourceAccount under %s; only a StringEquals pins an account", st.Sid, elsewhere)
		}
		switch {
		case account == "":
			if pinned != "" {
				t.Errorf("%s pins aws:ResourceAccount = %s with no --account given; an unasked-for account would refuse every correct bucket in any other account", st.Sid, pinned)
			}
		case st.Effect == "Allow":
			if pinned != account {
				t.Errorf("%s allows with aws:ResourceAccount = %q, want %q: an Allow without it matches a bucket of this name in any account, and the records hold secrets", st.Sid, pinned, account)
			}
			sawPin++
		default:
			if pinned != "" {
				t.Errorf("%s is a Deny that requires aws:ResourceAccount: it would stop applying the moment the account is wrong, which is the case it exists for", st.Sid)
			}
		}

		// Everything a statement conditions on BESIDES the owner pin. The
		// read-and-delete assertion below is about this, not about the pin.
		otherConditions := map[string]bool{}
		for op, keys := range st.Condition {
			for key := range keys {
				if key != "aws:ResourceAccount" {
					otherConditions[op+"/"+key] = true
				}
			}
		}
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
		if has(acts, "s3:GetObject") && has(acts, "s3:DeleteObject") && st.Effect == "Allow" && len(otherConditions) != 0 {
			t.Errorf("%s: the read-and-delete allow carries a condition besides the owner pin (%v); it has to be scoped by prefix alone", st.Sid, otherConditions)
		}
		// The read-only rendering's one read statement, held to the same
		// rule for the same measured reason (#1370): a read conditioned on
		// the existing tag is denied for every object the tag is not in the
		// request context for, so it is scoped by prefix and the tag does
		// its work as a Deny.
		if st.Sid == "ReadByPrefix" && len(otherConditions) != 0 {
			t.Errorf("%s: the read allow carries a condition besides the owner pin (%v); it has to be scoped by prefix alone", st.Sid, otherConditions)
		}
		if has(acts, "s3:PutObject") && st.Effect == "Allow" {
			sawWrite = true
			if !has(acts, "s3:PutObjectTagging") {
				t.Errorf("%s allows s3:PutObject without s3:PutObjectTagging: every write carries tags, and AWS denies a tagged PutObject without it", st.Sid)
			}
			if st.Condition["StringEquals"]["s3:RequestObjectTag/tofu-estate"] != "prod" {
				t.Errorf("%s does not require s3:RequestObjectTag/tofu-estate = prod", st.Sid)
			}
			// The merge, stated as a fact rather than implied by the line
			// above: both keys under the one StringEquals. A second
			// StringEquals object would have replaced this one and the tag
			// requirement would be gone, with the policy still valid JSON.
			if account != "" && len(st.Condition["StringEquals"]) != 2 {
				t.Errorf("%s has %d key(s) under StringEquals, want the request tag and the account together: %v", st.Sid, len(st.Condition["StringEquals"]), st.Condition["StringEquals"])
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
	switch {
	case readOnly && sawWrite:
		t.Error("the --read-only rendering allows s3:PutObject; a role that plans writes no record, and the one write a plan attempts is the store sentinel, which is denied and survived rather than granted (#1370)")
	case !readOnly && !sawWrite:
		t.Error("no write statement in the full rendering")
	}
	if !sawDeny {
		t.Error("no deny statement: the cross-estate boundary is the Deny, and it is in both renderings")
	}
	// Five Allow statements in the full rendering with a declared dependency,
	// three in the read-only one. Counted rather than assumed, so a render
	// that lost a statement does not pass by having fewer of them to pin.
	wantPins := 5
	if readOnly {
		wantPins = 3
	}
	if account != "" && sawPin < wantPins {
		t.Errorf("only %d statement(s) pin the account; this render has %d Allow statements, so something is not being read", sawPin, wantPins)
	}
	for sid := range wantResource {
		if !seen[sid] {
			t.Errorf("%s is missing from the rendered policy", sid)
		}
	}
}

// iamWritingActions are the actions that change something in the bucket: a
// record, its tags, or a version of it. GitHub issue #1370's whole point is
// that a role which plans has none of them.
//
// It is written out here rather than derived from the full rendering, so
// this test states what a write is instead of repeating whatever the
// renderer happens to grant today.
var iamWritingActions = []string{
	"s3:PutObject", "s3:PutObjectTagging", "s3:PutObjectVersionTagging",
	"s3:DeleteObject", "s3:DeleteObjectVersion", "s3:DeleteObjectTagging",
	"s3:DeleteObjectVersionTagging", "s3:AbortMultipartUpload",
	"s3:PutBucketVersioning", "s3:PutLifecycleConfiguration", "s3:PutBucketPublicAccessBlock",
	"kms:GenerateDataKey", "kms:GenerateDataKeyWithoutPlaintext", "kms:Encrypt", "kms:ReEncryptTo",
}

// iamAllowedActions is every action a rendering ALLOWS, across all of its
// Allow statements.
//
// Allow statements alone, because a Deny grants nothing: both renderings
// deny four tagging actions on another estate's objects, and the read-only
// one allows none of the four. That Deny is there for the reason the full
// rendering denies three read actions it never allows either - a Deny
// written for the actions of the day stops covering the boundary the moment
// somebody widens the Allow list - so reading it as a grant would report the
// stricter policy as the looser one.
func iamAllowedActions(t *testing.T, raw []byte) map[string]bool {
	t.Helper()
	var doc struct{ Statement []iamStatement }
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, raw)
	}
	out := map[string]bool{}
	for _, st := range doc.Statement {
		if st.Effect != "Allow" {
			continue
		}
		for _, a := range st.actions() {
			out[a] = true
		}
	}
	if len(out) == 0 {
		t.Fatalf("the rendering allows nothing at all, so a test that it allows no write proves nothing:\n%s", raw)
	}
	return out
}

// TestIAMReadOnlyRenderingAllowsNoWrite is GitHub issue #1370's acceptance in
// one line: --read-only grants nothing that writes, tags or deletes.
//
// The control below is the same render without the flag, and it exists
// because a renderer that printed an empty policy, or one whose --read-only
// silently rendered nothing at all, would pass the first half on its own.
func TestIAMReadOnlyRenderingAllowsNoWrite(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no other flags", []string{"prod", iamBucket}},
		{"--kms", []string{"prod", iamBucket, "--kms", iamKMSKey}},
		{"--account and a dependency", []string{"prod", iamBucket, "--account", iamAccount, "--reads-outputs-of", "network"}},
		{"--key-prefix", []string{"prod", iamBucket, "--key-prefix", "team/prod"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			readOnly := iamAllowedActions(t, renderIAMPolicy(t, append(append([]string{}, tc.args...), "--read-only")...))
			for _, a := range iamWritingActions {
				if readOnly[a] {
					t.Errorf("the --read-only rendering allows %s; a role that plans changes nothing in the bucket", a)
				}
			}
			// And it still reads: a policy that allowed nothing would pass
			// every line above.
			if !readOnly["s3:GetObject"] || !readOnly["s3:ListBucket"] {
				t.Errorf("the --read-only rendering does not allow both s3:GetObject and s3:ListBucket, so it cannot plan at all: %v", readOnly)
			}

			// The control. The same render without the flag has to allow the
			// writes, or the assertion above is measuring a list of actions
			// nothing ever grants.
			full := iamAllowedActions(t, renderIAMPolicy(t, tc.args...))
			for _, a := range []string{"s3:PutObject", "s3:PutObjectTagging", "s3:DeleteObject"} {
				if !full[a] {
					t.Errorf("the full rendering does not allow %s, so the --read-only assertions above are not measuring the difference between the two", a)
				}
			}
			// The key half of the same control (#1370): S3 asks for
			// kms:GenerateDataKey on a PUT, so a writer needs it and a
			// reader does not.
			if strings.Contains(strings.Join(tc.args, " "), "--kms") {
				if !full["kms:GenerateDataKey"] {
					t.Error("the full rendering with --kms does not allow kms:GenerateDataKey, which is what S3 asks for on every PUT")
				}
				if !readOnly["kms:Decrypt"] {
					t.Error("the --read-only rendering with --kms does not allow kms:Decrypt, so it cannot read an encrypted record")
				}
			}
		})
	}
}

// TestIAMReadOnlyRenderingKeepsTheBoundaryDenies: the read-only rendering is
// the full one with grants taken away and nothing else. Every Deny the full
// rendering carries is in it, unchanged, because the cross-estate boundary
// does not depend on what the role may write. A "read-only" policy that
// dropped a Deny would be the looser of the two while reading as the safer.
func TestIAMReadOnlyRenderingKeepsTheBoundaryDenies(t *testing.T) {
	denies := func(args ...string) map[string]string {
		t.Helper()
		var doc struct{ Statement []iamStatement }
		if err := json.Unmarshal(renderIAMPolicy(t, args...), &doc); err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, st := range doc.Statement {
			if st.Effect != "Deny" {
				continue
			}
			raw, err := json.Marshal(st)
			if err != nil {
				t.Fatal(err)
			}
			out[st.Sid] = string(raw)
		}
		return out
	}
	base := []string{"prod", iamBucket, "--reads-outputs-of", "network", "--kms", iamKMSKey}
	full := denies(base...)
	readOnly := denies(append(append([]string{}, base...), "--read-only")...)
	if len(full) != 3 {
		t.Fatalf("the full rendering has %d Deny statement(s), and this render has three (the read Deny, the dependency read Deny and the relabel Deny): %v", len(full), full)
	}
	for sid, want := range full {
		got, ok := readOnly[sid]
		if !ok {
			t.Errorf("the --read-only rendering has no %s; the cross-estate boundary is the same whether or not the role may write", sid)
			continue
		}
		if got != want {
			t.Errorf("%s differs between the two renderings.\nfull:\n  %s\nread-only:\n  %s", sid, want, got)
		}
	}
	for sid := range readOnly {
		if _, ok := full[sid]; !ok {
			t.Errorf("the --read-only rendering carries a Deny the full one does not, %s; say here why the two differ", sid)
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
	principals := []string{"arn:aws:iam::111122223333:role/prod-estate", "arn:aws:iam::111122223333:role/records-operator"}
	args := append([]string{"--key", iamKMSKey}, principals...)
	// Stdout alone, for renderIAMPolicy's reason: a render without --key
	// writes a warning to stderr (#1381) and CombinedOutput would put it
	// inside the JSON compared against the committed example.
	cmd := exec.Command("bash", append([]string{iamKeyStatementRenderer}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	got, err := cmd.Output()
	if err != nil {
		t.Fatalf("render-key-statement.sh: %v\n%s", err, stderr.String())
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
		Condition map[string]map[string]any
	}
	if err := json.Unmarshal(got, &st); err != nil {
		t.Fatalf("the statement is not JSON: %v\n%s", err, got)
	}
	if st.Effect != "Allow" || strings.Join(st.Principal.AWS, " ") != strings.Join(principals, " ") {
		t.Errorf("effect %q, principals %v", st.Effect, st.Principal.AWS)
	}
	// GitHub issue #1381. Without kms:ViaService the two actions are usable
	// directly, by every principal named, against any ciphertext made under
	// the key. With it they are usable only where S3 is the one asking.
	if via := st.Condition["StringEquals"]["kms:ViaService"]; via != iamKMSViaService {
		t.Errorf("the key statement requires kms:ViaService = %v, want %q", via, iamKMSViaService)
	}
	// And no encryption-context condition, deliberately: S3 sets that
	// context to the bucket ARN with S3 Bucket Keys on and to the object ARN
	// with them off, so a literal here is wrong for half of the buckets a
	// key serves and a wrong one denies every write, the first one included.
	for op, keys := range st.Condition {
		for key := range keys {
			if strings.HasPrefix(key, "kms:EncryptionContext:") {
				t.Errorf("the key statement conditions on %s (%s); with S3 Bucket Keys on that context is the bucket ARN and with them off it is the object ARN, so a guess denies every write", key, op)
			}
		}
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
	for _, bad := range []string{
		"arn:aws:iam::111122223333:root", "*", "arn:aws:iam::111122223333:role/*", "111122223333",
		"arn:aws:sts::111122223333:assumed-role/x/y", "",
		// GitHub issue #1381, both by name: the partition was matched as
		// "aws[a-z-]*", and the role path and name as one run of characters
		// that included "/", so an empty segment passed.
		"arn:awsevil:iam::111122223333:role/prod-estate",
		"arn:aws:iam::111122223333:role//",
		"arn:aws:iam::111122223333:role/",
		"arn:aws:iam::111122223333:role/a//b",
		"arn:aws:iam::11112222333:role/prod-estate",
		"arn:aws:iam::1111222233334:role/prod-estate",
		"arn:aws:iam::111122223333:group/admins",
		"arn:aws:iam::111122223333:role/has space",
	} {
		if out, err := exec.Command("bash", iamKeyStatementRenderer, bad).CombinedOutput(); err == nil {
			t.Errorf("render-key-statement.sh accepted %q:\n%s", bad, out)
		}
	}
	if out, err := exec.Command("bash", iamKeyStatementRenderer).CombinedOutput(); err == nil {
		t.Errorf("render-key-statement.sh printed a statement naming nobody:\n%s", out)
	}
	// One key lives in one partition, so principals from two of them cannot
	// all be using it. Rendered anyway, the statement reviews correctly and
	// refuses half of the principals it names.
	if out, err := exec.Command("bash", iamKeyStatementRenderer,
		"arn:aws:iam::111122223333:role/prod-estate",
		"arn:aws-cn:iam::111122223333:role/prod-estate").CombinedOutput(); err == nil {
		t.Errorf("render-key-statement.sh named principals in two partitions:\n%s", out)
	}
	for _, bad := range []string{"*", "garbage", "arn:awsevil:kms:us-east-2:111122223333:key/1234abcd-12ab-34cd-56ef-1234567890ab", "arn:aws:kms:us-east-2:111122223333:alias/mine", iamGovKMSKey} {
		if out, err := exec.Command("bash", iamKeyStatementRenderer, "--key", bad, "arn:aws:iam::111122223333:role/prod-estate").CombinedOutput(); err == nil {
			t.Errorf("render-key-statement.sh accepted --key %q beside a commercial-partition role:\n%s", bad, out)
		}
	}
	// The controls. A renderer that refuses everything passes every line
	// above, and two of these are the GovCloud and China shapes that #1381
	// found refused-by-accident or accepted-by-accident.
	for _, good := range [][]string{
		{"arn:aws:iam::111122223333:role/prod-estate"},
		{"--key", iamKMSKey, "arn:aws:iam::111122223333:role/prod-estate"},
		{"arn:aws:iam::111122223333:user/records-operator"},
		{"arn:aws:iam::111122223333:role/team/prod-estate"},
		{"arn:aws:iam::111122223333:role/with+plus=and,dots.and@at_and-dash"},
		{"--key", iamGovKMSKey, "arn:aws-us-gov:iam::111122223333:role/prod-estate"},
		{"--key", iamCNKMSKey, "arn:aws-cn:iam::111122223333:role/prod-estate"},
	} {
		if out, err := exec.Command("bash", append([]string{iamKeyStatementRenderer}, good...)...).CombinedOutput(); err != nil {
			t.Errorf("render-key-statement.sh refused %v: %v\n%s", good, err, out)
		}
	}
}

// TestKMSKeyStatementViaServiceFollowsThePartition: the service principal
// suffix is ".amazonaws.com.cn" in China and ".amazonaws.com" everywhere
// else, and the region is the key's own. A ViaService value that names the
// wrong endpoint denies every use of the key, which is what makes this worth
// pinning rather than reading off the rendered statement.
func TestKMSKeyStatementViaServiceFollowsThePartition(t *testing.T) {
	for _, tc := range []struct{ key, principal, want string }{
		{iamKMSKey, "arn:aws:iam::111122223333:role/prod-estate", "s3.us-east-2.amazonaws.com"},
		{iamGovKMSKey, "arn:aws-us-gov:iam::111122223333:role/prod-estate", "s3.us-gov-west-1.amazonaws.com"},
		{iamCNKMSKey, "arn:aws-cn:iam::111122223333:role/prod-estate", "s3.cn-north-1.amazonaws.com.cn"},
	} {
		cmd := exec.Command("bash", iamKeyStatementRenderer, "--key", tc.key, tc.principal)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("render-key-statement.sh --key %s: %v\n%s", tc.key, err, stderr.String())
		}
		var st struct {
			Condition map[string]map[string]any
		}
		if err := json.Unmarshal(out, &st); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
		if got := st.Condition["StringEquals"]["kms:ViaService"]; got != tc.want {
			t.Errorf("--key %s renders kms:ViaService = %v, want %q", tc.key, got, tc.want)
		}
	}
	// And the flag is optional, so every invocation written before it keeps
	// working. The render says on stderr what it left out, the way a render
	// without --account does, and stdout stays JSON on its own.
	cmd := exec.Command("bash", iamKeyStatementRenderer, "arn:aws:iam::111122223333:role/prod-estate")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("render-key-statement.sh with no --key: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--key") || !strings.Contains(stderr.String(), "kms:ViaService") {
		t.Errorf("a render with no --key said this on stderr:\n%s", stderr.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Errorf("stdout is not JSON on its own, so the warning leaked into the statement: %v\n%s", err, out)
	}
	if _, ok := doc["Condition"]; ok {
		t.Errorf("a render with no --key carries a Condition it has no region to build: %s", out)
	}
}

// TestIAMRendererWritesOnePartition: every ARN in a rendered policy is in the
// partition that was asked for. Until GitHub issue #1381 the renderer wrote
// the literal "arn:aws:", so an estate in GovCloud or in China received a
// policy naming resources in the commercial partition. Such a policy is valid
// JSON, attaches without complaint and grants the role nothing at all.
func TestIAMRendererWritesOnePartition(t *testing.T) {
	for _, tc := range []struct {
		partition string
		bucket    string
		key       string
	}{
		{"aws", iamBucket, iamKMSKey},
		{"aws-us-gov", iamGovBucket, iamGovKMSKey},
		{"aws-cn", iamCNBucket, iamCNKMSKey},
	} {
		t.Run(tc.partition, func(t *testing.T) {
			raw := renderIAMPolicy(t, "prod", tc.bucket, "--partition", tc.partition,
				"--kms", tc.key, "--account", iamAccount, "--reads-outputs-of", "network")
			// Every "arn:..." in the whole document, wherever it sits: a
			// Resource, a NotResource, or a field added later. Reading the
			// statements field by field would miss the one that was added
			// after this test was written, which is the case it exists for.
			arns := regexp.MustCompile(`arn:[a-z0-9-]*:`).FindAllString(string(raw), -1)
			if len(arns) < 8 {
				t.Fatalf("only %d ARN(s) in the rendered policy; this render has more than that, so something is not being read:\n%s", len(arns), raw)
			}
			for _, a := range arns {
				if a != "arn:"+tc.partition+":" {
					t.Errorf("--partition %s rendered an ARN beginning %q; a policy in the wrong partition matches nothing and denies everything", tc.partition, a)
				}
			}
		})
	}
}

// TestIAMRendererKeyPrefixIsWhatTheStoreWritesUnder ties the renderer's
// object ARNs to the namespaces the store really uses, so the two cannot
// drift. projection.BucketNamespaces is the one definition of what an estate
// writes under - its records (which key_prefix moves), its hint and its root
// outputs (which it does not) - and a renderer that disagreed with it by one
// namespace would produce a policy that reviews correctly and denies the
// estate its own keys. GitHub issue #1381.
func TestIAMRendererKeyPrefixIsWhatTheStoreWritesUnder(t *testing.T) {
	for _, tc := range []struct {
		name      string
		estate    string
		keyPrefix string
	}{
		{"no key_prefix", "prod", ""},
		{"a key_prefix", "prod", "team/prod"},
		{"a key_prefix with a trailing slash", "prod", "team/prod/"},
		{"a one-segment key_prefix", "prod", "records"},
		{"a key_prefix under the records root", "prod", "tofu-records/prod"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rs := &configs.LiveRecordStore{Type: "s3", Bucket: iamBucket}
			if tc.keyPrefix != "" {
				rs.KeyPrefix = tc.keyPrefix
				rs.KeyPrefixSet = true
			}
			var want []string
			for _, ns := range projection.BucketNamespaces(rs, tc.estate) {
				want = append(want, "arn:aws:s3:::"+iamBucket+"/"+ns+"*")
			}

			args := []string{tc.estate, iamBucket}
			if tc.keyPrefix != "" {
				args = append(args, "--key-prefix", tc.keyPrefix)
			}
			var doc struct{ Statement []iamStatement }
			if err := json.Unmarshal(renderIAMPolicy(t, args...), &doc); err != nil {
				t.Fatal(err)
			}
			// Both statements that name objects, and the list prefixes,
			// because an estate needs all three over the same namespaces:
			// read and delete, write, and the LIST that makes a missing key
			// answer 404.
			seen := 0
			for _, st := range doc.Statement {
				switch st.Sid {
				case "ReadAndDeleteByPrefix", "WriteOnlyObjectsTaggedAsThisEstate":
					seen++
					if got := anyStrings(st.Resource); strings.Join(got, "\n") != strings.Join(want, "\n") {
						t.Errorf("%s names\n  %v\nand projection.BucketNamespaces says the store writes under\n  %v", st.Sid, got, want)
					}
				case "ListOwnNamespaces":
					seen++
					var wantPrefixes []string
					for _, ns := range projection.BucketNamespaces(rs, tc.estate) {
						wantPrefixes = append(wantPrefixes, ns+"*")
					}
					if got := anyStrings(st.Condition["StringLike"]["s3:prefix"]); strings.Join(got, "\n") != strings.Join(wantPrefixes, "\n") {
						t.Errorf("%s lists\n  %v\nwant\n  %v", st.Sid, got, wantPrefixes)
					}
				}
			}
			if seen != 3 {
				t.Errorf("found %d of the three statements that name the estate's namespaces", seen)
			}
		})
	}
}

// TestIAMRenderersHoldNoApostropheInTheirJqProgram is a trap that has cost
// real time twice. Both renderers hold their jq program inside single quotes,
// so one apostrophe in a jq comment ends the quoting and breaks the script
// for EVERY input - after which a test that asserts a bad argument is refused
// passes, because everything is refused.
//
// This reads the file rather than the rendered output on purpose: a run that
// proves a good input still renders proves the quoting is intact today, and
// this says why it is intact, at the one place an editor would break it.
func TestIAMRenderersHoldNoApostropheInTheirJqProgram(t *testing.T) {
	for script, opener := range map[string]string{
		iamRenderer:             "--argjson others \"$others_json\" '",
		iamKeyStatementRenderer: "jq -s --arg via \"$via_service\" '",
	} {
		src, err := os.ReadFile(script)
		if err != nil {
			t.Fatal(err)
		}
		i := strings.Index(string(src), opener)
		if i < 0 {
			t.Fatalf("%s no longer opens its jq program with %q, so this test is reading nothing; find the new opener and put it here", script, opener)
		}
		program := string(src)[i+len(opener):]
		if n := strings.Count(program, "'"); n != 1 {
			t.Errorf("%s: the jq program holds %d single quotes, want exactly the one that closes it. An apostrophe in a jq comment ends the quoting and breaks the script for every input.", script, n)
		}
		if !strings.HasSuffix(strings.TrimRight(program, "\n"), "'") {
			t.Errorf("%s: the jq program does not end at the closing quote; what follows it is %q", script, strings.TrimRight(program, "\n"))
		}
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
	// "arn:awsevil:..." is #1381's own example: the partition was matched as
	// "aws[a-z-]*", so a partition that does not exist reached a Resource.
	for _, bad := range []string{"*", "garbage", "arn:aws:kms:us-east-2:111122223333:key/*", "arn:aws:kms:us-east-2:111122223333:alias/mine", "arn:aws:s3:::a-bucket", "arn:awsevil:kms:us-east-2:111122223333:key/1234abcd-12ab-34cd-56ef-1234567890ab"} {
		if out, err := exec.Command("bash", iamRenderer, "prod", iamBucket, "--kms", bad).CombinedOutput(); err == nil {
			t.Errorf("render-policy.sh accepted --kms %q:\n%s", bad, out)
		}
	}
	// GitHub issue #1381. The partition names the whole of AWS a policy
	// applies to, there are exactly three, and none of them is guessable
	// from the bucket name, so anything else is a typo that would render
	// ARNs matching nothing.
	for _, bad := range []string{"*", "", "AWS", "aws-gov", "awsevil", "aws-us-gov-west-1", "arn:aws"} {
		if out, err := exec.Command("bash", iamRenderer, "prod", iamBucket, "--partition", bad).CombinedOutput(); err == nil {
			t.Errorf("render-policy.sh accepted --partition %q:\n%s", bad, out)
		}
	}
	// A key and the bucket that uses it are in one partition. Rendering the
	// two apart produces a policy that is valid, reviews correctly, and
	// grants the role nothing at all.
	for _, tc := range [][]string{
		{"prod", iamBucket, "--kms", iamGovKMSKey},
		{"prod", iamGovBucket, "--partition", "aws-us-gov", "--kms", iamKMSKey},
		{"prod", iamCNBucket, "--partition", "aws-cn", "--kms", iamGovKMSKey},
	} {
		if out, err := exec.Command("bash", append([]string{iamRenderer}, tc...)...).CombinedOutput(); err == nil {
			t.Errorf("render-policy.sh rendered a key and a bucket in two partitions: %v\n%s", tc, out)
		}
	}
	// GitHub issue #1381. --key-prefix goes into a Resource ARN and into an
	// s3:prefix condition as it stands, and for a list, a write and a delete
	// that prefix is the whole defence - the same reason the bucket and the
	// estate name are checked. An empty segment survives
	// staterecord.NamespacePrefix and would be in every key.
	for _, bad := range []string{"*", "", "/", "/tofu-records/prod", "team//prod", "team/*", "../prod", "team/../../prod", "${aws:username}", "team prod", "team/prod/*"} {
		if out, err := exec.Command("bash", iamRenderer, "prod", iamBucket, "--key-prefix", bad).CombinedOutput(); err == nil {
			t.Errorf("render-policy.sh accepted --key-prefix %q:\n%s", bad, out)
		}
	}
	// GitHub issue #1381. --account goes into an aws:ResourceAccount
	// condition, which IAM compares as a literal string: an account with
	// dashes, an ARN around it, a wildcard or eleven digits matches no
	// request at all, so the policy would refuse the estate its own bucket
	// with a valid-looking condition nobody re-reads.
	for _, bad := range []string{"*", "", "1234-5678-9012", "11112222333", "1111222233334", "arn:aws:iam::111122223333:root", "111122223333 ", "abcdefghijkl", "${aws:PrincipalAccount}"} {
		if out, err := exec.Command("bash", iamRenderer, "prod", iamBucket, "--account", bad).CombinedOutput(); err == nil {
			t.Errorf("render-policy.sh accepted --account %q:\n%s", bad, out)
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
		{"prod", iamGovBucket, "--partition", "aws-us-gov", "--kms", iamGovKMSKey},
		{"prod", iamCNBucket, "--partition", "aws-cn", "--kms", iamCNKMSKey},
		{"prod", iamBucket, "--partition", "aws"},
		{"prod", iamBucket, "--account", iamAccount},
		{"prod", iamBucket, "--account", "000000000001"},
		{"prod", iamBucket, "--key-prefix", "team/prod"},
		{"prod", iamBucket, "--key-prefix", "team/prod/"},
		{"prod", iamBucket, "--key-prefix", "tofu-records/prod"},
		{"prod", iamBucket, "--key-prefix", "one-segment"},
		{"prod", iamBucket, "--account", iamAccount, "--kms", iamKMSKey, "--reads-outputs-of", "network"},
		{"prod", iamBucket, "--read-only"},
		{"prod", iamBucket, "--read-only", "--account", iamAccount, "--kms", iamKMSKey, "--reads-outputs-of", "network", "--key-prefix", "team/prod"},
	} {
		if out, err := exec.Command("bash", append([]string{iamRenderer}, good...)...).CombinedOutput(); err != nil {
			t.Errorf("render-policy.sh refused %v: %v\n%s", good, err, out)
		}
	}
	// GitHub issue #1370. --read-only takes no value, so a word after it is
	// a mistake and not an argument to it; a renderer that swallowed one
	// would ignore whatever the caller meant to pass.
	for _, bad := range [][]string{
		{"prod", iamBucket, "--read-only", "true"},
		{"prod", iamBucket, "--read-only=true"},
		{"prod", iamBucket, "--readonly"},
	} {
		if out, err := exec.Command("bash", append([]string{iamRenderer}, bad...)...).CombinedOutput(); err == nil {
			t.Errorf("render-policy.sh accepted %v:\n%s", bad, out)
		}
	}
}
