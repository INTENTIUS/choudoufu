"""The approval gate: live/GAUNTLET.md stage 12, graded.

The stage's own words are the oracle: "`plan -out` followed by
`apply <planfile>` applies when the world has not moved and refuses, naming
the mismatch, when it has", and its Break line is "apply the planfile after a
mutation and expect success; the run must refuse". So every test below that
grades a pass has a sibling that removes exactly one of the four signs and
asserts the verdict turns red - a gate that cannot fail is not a gate.

The texts are the engine's own bytes: the refusal was produced by choudoufu
v0.15.0 against floci, applying a plan file after `aws logs
put-retention-policy` moved the log group out from under it.
"""

from __future__ import annotations

import json
import pathlib
import tempfile
import unittest
from unittest import mock

from tlmig import approval, config, env, events, govern, guard

PLAN_OUT = """
Terraform used the selected providers to generate the following execution plan.

Plan: 0 to add, 1 to change, 0 to destroy.

Saved the plan to: approved.tfplan

To perform exactly these actions, run the following command to apply:
    choudoufu apply "approved.tfplan"
"""

# Verbatim from a v0.15.0 run against floci.
REFUSAL = """
Error: The approved plan no longer matches the live system

This apply read the live system and planned against what it found, the way
every live-markers run does. The result is not the plan "approved.tfplan"
describes, so the approval that file carries does not cover what this apply
would do, and it is refused before anything changes.

Both plans make this change to this live object, and disagree about the
values it writes:
  aws_cloudwatch_log_group.team_b_0  Update  /tlmig-abc123/team-b/svc-0
      before.retention_in_days

Exit status 3 says exactly this: send it back to review. Nothing was applied.
"""

APPLIED = """
Apply complete! Resources: 0 added, 1 changed, 0 destroyed.
"""


class ReadingTheEnginesText(unittest.TestCase):
    def test_the_plan_file_is_read_from_what_plan_said_it_wrote(self):
        self.assertEqual(approval.saved_planfile(PLAN_OUT), "approved.tfplan")
        self.assertEqual(approval.saved_planfile("Plan: 0 to add, 0 to change, 0 to destroy.\n"), "")

    def test_applied_counts(self):
        self.assertEqual(approval.applied_counts(APPLIED), (0, 1, 0))
        self.assertIsNone(approval.applied_counts(REFUSAL))

    def test_a_refusal_needs_all_three_signs(self):
        addr = "aws_cloudwatch_log_group.team_b_0"
        self.assertTrue(approval.refused(REFUSAL, 3, addr))
        # the summary without exit 3 is a message, not a refusal a pipeline
        # can branch on
        self.assertFalse(approval.refused(REFUSAL, 0, addr))
        # exit 3 without the summary is some other failure
        self.assertFalse(approval.refused("Error: something else\n", 3, addr))
        # the refusal of a different object is not this move's refusal
        self.assertFalse(approval.refused(REFUSAL, 3, "aws_cloudwatch_log_group.team_c_2"))

    def test_set_retention_edits_one_block_and_refuses_a_name_it_cannot_find(self):
        hcl = ('resource "aws_cloudwatch_log_group" "team_b_0" {\n  name = "/x/0"\n  retention_in_days = 1\n}\n\n'
               'resource "aws_cloudwatch_log_group" "team_b_1" {\n  name = "/x/1"\n  retention_in_days = 1\n}\n')
        out = approval.set_retention(hcl, "team_b_0", 3)
        self.assertIn('"team_b_0" {\n  name = "/x/0"\n  retention_in_days = 3\n}', out)
        self.assertIn('"team_b_1" {\n  name = "/x/1"\n  retention_in_days = 1\n}', out)
        with self.assertRaises(ValueError):
            approval.set_retention(hcl, "team_b_9", 3)


def _verdict(**over) -> approval.GateVerdict:
    base = dict(estate="tlmig-abc123-team-b", address="aws_cloudwatch_log_group.team_b_0",
                planfile="approved.tfplan", planfile_bytes=4367,
                drifted_exit=3, drifted_refused=True, drifted_named=True,
                restored_exit=0, restored_counts=(0, 1, 0),
                approved_value="3", live_value="3")
    return approval.GateVerdict(**{**base, **over})


class TheVerdict(unittest.TestCase):
    def test_all_four_signs_is_the_only_pass(self):
        self.assertTrue(_verdict().ok)

    def test_stage_12s_own_break_line_turns_it_red(self):
        """"Apply the planfile after a mutation and expect success; the run
        must refuse." Expect success, and the verdict must fail."""
        self.assertFalse(_verdict(drifted_exit=0, drifted_refused=False, drifted_named=False).ok)

    def test_a_refusal_that_names_something_else_is_not_this_ones(self):
        self.assertFalse(_verdict(drifted_named=False).ok)

    def test_no_inverted_control_is_not_a_pass(self):
        """A gate that refuses every plan file would pass the first half. The
        identical file has to apply once the world is put back."""
        self.assertFalse(_verdict(restored_exit=3, restored_counts=None).ok)
        self.assertFalse(_verdict(restored_counts=(0, 0, 0)).ok)

    def test_the_value_is_read_back_from_the_account_not_from_apply_complete(self):
        self.assertFalse(_verdict(live_value="1").ok)
        self.assertFalse(_verdict(live_value="").ok)

    def test_a_plan_that_wrote_no_file_is_not_a_pass(self):
        self.assertFalse(_verdict(planfile="", planfile_bytes=0).ok)


