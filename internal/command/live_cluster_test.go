// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

const clusterNS = "tofu-records-alice"

// clusterServer is what the stand-in connection reached, so every report
// these tests build names a cluster the way a real one does (#1448).
const clusterServer = "https://127.0.0.1:6443"

func clusterTargetForTest() projection.ClusterTarget {
	return projection.ClusterTarget{Namespace: clusterNS, Server: clusterServer}
}

// clusterSettingsAlwaysReported is what a contract check reports on a block
// that does not set `insecure = true`: every cluster setting but
// tls_verification, which has a finding only when it fails (#1448).
func clusterSettingsAlwaysReported() []staterecord.Setting {
	var out []staterecord.Setting
	for _, s := range staterecord.ClusterSettings {
		if s != staterecord.ClusterTLSVerification {
			out = append(out, s)
		}
	}
	return out
}

func clusterFindings(failing ...staterecord.Setting) []staterecord.Finding {
	var out []staterecord.Finding
	for _, s := range clusterSettingsAlwaysReported() {
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

// TestLiveClusterReportsTheClusterNotTheConfiguration is #1341's second rule
// on the other store. A plan under allow_insecure = ["estate_boundary"]
// proceeds; this report must still call a cluster with no boundary policy NOT
// correct, and name the waiver separately as hiding it.
func TestLiveClusterReportsTheClusterNotTheConfiguration(t *testing.T) {
	waived := &configs.LiveRecordStore{Type: "kubernetes", AllowInsecure: []string{"estate_boundary", "read_isolation"}}
	findings := clusterFindings(staterecord.ClusterEstateBoundary)

	for name, rs := range map[string]*configs.LiveRecordStore{
		"no configuration":       nil,
		"no waiver":              {Type: "kubernetes"},
		"estate_boundary waived": waived,
	} {
		if r := buildLiveClusterReport(clusterTargetForTest(), "alice", "apply", findings, rs); r.Correct {
			t.Errorf("%s: a cluster with no estate boundary policy was reported correct", name)
		}
	}

	r := buildLiveClusterReport(clusterTargetForTest(), "alice", "apply", findings, waived)
	if len(r.Waived) != 2 {
		t.Fatalf("got %d waiver lines, want 2", len(r.Waived))
	}
	if !r.Waived[0].Hiding || r.Waived[0].Setting != "estate_boundary" {
		t.Errorf("the estate_boundary waiver is hiding a real failure and was not reported so: %+v", r.Waived[0])
	}
	if r.Waived[1].Hiding {
		t.Errorf("the read_isolation waiver hides nothing and was reported as hiding: %+v", r.Waived[1])
	}

	text := renderLiveClusterReport(r)
	if !strings.Contains(text, "records namespace "+clusterNS+": NOT correct") {
		t.Errorf("the verdict line does not read NOT correct:\n%s", text)
	}
	for _, want := range []string{"estate_boundary", "FAIL", "DOES fail it", "hiding nothing"} {
		if !strings.Contains(text, want) {
			t.Errorf("the table does not contain %q:\n%s", want, text)
		}
	}

	ok := buildLiveClusterReport(clusterTargetForTest(), "alice", "apply", clusterFindings(), waived)
	if !ok.Correct || !strings.HasSuffix(renderLiveClusterReport(ok), "records namespace "+clusterNS+": correct") {
		t.Errorf("a cluster that passes all four was not reported correct:\n%s", renderLiveClusterReport(ok))
	}
}

// TestLiveClusterNotCheckedIsNotAPass is the rule the issue insisted on, and
// the one this command exists to make visible. Encryption at rest is not
// readable on a managed control plane; that is not a pass, it is its own
// verdict, and it makes the cluster NOT correct.
func TestLiveClusterNotCheckedIsNotAPass(t *testing.T) {
	findings := clusterFindings()
	findings[2] = staterecord.Finding{
		Setting: staterecord.ClusterEncryptionAtRest,
		Outcome: staterecord.NotChecked,
		Found:   "not readable from here, not checked: no kube-apiserver Pod is visible in kube-system",
	}
	r := buildLiveClusterReport(clusterTargetForTest(), "alice", "apply", findings, nil)
	if r.Correct {
		t.Error("a cluster with one property unreadable was reported correct")
	}
	if r.Settings[2].Verdict != "not_checked" {
		t.Errorf("verdict %q, want not_checked", r.Settings[2].Verdict)
	}
	text := renderLiveClusterReport(r)
	if !strings.Contains(text, "NOT_CHECKED") {
		t.Errorf("the table does not print the verdict:\n%s", text)
	}
	if !strings.Contains(text, "a NOT_CHECKED property is not a pass") {
		t.Errorf("the verdict line does not say why it is not correct:\n%s", text)
	}
}

// TestLiveClusterWarningsAreCountedAndNotSwallowed is the third verdict. A
// warning is a concern a run proceeds past, so calling the cluster NOT
// correct for one would make this report disagree with every apply; letting
// a plain "correct" swallow it would hide the thing someone ran the report
// for. It prints, it counts, and the verdict line names the count.
func TestLiveClusterWarningsAreCountedAndNotSwallowed(t *testing.T) {
	findings := clusterFindings()
	findings[1] = staterecord.Finding{
		Setting: staterecord.ClusterReadIsolation, Outcome: staterecord.Warned,
		Found: "this identity may get and list secrets in EVERY namespace, and this cluster holds no other tofu-records-* namespace today, so nothing is exposed yet",
	}
	r := buildLiveClusterReport(clusterTargetForTest(), "alice", "apply", findings, nil)
	if !r.Correct {
		t.Error("a warning made the cluster NOT correct; a run proceeds past it, so the report would disagree with every apply")
	}
	if r.Warnings != 1 {
		t.Errorf("Warnings = %d, want 1", r.Warnings)
	}
	if r.Settings[1].Verdict != "warn" {
		t.Errorf("verdict %q, want warn", r.Settings[1].Verdict)
	}
	text := renderLiveClusterReport(r)
	if !strings.Contains(text, "WARN") {
		t.Errorf("the table does not print the verdict:\n%s", text)
	}
	if !strings.Contains(text, "with 1 warning(s) a run proceeds past") {
		t.Errorf("the verdict line swallows the warning:\n%s", text)
	}

	// A clean cluster's verdict line says nothing about warnings.
	if clean := renderLiveClusterReport(buildLiveClusterReport(clusterTargetForTest(), "alice", "apply", clusterFindings(), nil)); strings.Contains(clean, "warning") {
		t.Errorf("a cluster with no warnings mentions warnings:\n%s", clean)
	}
}

// TestLiveClusterReportsEveryVerbItReviewed is what makes the report usable
// for the question an operator actually has: which verb is missing. The
// namespace_access line carries all five, with the authorizer's answer and
// whether this run needs it.
func TestLiveClusterReportsEveryVerbItReviewed(t *testing.T) {
	findings := clusterFindings()
	findings[0] = staterecord.Finding{
		Setting: staterecord.ClusterNamespaceAccess, Outcome: staterecord.Passed, Found: "allowed: get, list; denied: create, update, delete",
		Verbs: []staterecord.VerbAccess{
			{Verb: "get", Allowed: true, Required: true},
			{Verb: "list", Allowed: true, Required: true},
			{Verb: "create", Allowed: false, Required: false},
			{Verb: "update", Allowed: false, Required: false},
			{Verb: "delete", Allowed: false, Required: false},
		},
	}
	r := buildLiveClusterReport(clusterTargetForTest(), "alice", "plan", findings, nil)
	if !r.Correct {
		t.Errorf("a plan identity with get and list was reported NOT correct: %+v", r.Settings[0])
	}
	text := renderLiveClusterReport(r)
	for _, want := range []string{
		"secrets get     allowed  (needed by this run)",
		"secrets create  denied   (not needed by this run)",
		"checked as a plan identity",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the table does not contain %q:\n%s", want, text)
		}
	}

	// The same findings, asked as an apply: the three write verbs are then
	// required, so the report has to say so rather than repeat the plan's
	// answer. (The requirement itself is the contract's, measured in
	// internal/live/staterecord; what is pinned here is that the report
	// prints which question it asked.)
	apply := renderLiveClusterReport(buildLiveClusterReport(clusterTargetForTest(), "alice", "apply", clusterFindings(), nil))
	if !strings.Contains(apply, "checked as an apply identity") {
		t.Errorf("the table does not say it asked the apply question:\n%s", apply)
	}
}

// TestLiveClusterJSONIsTheSameReport pins that the two spellings are one
// report, the way live-bucket's are.
func TestLiveClusterJSONIsTheSameReport(t *testing.T) {
	r := buildLiveClusterReport(clusterTargetForTest(), "alice", "plan", clusterFindings(staterecord.ClusterReadIsolation), nil)
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshalling the report: %v", err)
	}
	var back liveClusterReport
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshalling the report: %v", err)
	}
	if back.Correct {
		t.Error("the JSON says the cluster is correct and the table does not")
	}
	if back.Namespace != clusterNS || back.Estate != "alice" || back.CheckedAs != "plan" {
		t.Errorf("the JSON lost what the report was about: %+v", back)
	}
	if want := len(clusterSettingsAlwaysReported()); len(back.Settings) != want {
		t.Errorf("the JSON carries %d settings, want %d", len(back.Settings), want)
	}
}

