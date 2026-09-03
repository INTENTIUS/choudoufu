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


if __name__ == "__main__":
    unittest.main()
