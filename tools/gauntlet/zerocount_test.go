// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// zerocount_test.go is #1248, the sweep #1246 left behind after fixing
// #1204: twelve fail verdicts in seven estate scripts named a cause whose
// evidence could be a count of zero. "0 line(s) naming one", "no 'Error:'
// line found" and an empty item list were each followed by a sentence that
// diagnosed a mechanism anyway, so the verdict said the same thing however
// the behaviour changed.
//
// None of these branches fires in a passing run, so no gauntlet run
// exercises them. Same pattern as TestGreenfieldVerdictNamesOnlyTheRefusalItSaw:
// each verdict is a function in the committed script (or, for the two
// families copied across scripts, in live/e2e/lib/gauntlet.sh), and these
// tests drive it with a real sighting and with a zero. They are written
// from what a verdict promises - the cause it names is one the output
// showed - not from the implementation.

// verdictScriptFunc returns the text of a column-0 shell function in an
// estate script, after checking that the script's fail branch really calls
// it (callSite) - or this test would be driving dead code.
func verdictScriptFunc(t *testing.T, estate, name, callSite string) string {
	t.Helper()
	path := filepath.Join(testRoot(t), "live", "e2e", estate, "run.sh")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	script := string(b)
	if callSite != "" && !strings.Contains(script, callSite) {
		t.Fatalf("%s no longer composes its verdict through %s (want the call site %q); this test would be driving dead code", path, name, callSite)
	}
	if name == "" {
		return ""
	}
	lines := strings.Split(script, "\n")
	for i, ln := range lines {
		if ln != name+"() {" {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if lines[j] == "}" {
				return strings.Join(lines[i:j+1], "\n") + "\n"
			}
		}
		t.Fatalf("%s: %s has no closing brace at column 0", path, name)
	}
	t.Fatalf("%s does not define %s() at column 0", path, name)
	return ""
}

// driveVerdict sources the real library, defines fn (may be empty, for a
// library function), and runs call with stdin as its standard input, under
// set -euo pipefail: stricter than any estate script, so a count of zero
// that kills the function under -e is caught here too.
func driveVerdict(t *testing.T, fn, call, stdin string) string {
	t.Helper()
	lib := filepath.Join(testRoot(t), "live", "e2e", "lib", "gauntlet.sh")
	in := filepath.Join(t.TempDir(), "stdin.txt")
	if err := os.WriteFile(in, []byte(stdin), 0o600); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("set -euo pipefail\nsource %q\n%s\n%s < %q\n", lib, fn, call, in)
	out, err := runBash(script)
	if err != nil {
		t.Fatalf("bash: %v\n%s", err, out)
	}
	return string(out)
}

func wantIn(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("the verdict does not contain %q:\n%s", w, got)
		}
	}
}

func wantNotIn(t *testing.T, why, got string, unwanted ...string) {
	t.Helper()
	for _, u := range unwanted {
		if strings.Contains(got, u) {
			t.Errorf("%s - the verdict contains %q:\n%s", why, u, got)
		}
	}
}

// Sites 8-10: the greenfield NONEMPTY_ITEMS family, fixed once in the
// library and called from all three copies.
func TestReplanActionsVerdictNamesOnlyWhatThePlanProposed(t *testing.T) {
	for estate, ctx := range map[string]string{
		"corpus-alb-complete":          `gauntlet_stage greenfield fail "$(gauntlet_replan_actions_verdict "$INSTANCES objects were created`,
		"corpus-ec2-instance-complete": `gauntlet_stage greenfield fail "$(gauntlet_replan_actions_verdict "35 objects were created`,
		"corpus-autoscaling-complete":  `gauntlet_stage greenfield fail "$(gauntlet_replan_actions_verdict "$GREEN_N/$STOCK_N objects match by count`,
	} {
		verdictScriptFunc(t, estate, "", ctx)
	}
	call := `gauntlet_replan_actions_verdict "35 objects were created and the instance's own marker verified fine"`

	t.Run("a proposed create is reported with the wrong-marker diagnosis", func(t *testing.T) {
		got := driveVerdict(t, "", call,
			"  # aws_sqs_queue.this will be created\n  # aws_iam_role.x will be updated in-place\nPlan: 1 to add, 1 to change, 0 to destroy.\n")
		wantIn(t, got, "2 header(s)", "1 create", "1 update in-place", "aws_sqs_queue.this will be created", "wrong-marker-shaped", "the objects named above are the gap")
	})
	t.Run("updates alone are not diagnosed as a create", func(t *testing.T) {
		got := driveVerdict(t, "", call, "  # aws_lb.this will be updated in-place\nPlan: 0 to add, 1 to change, 0 to destroy.\n")
		wantIn(t, got, "0 create", "not the wrong-marker shape")
		wantNotIn(t, "an update was diagnosed as a create of something that exists", got, "A create proposed for something that already exists")
	})
	t.Run("no header at all names no object and no mechanism", func(t *testing.T) {
		got := driveVerdict(t, "", call, "Error: reading SQS Queue: connection refused\n")
		wantNotIn(t, "an empty item list was followed by a diagnosis", got,
			"A create proposed for something that already exists", "the objects named above are the gap", "real resource action on objects")
		wantIn(t, got, "names no object", "0 \"# ... will be\"", "Error: reading SQS Queue: connection refused", "35 objects were created")
	})
}