// TestLiveClusterNamespaceOverridesTheNamespaceOnly is GitHub issue #1448.
// -namespace used to skip the configuration, so the record_store block's
// host, config_context and exec were never read and the report went to
// whatever the ambient kubeconfig points at, under the estate's namespace
// name. Two clusters can hold a namespace of the same name, so that is a
// report about a different cluster that prints "correct".
func TestLiveClusterNamespaceOverridesTheNamespaceOnly(t *testing.T) {
	block := &configs.LiveRecordStore{Type: "kubernetes", Namespace: clusterNS, NamespaceSet: true}
	block.Kubernetes.Host = "https://records.example:6443"
	block.Kubernetes.ConfigContext = "records-cluster"
	live := &configs.Live{Estate: "alice", RecordStore: block}

	namespace, estate, rs, diags := liveClusterSubject(&arguments.LiveCluster{Namespace: "other-ns"}, live, false)
	if diags.HasErrors() {
		t.Fatalf("-namespace in a configuration directory was refused: %s", diags.Err())
	}
	if rs != block {
		t.Error("-namespace threw away the record_store block, so the report goes to whatever the ambient kubeconfig points at")
	}
	if namespace != "other-ns" {
		t.Errorf("namespace = %q, want the flag's own value", namespace)
	}
	if estate != "alice" {
		t.Errorf("estate = %q, want the live block's", estate)
	}

	// -estate still wins over the block's, and the block still supplies the
	// connection.
	if _, estate, rs, _ = liveClusterSubject(&arguments.LiveCluster{Namespace: "other-ns", Estate: "bob"}, live, false); estate != "bob" || rs != block {
		t.Errorf("estate = %q rs = %v, want bob and the block", estate, rs)
	}

	// Outside a configuration directory, or in one with no such block,
	// -namespace still answers from the ambient kubeconfig.
	if _, _, rs, diags = liveClusterSubject(&arguments.LiveCluster{Namespace: "other-ns"}, nil, false); rs != nil || diags.HasErrors() {
		t.Errorf("a namespace named outside a configuration directory: rs = %v, errors = %v", rs, diags.HasErrors())
	}
	if _, _, _, diags = liveClusterSubject(&arguments.LiveCluster{}, nil, false); !diags.HasErrors() {
		t.Error("no -namespace and no record_store \"kubernetes\" block was accepted")
	}
	local := &configs.Live{RecordStore: &configs.LiveRecordStore{Type: "local"}}
	if _, _, _, diags = liveClusterSubject(&arguments.LiveCluster{}, local, false); !diags.HasErrors() {
		t.Error("a record_store \"local\" directory was reported on as a cluster")
	}
}

