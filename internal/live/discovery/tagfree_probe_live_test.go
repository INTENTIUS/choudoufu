// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// TestListedObjectsCarryTheirTags measures one property of the AWS provider's
// native list resources, against a real provider plugin and the pinned
// emulator: does an object that came out of ListResource carry the tag map
// the same object carries when it is read?
//
// It exists because issue #255's e2e run found that aws_iam_role's list
// resource does not - iam:ListRoles returns no tags and the provider issues
// no GetRole per member - so a swept role reads as "tagged with nothing" and
// the per-type sweep silently finds no owner. That is a wrong verdict, not a
// refusal, and the question this test answers is how many other types share
// the property.
//
// The natural experiment is the demo estate: stock terraform applies it, so
// every resource in it carries this estate's marker in a real tag, verified
// through the provider's own read during apply. Anything that then lists
// WITHOUT the marker is losing tags on the list path specifically.
//
//	TF_FLOCI_TEST=1 go test ./internal/live/discovery/ -run TestListedObjectsCarryTheirTags -v
func TestListedObjectsCarryTheirTags(t *testing.T) {
	flocitest.Gate(t, "listed-object tags")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, terraformBin)

	ctx := context.Background()
	flociPort := flocitest.StartFloci(t, "cdf-tagfree")
	endpoint := "http://localhost:" + flociPort

	t.Setenv("AWS_ENDPOINT_URL", endpoint)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)

	dir := flocitest.CopyEstate(t)
	flocitest.PluginCacheDir(t)
	flocitest.Run(t, dir, terraformBin, "init", "-input=false", "-no-color")
	flocitest.Run(t, dir, terraformBin, "apply", "-auto-approve", "-input=false", "-no-color")

	declaredTypes := estateDeclaredTypes(t, dir)
	t.Logf("the estate declares %d resource types", len(declaredTypes))

	provider := launchAWSProvider(t, dir)

	schemas, diags := listclient.ListSchemas(ctx, provider)
	if diags.HasErrors() {
		t.Fatalf("ListSchemas: %s", diags.Err())
	}

	type row struct {
		typeName string
		verdict  string
		detail   string
	}
	var rows []row

	for _, typeName := range declaredTypes {
		ts, ok := schemas.Get(typeName)
		if !ok {
			rows = append(rows, row{typeName, "NO-LIST-RESOURCE", "the provider offers no native list resource"})
			continue
		}
		if !markerCapable(ts) {
			rows = append(rows, row{typeName, "NOT-TAGGABLE", "the managed schema has neither tags nor tags_all"})
			continue
		}

		vals := map[string]cty.Value{}
		if hasAttr(ts.Config, "region") {
			vals["region"] = cty.StringVal(awsRegion)
		}
		cfg, cfgDiags := ts.BuildConfig(vals)
		if cfgDiags.HasErrors() {
			rows = append(rows, row{typeName, "CONFIG-FAILED", cfgDiags.Err().Error()})
			continue
		}
		results, listDiags := listclient.List(ctx, provider, typeName, cfg, true)
		if listDiags.HasErrors() {
			rows = append(rows, row{typeName, "LIST-FAILED", listDiags.Err().Error()})
			continue
		}
		if len(results) == 0 {
			rows = append(rows, row{typeName, "LISTED-NOTHING", "the emulator returned no objects of this type"})
			continue
		}

		// Per listed object: what shape did tags/tags_all arrive in, and did
		// any object carry this estate's marker?
		var sawMarker bool
		shapes := map[string]int{}
		for _, r := range results {
			shapes[tagShape(r.Resource)]++
			if tags, ok := markers.TagsOf(r.Resource); ok && tags[TagEstate] == estateName {
				sawMarker = true
			}
		}
		verdict := tagsCarried
		if !sawMarker {
			verdict = tagsLost
		}
		rows = append(rows, row{typeName, verdict, fmt.Sprintf("%d listed, shapes %s", len(results), shapeString(shapes))})
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].verdict != rows[j].verdict {
			return rows[i].verdict < rows[j].verdict
		}
		return rows[i].typeName < rows[j].typeName
	})

	counts := map[string]int{}
	lost := map[string]bool{}
	decided := 0
	for _, r := range rows {
		counts[r.verdict]++
		if r.verdict == tagsLost || r.verdict == tagsCarried {
			decided++
		}
		if r.verdict == tagsLost {
			lost[r.typeName] = true
		}
		t.Logf("PROBE %-16s %-44s %s", r.verdict, r.typeName, r.detail)
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("PROBE TOTAL %-16s %d", k, counts[k])
	}
	t.Logf("PROBE DENOMINATOR %d types decided (%d of them taggable+listable and carrying a live marker)", decided, decided)

	// Both directions. A type joining the set is a new silent hole; a type
	// leaving it is a provider release that fixed one, and the entry below
	// has to go rather than sit there describing nothing.
	for typeName, why := range tagLosingListPaths {
		if !lost[typeName] {
			t.Errorf("%s is recorded as losing its tags on the list path, but this run read a marker off a listed one. "+
				"If a provider release fixed it, delete the entry and say which version did. Recorded reason: %s", typeName, why)
		}
	}
	for typeName := range lost {
		if _, ok := tagLosingListPaths[typeName]; !ok {
			t.Errorf("%s listed objects that carry this estate's marker in a real tag, and not one of them came back with it. "+
				"That is a new silent removal hole of the shape issue #255 found in aws_iam_role: the per-type sweep will report "+
				"nothing undeclared for this type while an owned resource sits there. Establish which AWS API strips the tags, "+
				"record it in tagLosingListPaths, and open an issue for the coverage.", typeName)
		}
	}
}