// Sites 5-7 and 11: the day2_remove shortfall, fixed once in the library.
func TestDestroyGapVerdictNamesOnlyAMissingType(t *testing.T) {
	for estate, site := range map[string]string{
		"corpus-autoscaling-complete":  `gauntlet_stage day2_remove fail "$(gauntlet_destroy_gap_verdict 'module\.default\.' "module.default" "$REMOVE_ORACLE_PLAN_OUT" aws_autoscaling_group`,
		"corpus-alb-complete":          `gauntlet_stage day2_remove fail "$(gauntlet_destroy_gap_verdict '' "aws_instance.other_renamed's block removal`,
		"corpus-ec2-instance-complete": `gauntlet_stage day2_remove fail "$(gauntlet_destroy_gap_verdict 'module\.ec2_complete\.' "module.ec2_complete" "$REMOVE_ORACLE_PLAN_OUT" <<< "$REMOVE_PLAN_OUT")`,
		"corpus-ecs-fargate":           `fail "$(gauntlet_destroy_gap_verdict 'module\.ecs_task_definition\.' "module.ecs_task_definition" "$REMOVE_ORACLE_PLAN_OUT" aws_iam_role_policy_attachment`,
	} {
		verdictScriptFunc(t, estate, "", site)
	}
	const oracle = "  # module.default.aws_autoscaling_group.this[0] will be destroyed\n" +
		"  # module.default.aws_launch_template.this[0] will be destroyed\n" +
		"  # module.vpc.aws_vpc.this[0] will be updated in-place\n" +
		"Plan: 0 to add, 1 to change, 2 to destroy.\n"
	call := func(suspect string) string {
		return fmt.Sprintf("ORACLE=\"$(cat <<'EOF'\n%sEOF\n)\"\n"+`gauntlet_destroy_gap_verdict 'module\.default\.' "module.default" "$ORACLE" %s "SUSPECT-REASON"`, oracle, suspect)
	}

	t.Run("a missing type is counted, and the suspect named only because it is that type", func(t *testing.T) {
		got := driveVerdict(t, "", call("aws_autoscaling_group"),
			"  # module.default.aws_launch_template.this[0] will be destroyed\nPlan: 0 to add, 0 to change, 1 to destroy.\n")
		wantIn(t, got, "destroys 1 of the 2", "aws_autoscaling_group x1", "aws_autoscaling_group is among the missing types: SUSPECT-REASON")
	})
	t.Run("a suspect that was destroyed is cleared, not named", func(t *testing.T) {
		got := driveVerdict(t, "", call("aws_autoscaling_group"),
			"  # module.default.aws_autoscaling_group.this[0] will be destroyed\n")
		wantIn(t, got, "aws_launch_template x1", "NOT among the missing types")
		wantNotIn(t, "the suspect was named although this run destroyed it", got, "SUSPECT-REASON")
	})
	t.Run("zero destroys names no type", func(t *testing.T) {
		got := driveVerdict(t, "", call("aws_autoscaling_group"),
			"No changes. Your infrastructure matches the configuration.\n")
		wantIn(t, got, "destroys 0 of the 2", "no destroy under module.default at all", `"No changes. Your infrastructure matches the configuration"`)
		wantNotIn(t, "a single culprit was nominated for a sweep that destroyed nothing", got,
			"SUSPECT-REASON", "aws_autoscaling_group", "most likely", "the missing address is")
	})
	t.Run("a renamed instance is compared by type", func(t *testing.T) {
		got := driveVerdict(t, "",
			"ORACLE=\"$(cat <<'EOF'\n  # aws_instance.other will be destroyed\n  # module.alb.aws_lb_target_group_attachment.this[\"ex-instance-other\"] will be destroyed\nEOF\n)\"\n"+
				`gauntlet_destroy_gap_verdict '' "the block" "$ORACLE" aws_lb_target_group_attachment "R"`,
			"  # aws_instance.other_renamed will be destroyed\n")
		wantIn(t, got, "destroys 1 of the 2", "aws_lb_target_group_attachment x1", "is among the missing types: R")
		wantNotIn(t, "a rename between the two roots read as a missing type", got, "aws_instance x")
	})
}

