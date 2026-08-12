// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package foreign

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/opentofu/opentofu/internal/addrs"
	"github.com/opentofu/opentofu/internal/configs"
	"github.com/opentofu/opentofu/internal/stateless/discovery"
	"github.com/opentofu/opentofu/internal/stateless/flocitest"
	"github.com/opentofu/opentofu/internal/tfdiags"
)

// Every test classifies against the P0.1 estate fixture, so the declared
// side of a match is a real configuration: a security group named in
// configuration, a VPC with a literal CIDR, and two for_each subnets whose
// arguments come out of each.value.
const estateName = "stateless-e2e"

func estateDir(t *testing.T) string {
	return flocitest.EstateDir(t)
}

// ---------------------------------------------------------------------------
// FOREIGN
// ---------------------------------------------------------------------------

// TestClassifyForeign is the safety property in miniature: a live resource
// carrying no marker, matching nothing, reported and classified foreign with
// everything an operator needs to go look at it.
func TestClassifyForeign(t *testing.T) {
	res := classifyFixture(t, discovery.Result{
		Scans: []discovery.TypeScan{scan("aws_security_group", 1)},
		Unclaimed: []discovery.UnclaimedResource{
			live("aws_security_group", "sg-foreign", "someone-elses-sg",
				map[string]string{"Name": "someone-elses-sg", "team": "payments"},
				map[string]string{"name": "someone-elses-sg"}),
		},
	})

	if len(res.Foreign) != 1 {
		t.Fatalf("want exactly one foreign resource, got:\n%s", res)
	}
	f := res.Foreign[0]
	if f.TypeName != "aws_security_group" || f.LiveID != "sg-foreign" {
		t.Errorf("foreign resource is %s, want the aws_security_group sg-foreign", f)
	}
	if f.DisplayName != "someone-elses-sg" {
		t.Errorf("foreign resource carries display name %q", f.DisplayName)
	}
	if got := f.TagSummary(0); got != "Name=someone-elses-sg, team=payments" {
		t.Errorf("tag summary is %q, want both tags sorted by key", got)
	}
	if f.Why == "" {
		t.Error("a foreign resource was reported with no reason for not being a bind candidate")
	}
	if len(res.Candidates) != 0 {
		t.Errorf("an unmatched resource was offered for adoption: %v", res.Candidates)
	}
	if res.Empty() || res.SweptClean() {
		t.Errorf("a result holding a foreign resource reports itself empty:\n%s", res)
	}
}

// TestClassifyForeignWithoutIdentity: a provider that sends no identity does
// not get to make a resource disappear from the report.
func TestClassifyForeignWithoutIdentity(t *testing.T) {
	res := classifyFixture(t, discovery.Result{
		Scans: []discovery.TypeScan{scan("aws_route_table", 1)},
		Unclaimed: []discovery.UnclaimedResource{
			live("aws_route_table", "", "rtb-nameless", nil, nil),
		},
	})

	if len(res.Foreign) != 1 {
		t.Fatalf("want one foreign resource, got:\n%s", res)
	}
	if got := res.Foreign[0].String(); !strings.Contains(got, "(no identity)") {
		t.Errorf("foreign line is %q, want it to say the identity is missing", got)
	}
	if got := res.Foreign[0].TagSummary(0); got != "(no tags)" {
		t.Errorf("tag summary of an untagged resource is %q", got)
	}
}

