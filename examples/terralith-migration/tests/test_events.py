import json
import pathlib
import tempfile
import unittest
from unittest import mock

from tlmig import config, events, govern, guard


def _cfg(d: str) -> config.Config:
    return config.Config(run_id="ev01", run_dir=pathlib.Path(d), binary="choudoufu")


class Feed(unittest.TestCase):
    def test_every_line_carries_the_envelope_and_the_phase(self):
        with tempfile.TemporaryDirectory() as d:
            cfg = _cfg(d)
            events.phase(cfg, "setup", "start", title="stand the monolith up")
            events.note(cfg, "hello")
            events.cmd(cfg, ["aws", "sts", "get-caller-identity"], None, 0, 0.12345, stdout="123\n")
            events.phase(cfg, "setup", "end")
            events.note(cfg, "between beats")
            lines = events.read(cfg)
        self.assertEqual([l["kind"] for l in lines], ["phase", "note", "cmd", "phase", "note"])
        self.assertTrue(all(l["run_id"] == "ev01" and l["ts"].endswith("+00:00") for l in lines))
        self.assertEqual([l["phase"] for l in lines], ["setup", "setup", "setup", "setup", None])
        self.assertEqual(lines[2]["seconds"], 0.123)
        self.assertTrue(lines[2]["stdout_path"].endswith("cmd/0001.out"))

    def test_captured_stdout_lands_in_a_file_not_the_feed(self):
        with tempfile.TemporaryDirectory() as d:
            cfg = _cfg(d)
            events.cmd(cfg, ["choudoufu", "plan"], "/x", 0, 1.0, stdout="No changes.\n")
            events.cmd(cfg, ["choudoufu", "plan"], "/y", 0, 1.0, stdout="Plan: 1 to add\n")
            lines = events.read(cfg)
            self.assertEqual(pathlib.Path(lines[0]["stdout_path"]).read_text(), "No changes.\n")
            self.assertEqual(pathlib.Path(lines[1]["stdout_path"]).name, "0002.out")
            self.assertNotIn("No changes", (cfg.run_dir / "events.jsonl").read_text())

    def test_append_only(self):
        with tempfile.TemporaryDirectory() as d:
            cfg = _cfg(d)
            events.note(cfg, "one")
            first = (cfg.run_dir / "events.jsonl").read_text()
            events.note(cfg, "two")
            self.assertTrue((cfg.run_dir / "events.jsonl").read_text().startswith(first))

    def test_bad_phase_status_is_refused(self):
        with tempfile.TemporaryDirectory() as d:
            with self.assertRaises(ValueError):
                events.phase(_cfg(d), "x", "middle")

    def test_dataclasses_serialize(self):
        with tempfile.TemporaryDirectory() as d:
            cfg = _cfg(d)
            r = guard.Result(argv=["a"], returncode=0, stdout="", stderr="", seconds=0.0)
            events.verdict(cfg, "x", r)
            self.assertEqual(events.read(cfg)[0]["verdict"]["argv"], ["a"])


class Inventory(unittest.TestCase):
    def test_tagged_plus_roles_by_prefix(self):
        def fake_aws(cfg, *args, **kw):
            if "get-resources" in args:
                return guard.Result(["x"], 0, json.dumps({"ResourceTagMappingList": [
                    {"ResourceARN": "arn:aws:logs:us-east-1:1:log-group:/tlmig-ev01/team-a/svc-1",
                     "Tags": [{"Key": "tofu-estate", "Value": "tlmig-ev01-team-a"}, {"Key": "tofu-address", "Value": "aws_cloudwatch_log_group.svc[0]"}]}]}), "", 0.0)
            if "list-roles" in args:
                return guard.Result(["x"], 0, json.dumps({"Roles": [
                    {"RoleName": "tlmig-ev01-team-a-role", "Arn": "arn:aws:iam::1:role/tlmig-ev01-team-a-role"},
                    {"RoleName": "tlmig-ev01-team-b-role", "Arn": "arn:aws:iam::1:role/tlmig-ev01-team-b-role"},
                    {"RoleName": "prod-admin", "Arn": "arn:aws:iam::1:role/prod-admin"}]}), "", 0.0)
            if "list-role-tags" in args:
                role = args[args.index("--role-name") + 1]
                est = "tlmig-ev01-team-a" if role.endswith("team-a-role") else "tlmig-ev01-team-b"
                return guard.Result(["x"], 0, json.dumps({"Tags": [{"Key": "tofu-estate", "Value": est}, {"Key": "tofu-address", "Value": "aws_iam_role.team"}]}), "", 0.0)
            raise AssertionError(args)
        with tempfile.TemporaryDirectory() as d, mock.patch.object(guard, "aws", fake_aws):
            cfg = _cfg(d)
            items = govern.read_inventory(cfg, "tlmig-ev01-team-a")
            feed = events.read(cfg)
        self.assertEqual([i["type"] for i in items], ["iam:role", "logs:log-group"])
        self.assertEqual(items[0]["address"], "aws_iam_role.team")
        self.assertEqual(feed[-1]["kind"], "inventory")
        self.assertEqual(feed[-1]["estate"], "tlmig-ev01-team-a")
        self.assertEqual(len(feed[-1]["items"]), 2)


if __name__ == "__main__":
    unittest.main()