// Site 1.
func TestReferenceK8sMigrateVerdictNeedsTheUntaggableLine(t *testing.T) {
	fn := verdictScriptFunc(t, "reference-k8s", "migrate_no_label_verdict",
		`gauntlet_stage migrate fail "$(migrate_no_label_verdict "$SUMMARY_LINE" <<< "$APPROVE_OUT")"`)
	untaggable := strings.Repeat("  UNTAGGABLE  kubernetes_config_map.app: kubernetes_config_map has no tags argument in the provider's schema, so there is nowhere on it to carry an ownership marker.\n", 7)

	t.Run("the UNTAGGABLE lines are counted and #1073 named", func(t *testing.T) {
		got := driveVerdict(t, fn, `migrate_no_label_verdict "0 resource(s) newly stamped, 0 failed, 7 skipped"`, untaggable)
		wantIn(t, got, "classed 7 kubernetes_* instance(s) UNTAGGABLE", "#1073", "0 resource(s) newly stamped")
	})
	t.Run("a different shortfall does not name #1073's mechanism", func(t *testing.T) {
		got := driveVerdict(t, fn, `migrate_no_label_verdict "5 resource(s) newly stamped, 0 already stamped, 2 failed"`,
			"Error: patching configmap shard-1: connection reset\n")
		wantNotIn(t, "the label-surface mechanism was asserted with no UNTAGGABLE line in the output", got,
			"ratify.go's carrier", "classed 0", "one apply away")
		wantIn(t, got, "5 resource(s) newly stamped", "0 \"has no tags argument", "Error: patching configmap shard-1: connection reset")
	})
	t.Run("no summary line is said, not filled in", func(t *testing.T) {
		got := driveVerdict(t, fn, `migrate_no_label_verdict ""`, "")
		wantIn(t, got, "no summary line at all", `first Error: line: "none"`)
		wantNotIn(t, "a cause was named over an empty output", got, "ratify.go's carrier")
	})
}

// Site 4.
func TestReferenceK8sTestPlanVerdictDoesNotContradictItself(t *testing.T) {
	fn := verdictScriptFunc(t, "reference-k8s", "test_plan_verdict",
		`gauntlet_stage test_plan fail "$(test_plan_verdict "$IDS_OK" "$IDS_MISSING" <<< "$PLAN_OUT")"`)

	t.Run("an empty plan with a missing identity says so", func(t *testing.T) {
		got := driveVerdict(t, fn, `test_plan_verdict 0 " k8s-ref/deployment/web"`,
			"No changes. Your infrastructure matches the configuration.\n")
		wantIn(t, got, "identities confirmed: 0", "k8s-ref/deployment/web", "(No changes. Your infrastructure matches the configuration)")
		wantNotIn(t, "the verdict blames a label change in a plan that proposes none", got, "tofu-estate", "is not empty: No changes")
	})
	t.Run("label diff lines are counted before the label is blamed", func(t *testing.T) {
		got := driveVerdict(t, fn, `test_plan_verdict 1 ""`,
			"  # kubernetes_config_map.app will be updated in-place\n      ~ metadata {\n          ~ labels = {\n              + \"tofu-estate\" = \"reference-k8s\"\n\nPlan: 0 to add, 1 to change, 0 to destroy.\n")
		wantIn(t, got, "Plan: 0 to add, 1 to change, 0 to destroy", "1 diff line(s) write the tofu-estate label", "none missing")
	})
}

// Site 2.
func TestCertManagerCountVerdictNeedsItsSignatures(t *testing.T) {
	fn := verdictScriptFunc(t, "reference-k8s-cert-manager", "count_replan_verdict",
		`COUNT_VERDICT="$(count_replan_verdict "$A_RE_RC" "$NS" "$TOTAL_N" <<< "$A_RE")"`)

	t.Run("#1178's regression is reported half by half", func(t *testing.T) {
		got := driveVerdict(t, fn, `count_replan_verdict 1 cm 14`,
			"  # kubernetes_manifest.issuer_shard[0] will be created\n  # kubernetes_manifest.issuer_shard[1] will be created\n"+
				"  # kubernetes_manifest.orphan_issuer_cm_shard-0 will be destroyed\n  # kubernetes_manifest.orphan_issuer_cm_shard-1 will be destroyed\n"+
				"    the server refused the create: issuers.cert-manager.io \"shard-0\" already exists\nPlan: 2 to add, 0 to change, 2 to destroy.\n")
		wantIn(t, got, "CREATING 2 counted instance(s)", `the server refused the create: issuers.cert-manager.io "shard-0" already exists`,
			"(2 line(s) naming one)", "count on kubernetes_manifest specifically")
	})
	t.Run("none of the signatures names no mechanism", func(t *testing.T) {
		got := driveVerdict(t, fn, `count_replan_verdict 1 cm 14`,
			"Error: Failed to construct REST client\n")
		wantNotIn(t, "#1178's story was told over zero of its signatures", got,
			"CREATING", "no rejection line", "0 line(s) naming one", "count on kubernetes_manifest specifically")
		wantIn(t, got, "None of #1178's signatures appeared", "Error: Failed to construct REST client", "exit 1")
	})
}