// TestClassifyTypesWithNoDistinguishingArguments: route tables, gateways and
// EIPs can never be bind candidates, and the report says why rather than
// leaving the operator to wonder.
func TestClassifyTypesWithNoDistinguishingArguments(t *testing.T) {
	for _, typeName := range []string{"aws_route_table", "aws_internet_gateway", "aws_eip"} {
		t.Run(typeName, func(t *testing.T) {
			res := classifyFixture(t, discovery.Result{
				Scans:     []discovery.TypeScan{scan(typeName, 1)},
				Unbound:   []addrs.AbsResourceInstance{mustAddr(t, unboundAddrFor(typeName))},
				Unclaimed: []discovery.UnclaimedResource{live(typeName, "id-1", "", nil, nil)},
			})
			if len(res.Candidates) != 0 {
				t.Fatalf("%s was offered for adoption:\n%s", typeName, res)
			}
			if len(res.Foreign) != 1 {
				t.Fatalf("want one foreign %s:\n%s", typeName, res)
			}
			if !strings.Contains(res.Foreign[0].Why, "distinguishes") {
				t.Errorf("reason is %q, want it to name the missing distinguishing argument", res.Foreign[0].Why)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// BIND_CANDIDATE
// ---------------------------------------------------------------------------

// TestClassifyBindCandidate: an unmarked security group whose name is exactly
// the declared one's is offered for adoption, with the marker pair to stamp -
// and is not bound, which is the part the marker spec insists on.
func TestClassifyBindCandidate(t *testing.T) {
	res := classifyFixture(t, discovery.Result{
		Scans:   []discovery.TypeScan{scan("aws_security_group", 1)},
		Unbound: []addrs.AbsResourceInstance{mustAddr(t, "aws_security_group.main")},
		Unclaimed: []discovery.UnclaimedResource{
			live("aws_security_group", "sg-0abc", "stateless-e2e-main",
				map[string]string{"Name": "stateless-e2e-main"},
				map[string]string{"name": "stateless-e2e-main", "description": "estate fixture security group"}),
		},
	})

	if len(res.Candidates) != 1 {
		t.Fatalf("want exactly one bind candidate, got:\n%s", res)
	}
	c := res.Candidates[0]
	if c.Addr.String() != "aws_security_group.main" {
		t.Errorf("candidate offered for %s", c.Addr)
	}
	if c.LiveID != "sg-0abc" {
		t.Errorf("candidate names live ID %q", c.LiveID)
	}
	if len(c.Matched) != 1 || c.Matched[0].Attr != "name" || c.Matched[0].Value != "stateless-e2e-main" {
		t.Errorf("candidate matched on %v, want the name argument only", c.Matched)
	}
	if c.MarkerEstate != estateName || c.MarkerAddress != "aws_security_group.main" {
		t.Errorf("candidate carries marker pair %q/%q", c.MarkerEstate, c.MarkerAddress)
	}
	if !strings.Contains(c.Hint, "sg-0abc") ||
		!strings.Contains(c.Hint, "tofu-estate,Value="+estateName) ||
		!strings.Contains(c.Hint, "tofu-address,Value=aws_security_group.main") {
		t.Errorf("adoption hint does not stamp both markers on the live resource: %q", c.Hint)
	}
	if len(res.Foreign) != 0 {
		t.Errorf("a bind candidate was also reported as foreign:\n%s", res)
	}
}

// TestClassifyVPCBindCandidate matches on a literal CIDR rather than a name,
// which is the other shape of identity-bearing argument in the table.
func TestClassifyVPCBindCandidate(t *testing.T) {
	res := classifyFixture(t, discovery.Result{
		Scans:   []discovery.TypeScan{scan("aws_vpc", 1)},
		Unbound: []addrs.AbsResourceInstance{mustAddr(t, "aws_vpc.main")},
		Unclaimed: []discovery.UnclaimedResource{
			live("aws_vpc", "vpc-0abc", "", nil, map[string]string{"cidr_block": "10.42.0.0/16"}),
		},
	})

	c, ok := res.CandidateFor(mustAddr(t, "aws_vpc.main"))
	if !ok {
		t.Fatalf("the VPC with the declared CIDR was not offered for adoption:\n%s", res)
	}
	if c.Matched[0].Attr != "cidr_block" {
		t.Errorf("matched on %v, want cidr_block", c.Matched)
	}
}

// TestClassifyNearMissStaysForeign is the conservative-matching boundary: one
// character of difference in the identity-bearing argument is not a match,
// and the report says what differed.
func TestClassifyNearMissStaysForeign(t *testing.T) {
	for name, obj := range map[string]discovery.UnclaimedResource{
		"a suffix on the name": live("aws_security_group", "sg-old", "",
			nil, map[string]string{"name": "stateless-e2e-main-old"}),
		"a different case": live("aws_security_group", "sg-case", "",
			nil, map[string]string{"name": "Stateless-E2E-Main"}),
		"no name at all": live("aws_security_group", "sg-unnamed", "",
			nil, map[string]string{"description": "estate fixture security group"}),
	} {
		t.Run(name, func(t *testing.T) {
			res := classifyFixture(t, discovery.Result{
				Scans:     []discovery.TypeScan{scan("aws_security_group", 1)},
				Unbound:   []addrs.AbsResourceInstance{mustAddr(t, "aws_security_group.main")},
				Unclaimed: []discovery.UnclaimedResource{obj},
			})
			if len(res.Candidates) != 0 {
				t.Fatalf("a near miss was offered for adoption:\n%s", res)
			}
			if len(res.Foreign) != 1 {
				t.Fatalf("want one foreign resource:\n%s", res)
			}
			if !strings.Contains(res.Foreign[0].Why, "aws_security_group.main") {
				t.Errorf("the reason does not name the declared instance it nearly matched: %q", res.Foreign[0].Why)
			}
		})
	}
}

// TestClassifyPartialMatchStaysForeign: a subnet-shaped type needs every
// identity-bearing argument to agree, not the first one.
func TestClassifyPartialMatchStaysForeign(t *testing.T) {
	// aws_subnet.this is for_each in the fixture, so the declared side here
	// is built directly rather than through the fixture's own addresses; the
	// point under test is the all-arguments rule, and the keyed-instance rule
	// has its own test below.
	slots := []*slot{{
		addr: mustAddr(t, "aws_subnet.this"),
		values: []AttrMatch{
			{Attr: "cidr_block", Value: "10.42.1.0/24"},
			{Attr: "availability_zone", Value: "us-east-1a"},
		},
	}}

	sameCIDRotherAZ := live("aws_subnet", "subnet-1", "", nil, map[string]string{
		"cidr_block": "10.42.1.0/24", "availability_zone": "us-east-1b",
	})
	if _, ok := matches(&sameCIDRotherAZ, slots[0]); ok {
		t.Error("a subnet matching only on CIDR was treated as an exact match")
	}

	both := live("aws_subnet", "subnet-2", "", nil, map[string]string{
		"cidr_block": "10.42.1.0/24", "availability_zone": "us-east-1a",
	})
	if _, ok := matches(&both, slots[0]); !ok {
		t.Error("a subnet matching on both arguments was not treated as a match")
	}
}

// TestClassifyKeyedInstancesAreNeverCandidates: count and for_each members
// are phase 3's set matcher's business, and content matching them here would
// attach a plan to an arbitrary member of a set.
func TestClassifyKeyedInstancesAreNeverCandidates(t *testing.T) {
	res := classifyFixture(t, discovery.Result{
		Scans: []discovery.TypeScan{scan("aws_subnet", 2)},
		Unbound: []addrs.AbsResourceInstance{
			mustAddr(t, `aws_subnet.this["a"]`),
			mustAddr(t, `aws_subnet.this["b"]`),
		},
		Unclaimed: []discovery.UnclaimedResource{
			live("aws_subnet", "subnet-a", "", nil, map[string]string{
				"cidr_block": "10.42.1.0/24", "availability_zone": "us-east-1a",
			}),
		},
	})

	if len(res.Candidates) != 0 {
		t.Fatalf("a for_each member was offered for adoption:\n%s", res)
	}
	if len(res.Foreign) != 1 {
		t.Fatalf("want the subnet reported as foreign:\n%s", res)
	}
	if !strings.Contains(res.Foreign[0].Why, "slot markers") {
		t.Errorf("the reason does not point at slot markers: %q", res.Foreign[0].Why)
	}
}

// TestClassifyAmbiguousMatchStaysForeign: the one-to-one rule. Two live
// resources that match one declared instance equally well are both foreign,
// and the report names the other one so an operator can tell them apart.
func TestClassifyAmbiguousMatchStaysForeign(t *testing.T) {
	res := classifyFixture(t, discovery.Result{
		Scans:   []discovery.TypeScan{scan("aws_security_group", 2)},
		Unbound: []addrs.AbsResourceInstance{mustAddr(t, "aws_security_group.main")},
		Unclaimed: []discovery.UnclaimedResource{
			live("aws_security_group", "sg-one", "", nil, map[string]string{"name": "stateless-e2e-main"}),
			live("aws_security_group", "sg-two", "", nil, map[string]string{"name": "stateless-e2e-main"}),
		},
	})

	if len(res.Candidates) != 0 {
		t.Fatalf("one of two identical-looking resources was picked:\n%s", res)
	}
	if len(res.Foreign) != 2 {
		t.Fatalf("want both reported foreign:\n%s", res)
	}
	for _, f := range res.Foreign {
		if !strings.Contains(f.Why, "guess") {
			t.Errorf("%s: reason %q does not say the choice would be a guess", f.LiveID, f.Why)
		}
	}
}

// ---------------------------------------------------------------------------
// OTHER_ESTATE
// ---------------------------------------------------------------------------

func TestClassifyOtherEstate(t *testing.T) {
	res := classifyFixture(t, discovery.Result{
		Scans: []discovery.TypeScan{
			{TypeName: "aws_vpc", Scope: discovery.ScopeAll, Listed: 3, OtherEstate: 1},
			{TypeName: "aws_security_group", Scope: discovery.ScopeAll, Listed: 2},
		},
		Unclaimed: []discovery.UnclaimedResource{
			// Discovery itself never routes another estate's resource here,
			// but classification answers "whose is it" from the tags, so a
			// tagged resource that does arrive is counted, not itemized.
			live("aws_security_group", "sg-theirs", "",
				map[string]string{discovery.TagEstate: "other-estate", discovery.TagAddress: "aws_security_group.web"}, nil),
		},
	})

	if len(res.Foreign) != 0 {
		t.Errorf("another estate's resource was reported as foreign:\n%s", res)
	}
	if got := res.OtherEstateTotal(); got != 2 {
		t.Errorf("other-estate total is %d, want 2 (one named, one from the scan tally)", got)
	}

	var named, unnamed bool
	for _, e := range res.OtherEstates {
		switch e.Estate {
		case "other-estate":
			named = true
			if e.Count != 1 || len(e.Types) != 1 || e.Types[0] != "aws_security_group" {
				t.Errorf("named estate count is %s", e)
			}
		case "":
			unnamed = true
			if e.Count != 1 || e.Types[0] != "aws_vpc" {
				t.Errorf("unnamed estate count is %s", e)
			}
		}
	}
	if !named || !unnamed {
		t.Errorf("want both a named and an unnamed other-estate count:\n%s", res)
	}
}

// TestClassifyIgnoresOwnEstate: a resource carrying this estate's own marker
// is discovery's business, not this package's, and is never reclassified as
// foreign.
func TestClassifyIgnoresOwnEstate(t *testing.T) {
	res := classifyFixture(t, discovery.Result{
		Scans: []discovery.TypeScan{scan("aws_vpc", 1)},
		Unclaimed: []discovery.UnclaimedResource{
			live("aws_vpc", "vpc-ours", "",
				map[string]string{discovery.TagEstate: estateName, discovery.TagAddress: "aws_vpc.main"}, nil),
		},
	})
	if !res.Empty() {
		t.Errorf("this estate's own resource was classified:\n%s", res)
	}
	if !res.SweptClean() {
		t.Error("a swept scan that found nothing unclaimed does not report itself swept clean")
	}
}

// ---------------------------------------------------------------------------
// Epistemics: what was swept, and what was not
// ---------------------------------------------------------------------------

// TestClassifyUnsweptTypes: every way a type can be unknown to the
// classification is reported as its own kind, because "nobody looked" must
// never read as "there are none".
func TestClassifyUnsweptTypes(t *testing.T) {
	res := classifyFixture(t, discovery.Result{
		Scans: []discovery.TypeScan{
			{TypeName: "aws_vpc", Scope: discovery.ScopeAll},
			{TypeName: "aws_subnet", Scope: discovery.ScopeEstate},
			{TypeName: "aws_eip"},
			{TypeName: "aws_route_table"},
		},
		Problems: []discovery.Problem{
			{Kind: discovery.ProblemTypeNotListable, TypeName: "aws_eip", Detail: "cannot list"},
			{Kind: discovery.ProblemListFailed, TypeName: "aws_route_table", Detail: "list blew up"},
		},
	})

	want := map[string]UnsweptReason{
		"aws_subnet":                     UnsweptEstateScoped,
		"aws_eip":                        UnsweptNotListable,
		"aws_route_table":                UnsweptListFailed,
		"aws_s3_bucket":                  UnsweptNotScanned,
		"aws_iam_role":                   UnsweptNotScanned,
		"aws_cloudwatch_log_group":       UnsweptNotScanned,
		"aws_internet_gateway":           UnsweptNotScanned,
		"aws_route":                      UnsweptNotScanned,
		"aws_route_table_association":    UnsweptNotScanned,
		"aws_s3_bucket_policy":           UnsweptNotScanned,
		"aws_iam_role_policy_attachment": UnsweptNotScanned,
	}
	for typeName, reason := range want {
		u, ok := res.UnsweptOf(typeName)
		if !ok {
			t.Errorf("%s is not reported as unswept:\n%s", typeName, res)
			continue
		}
		if u.Reason != reason {
			t.Errorf("%s is unswept for %s, want %s", typeName, u.Reason, reason)
		}
		if u.Detail == "" {
			t.Errorf("%s is unswept with no explanation", typeName)
		}
	}
	if len(res.Swept) != 1 || res.Swept[0] != "aws_vpc" {
		t.Errorf("swept types are %v, want aws_vpc alone", res.Swept)
	}
	if _, ok := res.UnsweptOf("aws_vpc"); ok {
		t.Error("a fully swept type is also reported as unswept")
	}
}

// TestClassifyNobodyLooked is the distinction the whole section rests on: an
// empty result from a pass that swept nothing is not a clean bill of health.
func TestClassifyNobodyLooked(t *testing.T) {
	never := classifyFixture(t, discovery.Result{})
	if !never.Empty() {
		t.Errorf("a pass that listed nothing reported findings:\n%s", never)
	}
	if never.SweptClean() {
		t.Error("a pass that swept no type at all claims to be swept clean")
	}
	if len(never.Unswept) == 0 {
		t.Errorf("a pass that swept nothing reported no unswept types:\n%s", never)
	}

	// The same emptiness, with a type actually swept: now it means something.
	clean := classifyFixture(t, discovery.Result{
		Scans: []discovery.TypeScan{scan("aws_security_group", 4)},
	})
	if !clean.SweptClean() {
		t.Errorf("a swept type with no unclaimed resources is not reported as swept clean:\n%s", clean)
	}
	if _, ok := clean.UnsweptOf("aws_security_group"); ok {
		t.Error("the swept type is also listed as unswept")
	}
}

// TestClassifyEstateScopedScanFindsNothing: with the server-side estate
// filter on, Unclaimed is empty because nothing looked. The result must not
// present that as a clean sweep.
func TestClassifyEstateScopedScanFindsNothing(t *testing.T) {
	res := classifyFixture(t, discovery.Result{
		Scans: []discovery.TypeScan{
			{TypeName: "aws_security_group", Scope: discovery.ScopeEstate, Listed: 1},
		},
	})
	if res.SweptClean() {
		t.Errorf("an estate-filtered scan claims to be a clean sweep:\n%s", res)
	}
	u, ok := res.UnsweptOf("aws_security_group")
	if !ok || u.Reason != UnsweptEstateScoped {
		t.Errorf("the estate-filtered type is not reported unswept:\n%s", res)
	}
}

// ---------------------------------------------------------------------------
// Inputs
// ---------------------------------------------------------------------------

func TestClassifyRejectsMissingInputs(t *testing.T) {
	if _, diags := Classify(context.Background(), Request{Estate: estateName, Config: loadConfig(t, estateDir(t))}); !diags.HasErrors() {
		t.Error("classifying with no discovery result did not error")
	}
	if _, diags := Classify(context.Background(), Request{Estate: estateName, Discovery: &discovery.Result{}}); !diags.HasErrors() {
		t.Error("classifying with no configuration did not error")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func classifyFixture(t *testing.T, res discovery.Result) *Result {
	t.Helper()

	out, diags := Classify(context.Background(), Request{
		Estate:    estateName,
		Config:    loadConfig(t, estateDir(t)),
		Discovery: &res,
	})
	if diags.HasErrors() {
		t.Fatalf("classification failed:\n%s", renderDiags(diags))
	}
	return out
}

// live builds one unclaimed live resource: its tags, and the attributes of
// the object the provider listed.
func live(typeName, id, displayName string, tags, attrs map[string]string) discovery.UnclaimedResource {
	obj := cty.NilVal
	if attrs != nil {
		vals := make(map[string]cty.Value, len(attrs))
		for k, v := range attrs {
			vals[k] = cty.StringVal(v)
		}
		obj = cty.ObjectVal(vals)
	}
	return discovery.UnclaimedResource{
		TypeName:     typeName,
		ImportID:     id,
		IdentityAttr: "id",
		DisplayName:  displayName,
		Tags:         tags,
		Resource:     obj,
	}
}

func scan(typeName string, listed int) discovery.TypeScan {
	return discovery.TypeScan{
		TypeName:  typeName,
		Filtering: discovery.FilterClientSide,
		Scope:     discovery.ScopeAll,
		Listed:    listed,
	}
}

// unboundAddrFor is the fixture's declared address for one type, used by the
// tests that only care that the type can never be matched on content.
func unboundAddrFor(typeName string) string {
	switch typeName {
	case "aws_eip":
		return "aws_eip.pool[0]"
	default:
		return typeName + ".main"
	}
}

func mustAddr(t *testing.T, s string) addrs.AbsResourceInstance {
	t.Helper()
	addr, diags := addrs.ParseAbsResourceInstanceStr(s)
	if diags.HasErrors() {
		t.Fatalf("bad test address %q: %s", s, diags.Err())
	}
	return addr
}

func loadConfig(t *testing.T, dir string) *configs.Config {
	t.Helper()

	parser := configs.NewParser(nil)
	call := configs.NewStaticModuleCall(
		addrs.RootModule,
		hcl.Range{},
		func(v *configs.Variable) (cty.Value, hcl.Diagnostics) { return v.Default, nil },
		dir,
		"default",
	)

	mod, diags := parser.LoadConfigDir(dir, call)
	if diags.HasErrors() {
		t.Fatalf("loading %s: %s", dir, diags.Error())
	}
	cfg, cfgDiags := configs.BuildConfig(context.Background(), mod, configs.ModuleWalkerFunc(
		func(_ context.Context, req *configs.ModuleRequest) (*configs.Module, *version.Version, hcl.Diagnostics) {
			t.Fatalf("fixture %s unexpectedly calls module %q", dir, req.Name)
			return nil, nil, nil
		},
	))
	if cfgDiags.HasErrors() {
		t.Fatalf("building config for %s: %s", dir, cfgDiags.Error())
	}
	return cfg
}

func renderDiags(diags tfdiags.Diagnostics) string {
	var b strings.Builder
	for _, d := range diags {
		b.WriteString(string(d.Severity()) + ": " + d.Description().Summary + "\n  " + d.Description().Detail + "\n")
	}
	return b.String()
}
