"""The fenced executor reports every spawned command to the event feed, so a
beat never has to remember to. These run with subprocess faked at the one
spawn site; nothing touches a cloud."""

from __future__ import annotations

import pathlib
import tempfile
import unittest
from unittest import mock

from tlmig import config, events, guard


class FakeProc:
    def __init__(self, returncode: int, stdout: str | None, stderr: str | None) -> None:
        self.returncode = returncode
        self.stdout = stdout
        self.stderr = stderr


class GuardFeedTest(unittest.TestCase):
    def setUp(self) -> None:
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.cfg = config.Config(run_id="t1", run_dir=pathlib.Path(tmp.name) / "run", binary="choudoufu")

    def test_a_captured_read_lands_in_the_feed_with_its_output(self) -> None:
        with mock.patch.object(guard.subprocess, "run", return_value=FakeProc(0, "354867293429\n", "")):
            res = guard.aws(self.cfg, "sts", "get-caller-identity", label="which account")
        self.assertTrue(res.ok)
        feed = events.read(self.cfg)
        self.assertEqual([e["kind"] for e in feed], ["cmd"])
        e = feed[0]
        self.assertEqual(e["argv"], ["aws", "sts", "get-caller-identity"])
        self.assertEqual(e["label"], "which account")
        self.assertEqual(e["returncode"], 0)
        self.assertIsNone(e["cwd"])
        self.assertEqual(pathlib.Path(e["stdout_path"]).read_text(), "354867293429\n")
        self.assertIn("aws sts get-caller-identity", self.cfg.transcript_path.read_text())

    def test_an_uncaptured_command_records_cwd_and_no_stdout_file(self) -> None:
        with mock.patch.object(guard.subprocess, "run", return_value=FakeProc(0, None, None)):
            guard.chdf(self.cfg, "init", "-input=false", cwd=str(self.cfg.run_dir), capture=False, label="init")
        e = events.read(self.cfg)[0]
        self.assertEqual(e["argv"], ["choudoufu", "init", "-input=false"])
        self.assertEqual(e["cwd"], str(self.cfg.run_dir))
        self.assertIsNone(e["stdout_path"])
        self.assertEqual(e["label"], "init")
        self.assertFalse((self.cfg.run_dir / "cmd").exists())

    def test_a_failed_command_is_in_the_feed_before_the_guard_raises(self) -> None:
        with mock.patch.object(guard.subprocess, "run", return_value=FakeProc(1, "", "AccessDenied")):
            with self.assertRaises(guard.GuardError):
                guard.aws(self.cfg, "iam", "list-roles")
        e = events.read(self.cfg)[0]
        self.assertEqual(e["returncode"], 1)
        self.assertEqual(e["label"], "")

    def test_the_feed_is_ordered_by_spawn(self) -> None:
        with mock.patch.object(guard.subprocess, "run", return_value=FakeProc(0, "", "")):
            guard.aws(self.cfg, "iam", "list-roles", label="first")
            guard.aws(self.cfg, "iam", "list-policies", label="second")
        self.assertEqual([e["label"] for e in events.read(self.cfg)], ["first", "second"])


class TheRunPrefixFence(unittest.TestCase):
    """What the fence actually checks, verified rather than assumed.

    v0.15.0 retired the stamp and the module_prefix evaluator symbol (#644,
    PR #944), and the README leans on the run-prefix discipline for the
    destructive fence, so it is worth pinning what that discipline is made
    of: the NAME a destructive call names, and nothing else. Not a marker
    tag, not anything choudoufu writes.
    """

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.cfg = config.Config(run_id="9f3a1c", run_dir=pathlib.Path(self.tmp.name) / "run", binary="choudoufu")

    def test_a_name_carrying_this_runs_prefix_passes(self):
        guard.assert_owned_name(self.cfg, "tlmig-9f3a1c-team-a-role")

    def test_another_runs_resource_is_refused_however_it_is_marked(self):
        """Same example, same markers, different run. The fence has to refuse
        it, and does so on the name alone - it never reads a tag."""
        with self.assertRaises(guard.GuardError) as cm:
            guard.assert_owned_name(self.cfg, "tlmig-0b0b0b-team-a-role")
        self.assertIn("tlmig-9f3a1c", str(cm.exception))

    def test_a_destructive_aws_call_without_a_named_target_is_refused(self):
        with self.assertRaises(guard.GuardError):
            guard.aws(self.cfg, "iam", "delete-role", "--role-name", "x", destructive=True)

    def test_a_destructive_choudoufu_command_outside_the_run_tree_is_refused(self):
        with self.assertRaises(guard.GuardError):
            guard._assert_in_run(self.cfg, str(pathlib.Path(self.tmp.name)))
        with self.assertRaises(guard.GuardError):
            guard._assert_in_run(self.cfg, None)
        # inside the run tree is fine
        wd = self.cfg.workdir("e")
        wd.mkdir(parents=True)
        guard._assert_in_run(self.cfg, str(wd))


if __name__ == "__main__":
    unittest.main()