// Site 3.
func TestAutoscalingRemoveApplyVerdictNeedsTheOrdering(t *testing.T) {
	fn := verdictScriptFunc(t, "corpus-autoscaling-complete", "remove_apply_failure_verdict",
		`fail "$(remove_apply_failure_verdict "$REMOVE_APPLY_RC" "$ORACLE_REMOVE_N" <<< "$REMOVE_APPLY_OUT")`)

	t.Run("launch template gone and ASG not is the ordering gap", func(t *testing.T) {
		got := driveVerdict(t, fn, `remove_apply_failure_verdict 1 2`,
			"module.default.aws_launch_template.this[0]: Destruction complete after 0s\n"+
				"Error: updating Auto Scaling Group (default): ValidationError: launch template not found\n")
		wantIn(t, got, "1 object(s) reported Destruction complete (module.default.aws_launch_template.this[0])", "destroy-order gap", "ValidationError")
	})
	t.Run("no Error: line names no cause", func(t *testing.T) {
		got := driveVerdict(t, fn, `remove_apply_failure_verdict 137 2`, "module.default.aws_autoscaling_group.this[0]: Destroying...\nKilled\n")
		wantNotIn(t, "the ordering gap was named on an exit code alone", got, "destroy-order gap", "no 'Error:' line found")
		wantIn(t, got, "exited 137", "0 object(s) reported Destruction complete (none)", "no Error: line at all")
	})
	t.Run("an error without the ordering quotes the error and names no gap", func(t *testing.T) {
		got := driveVerdict(t, fn, `remove_apply_failure_verdict 1 2`, "Error: deleting EC2 Launch Template: RequestLimitExceeded\n")
		wantNotIn(t, "the ordering gap was named with neither destruction in the output", got, "destroy-order gap between")
		wantIn(t, got, "RequestLimitExceeded", "0 launch template and 0 ASG destruction(s)")
	})
}

// Site 12.
func TestVpcNonemptyPlanVerdictNamesWhatItCounted(t *testing.T) {
	fn := verdictScriptFunc(t, "corpus-vpc-complete", "nonempty_plan_verdict",
		`fail "$(nonempty_plan_verdict <<< "$PLAN_OUT")"`)
	ecs := "  # module.vpc_endpoints.aws_vpc_endpoint.this[\"ecs\"] must be replaced\n      ~ network_interface_ids = [...] -> (known after apply)\n"

	t.Run("the ecs endpoint alone is floci#99", func(t *testing.T) {
		got := driveVerdict(t, fn, `nonempty_plan_verdict`, ecs+"Plan: 1 to add, 0 to change, 1 to destroy.\n")
		wantIn(t, got, "1 object(s) change", "lex00/floci#99")
		wantNotIn(t, "one object counted and others mentioned", got, "the other")
	})
	t.Run("five objects are not described as one", func(t *testing.T) {
		more := ""
		for i := 0; i < 4; i++ {
			more += fmt.Sprintf("  # module.vpc.aws_subnet.private[%d] will be updated in-place\n", i)
		}
		got := driveVerdict(t, fn, `nonempty_plan_verdict`, ecs+more)
		wantIn(t, got, "5 object(s) change", "module.vpc.aws_subnet.private[3] will be updated in-place", "the other 4 object(s) are not explained by it")
		wantNotIn(t, "five changes reported as one remainder", got, "What is left is")
	})
	t.Run("without the ecs endpoint floci#99 is not named", func(t *testing.T) {
		got := driveVerdict(t, fn, `nonempty_plan_verdict`, "  # module.vpc.aws_vpc.this[0] will be updated in-place\n")
		wantNotIn(t, "the floci story was told about an object not in the plan", got, "floci#99", `this["ecs"]`)
		wantIn(t, got, "1 object(s) change", "module.vpc.aws_vpc.this[0]", "No cause is named")
	})
}