class TheWalk(unittest.TestCase):
    """The whole gate with the cloud faked at the guard boundary, so the
    sequence is what is tested and not a stand-in for it."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.cfg = config.Config(run_id="abc123", run_dir=pathlib.Path(self.tmp.name) / "run", binary="choudoufu")
        wd = self.cfg.workdir("tlmig-abc123-team-b")
        wd.mkdir(parents=True)
        (wd / "main.tf").write_text(
            'resource "aws_cloudwatch_log_group" "team_b_0" {\n'
            '  name              = "/tlmig-abc123/team-b/svc-0"\n  retention_in_days = 1\n}\n')
        (wd / "approved.tfplan").write_bytes(b"x" * 4367)
        self.live = {"retention": "1"}

    def _chdf(self, drift_rc=3, drift_text=REFUSAL, restore_text=APPLIED):
        calls = []
        def fake(cfg, *a, cwd="", capture=False, check=True, label="", **kw):
            calls.append(list(a))
            if a[0] == "plan":
                return guard.Result(argv=list(a), returncode=0, stdout=PLAN_OUT, stderr="", seconds=0.1)
            # apply <planfile>: which one depends on where the world stands
            if self.live["retention"] != "1":
                return guard.Result(argv=list(a), returncode=drift_rc, stdout=drift_text, stderr="", seconds=0.1)
            return guard.Result(argv=list(a), returncode=0, stdout=restore_text, stderr="", seconds=0.1)
        return calls, fake

    def _aws(self):
        def fake(cfg, *a, check=True, label="", **kw):
            if "put-retention-policy" in a:
                self.live["retention"] = a[a.index("--retention-in-days") + 1]
                return guard.Result(argv=list(a), returncode=0, stdout="", stderr="", seconds=0.0)
            if "describe-log-groups" in a:
                # the approved apply landed: the account reports the approved value
                return guard.Result(argv=list(a), returncode=0, stdout="3\n", stderr="", seconds=0.0)
            raise AssertionError(f"unexpected aws call {a}")
        return fake

    def test_the_gate_holds_and_the_sequence_is_the_stages(self):
        calls, chdf = self._chdf()
        with mock.patch.object(guard, "chdf", chdf), mock.patch.object(guard, "aws", self._aws()):
            v = govern.approval_gate(self.cfg, "tlmig-abc123-team-b", "team_b_0", "/tlmig-abc123/team-b/svc-0")
        self.assertTrue(v.ok, v.lines())
        # plan -out first, then two applies of the file it named
        self.assertEqual([c[0] for c in calls], ["plan", "apply", "apply"])
        self.assertIn(f"-out={env.PLANFILE}", calls[0])
        self.assertIn("approved.tfplan", calls[1])
        self.assertEqual(calls[1], calls[2], "the inverted control must apply the IDENTICAL file")
        # the config carries the approved change
        self.assertIn("retention_in_days = 3",
                      (self.cfg.workdir("tlmig-abc123-team-b") / "main.tf").read_text())
        verdicts = [e for e in events.read(self.cfg) if e["kind"] == "verdict" and e["name"] == "plan-approval"]
        self.assertEqual(len(verdicts), 1)
        self.assertTrue(verdicts[0]["ok"])

    def test_stage_12s_break_line_run_literally(self):
        """Apply the planfile after the mutation and have it succeed. The
        verdict must be false and the event must say so."""
        calls, chdf = self._chdf(drift_rc=0, drift_text=APPLIED)
        with mock.patch.object(guard, "chdf", chdf), mock.patch.object(guard, "aws", self._aws()):
            v = govern.approval_gate(self.cfg, "tlmig-abc123-team-b", "team_b_0", "/tlmig-abc123/team-b/svc-0")
        self.assertFalse(v.ok)
        self.assertFalse(v.drifted_refused)
        self.assertIn("did NOT refuse", " ".join(v.lines()))
        verdicts = [e for e in events.read(self.cfg) if e["kind"] == "verdict" and e["name"] == "plan-approval"]
        self.assertFalse(verdicts[0]["ok"])

    def test_the_out_of_band_write_passes_the_run_prefix_fence(self):
        """The drift is a real AWS write, so it names its target and the fence
        checks it, like every other write this example makes."""
        with self.assertRaises(guard.GuardError):
            govern._set_retention_live(self.cfg, "/someone-elses/log-group", 5)


class ApplyGoesThroughTheGate(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.cfg = config.Config(run_id="ap01", run_dir=pathlib.Path(self.tmp.name) / "run", binary="choudoufu")
        self.cfg.workdir("e").mkdir(parents=True)

    def test_env_apply_plans_to_a_file_and_applies_that_file(self):
        """Not `apply -auto-approve`: the plan a reader read is the artifact
        the apply consumes, which is the shape stage 12 measures."""
        calls = []
        def fake(cfg, *a, cwd="", destructive=False, **kw):
            calls.append((list(a), destructive))
            return guard.Result(argv=list(a), returncode=0, stdout="", stderr="", seconds=0.0)
        with mock.patch.object(guard, "chdf", fake):
            env.apply(self.cfg, "e")
        self.assertEqual([c[0][0] for c in calls], ["plan", "apply"])
        self.assertIn(f"-out={env.PLANFILE}", calls[0][0])
        self.assertFalse(calls[0][1], "the plan is a read")
        self.assertEqual(calls[1][0][-1], env.PLANFILE)
        self.assertNotIn("-auto-approve", calls[1][0])
        self.assertTrue(calls[1][1], "the apply is destructive and confirmed")
        manifest = json.loads(self.cfg.manifest_path.read_text())
        self.assertEqual([e["estate"] for e in manifest["estates"]], ["e"])


if __name__ == "__main__":
    unittest.main()