// TestLiveClusterSaysWhichClusterItChecked is the other half of #1448: a
// report that named no cluster could not be told from a report about another
// one. The table and the JSON both carry it.
func TestLiveClusterSaysWhichClusterItChecked(t *testing.T) {
	block := &configs.LiveRecordStore{Type: "kubernetes"}
	block.Kubernetes.ConfigContext = "records-cluster"

	fromBlock := buildLiveClusterReport(clusterTargetForTest(), "alice", "apply", clusterFindings(), block)
	if fromBlock.Server != clusterServer || !fromBlock.FromConfig || fromBlock.Context != "records-cluster" {
		t.Errorf("the report does not carry where it went: %+v", fromBlock)
	}
	text := renderLiveClusterReport(fromBlock)
	for _, want := range []string{clusterServer, `config_context "records-cluster"`} {
		if !strings.Contains(text, want) {
			t.Errorf("the table does not say %q:\n%s", want, text)
		}
	}

	// A block with no config_context names the block instead, and the
	// ambient case says so rather than leaving the reader to guess.
	plain := renderLiveClusterReport(buildLiveClusterReport(clusterTargetForTest(), "alice", "apply", clusterFindings(), &configs.LiveRecordStore{Type: "kubernetes"}))
	if !strings.Contains(plain, `reached through the record_store "kubernetes" block`) {
		t.Errorf("the table does not say the block supplied the connection:\n%s", plain)
	}
	ambient := buildLiveClusterReport(clusterTargetForTest(), "alice", "apply", clusterFindings(), nil)
	if ambient.FromConfig {
		t.Error("a report with no record_store block claims one supplied its connection")
	}
	if !strings.Contains(renderLiveClusterReport(ambient), "ambient kubeconfig") {
		t.Errorf("the table does not say the connection was the ambient one:\n%s", renderLiveClusterReport(ambient))
	}

	raw, err := json.Marshal(fromBlock)
	if err != nil {
		t.Fatalf("marshalling the report: %v", err)
	}
	var back liveClusterReport
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshalling the report: %v", err)
	}
	if back.Server != clusterServer || !back.FromConfig || back.Context != "records-cluster" {
		t.Errorf("the JSON lost which cluster answered: %+v", back)
	}
}