const (
	tagsLost    = "TAGS-LOST"
	tagsCarried = "TAGS-CARRIED"
)

// tagLosingListPaths is a MEASUREMENT, not a rule: the types this probe has
// caught returning objects with no tags on them at all, when the same objects
// demonstrably carry this estate's marker. Nothing in the product path reads
// it, and nothing should - a hand-list here would be a hand-list wherever it
// was consumed. It exists so that the set cannot grow without somebody saying
// so, which is the only guard available while the property is invisible to
// every artifact this repository generates (the CloudFormation registry's
// handler permissions were tried and predict it on 13 of 24 sampled types,
// including getting aws_iam_role backwards).
//
// Each reason is sourced from the AWS API rather than from the emulator, so
// that the entry does not rest on floci behaving like AWS.
var tagLosingListPaths = map[string]string{
	"aws_iam_role": "iam:ListRoles does not return tags (AWS documents \"this operation does not return tags\"; " +
		"iam:GetRole does), and the provider's role list resource issues no GetRole per member - zero GetRole " +
		"requests reach the wire during ListResource. Issue #255.",
	"aws_ssm_parameter": "ssm:DescribeParameters returns ParameterMetadata, which has no Tags member; tags need a " +
		"separate ssm:ListTagsForResource call. live/registry-schema-facts.json says the same thing from the other " +
		"side: AWS::SSM::Parameter's read handler needs ssm:ListTagsForResource and its list handler does not.",
}

// tagShape names how a listed object's tag attributes arrived, so that
// "absent", "null" and "present but empty" are distinguishable in the log.
// That distinction is the whole question: an empty map is a real "tagged
// with nothing", a null attribute is "nobody populated this field".
func tagShape(obj cty.Value) string {
	if obj == cty.NilVal || obj.IsNull() || !obj.IsKnown() || !obj.Type().IsObjectType() {
		return "no-object"
	}
	part := func(name string) string {
		if !obj.Type().HasAttribute(name) {
			return name + "=absent"
		}
		v := obj.GetAttr(name)
		switch {
		case !v.IsKnown():
			return name + "=unknown"
		case v.IsNull():
			return name + "=null"
		case !v.CanIterateElements():
			return name + "=scalar"
		case v.LengthInt() == 0:
			return name + "=empty"
		default:
			return fmt.Sprintf("%s=%d", name, v.LengthInt())
		}
	}
	return part("tags") + "," + part("tags_all")
}

func shapeString(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s x%d", k, m[k]))
	}
	return strings.Join(parts, "; ")
}

var resourceBlockRE = regexp.MustCompile(`(?m)^resource\s+"([^"]+)"`)

// estateDeclaredTypes reads the type names out of the estate's own .tf files
// rather than carrying a list: the fixture is the sample, so the sample moves
// when the fixture does.
func estateDeclaredTypes(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the estate directory: %v", err)
	}
	set := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // a test fixture path
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		for _, m := range resourceBlockRE.FindAllStringSubmatch(string(data), -1) {
			set[m[1]] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