// TestLiveClusterHelpSaysWhatAWarningDoesToTheExitStatus is #1448's D3. The
// warn/refuse split is a ruling and stays; the help said "Exits non-zero
// unless all four hold", which a Warned finding does not do.
func TestLiveClusterHelpSaysWhatAWarningDoesToTheExitStatus(t *testing.T) {
	warned := clusterFindings()
	warned[1] = staterecord.Finding{
		Setting: staterecord.ClusterReadIsolation, Outcome: staterecord.Warned, Found: "a concern, not a breach",
	}
	if r := buildLiveClusterReport(clusterTargetForTest(), "alice", "apply", warned, nil); !r.Correct {
		t.Fatal("the exit behaviour changed; this test is about the help text, not the ruling")
	}

	help := (&LiveClusterCommand{}).Help()
	if strings.Contains(help, "Exits non-zero unless all four hold.") {
		t.Error("the help still says a warning exits non-zero, which it does not")
	}
	if !strings.Contains(help, "warning") {
		t.Errorf("the help does not say what a warning does to the exit status:\n%s", help)
	}
}

// TestParseLiveCluster covers the flags and the one shape of argument this
// command refuses.
func TestParseLiveCluster(t *testing.T) {
	lc, diags := arguments.ParseLiveCluster([]string{"-namespace=ns", "-estate=e", "-plan-identity", "-json"})
	if diags.HasErrors() {
		t.Fatalf("a valid command line was refused: %s", diags.Err())
	}
	if lc.Namespace != "ns" || lc.Estate != "e" || !lc.PlanIdentity || !lc.JSON {
		t.Errorf("parsed %+v", lc)
	}

	if _, diags := arguments.ParseLiveCluster([]string{"some-namespace"}); !diags.HasErrors() {
		t.Error("a positional argument was accepted; -namespace is how a namespace is named")
	}

	lc, diags = arguments.ParseLiveCluster(nil)
	if diags.HasErrors() {
		t.Fatalf("no arguments at all was refused: %s", diags.Err())
	}
	if lc.PlanIdentity {
		t.Error("the default is the apply question, which is the stricter one")
	}
}

// TestLiveClusterHelpSaysWhatItCannotAnswer holds the help text to the thing
// a reader has to know before they trust a green: two of the four properties
// are not readable everywhere, and an unanswered one is not a pass.
func TestLiveClusterHelpSaysWhatItCannotAnswer(t *testing.T) {
	help := (&LiveClusterCommand{}).Help()
	for _, want := range []string{
		"-namespace", "-estate", "-plan-identity", "-json",
		"NOT_CHECKED", "not a pass", "allow_insecure", "Nothing is written",
		"SelfSubjectAccessReview",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("the help text does not mention %q:\n%s", want, help)
		}
	}
	if syn := (&LiveClusterCommand{}).Synopsis(); syn == "" || strings.Contains(syn, "bucket") {
		t.Errorf("the synopsis is wrong for this command: %q", syn)
	}
}
