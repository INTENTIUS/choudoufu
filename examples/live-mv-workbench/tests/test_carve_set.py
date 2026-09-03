"""read_carve_set and preview_carve, with the cloud faked at the guard
boundary the same way the other govern tests fake it: every plan, tag-index
read and live-mv still goes through guard's own functions, so the assembly is
what is tested, not a stand-in."""

from __future__ import annotations

import json
import pathlib
import tempfile
import unittest
from unittest import mock

from tlmig import config, events, govern, guard

CLEAN = "No changes. Your infrastructure matches the configuration.\n"
DIRTY = "  # aws_iam_role_policy.team_a_inline will be destroyed\n\nPlan: 0 to add, 0 to change, 1 to destroy.\n"


def _result(stdout: str, rc: int = 0, stderr: str = "") -> guard.Result:
    return guard.Result(argv=["fake"], returncode=rc, stdout=stdout, stderr=stderr, seconds=0.0)


def _dry_run(frm: str, to: str, addr: str) -> str:
    return (
        "\nWould move one live resource into this estate. Nothing was written (-dry-run).\n\n"
        f"  from estate    {frm}\n  to estate      {to}\n"
        f'  tofu-estate    "{frm}" -> "{to}"\n'
        f"  resource type  aws_iam_policy\n  live ID        arn:aws:iam::354867293429:policy/{addr}\n"
        f"  old address    {addr}\n  new address    {addr}\n"
        f'  tofu-address   "{addr}" -> "{addr}"\n'
        "  found by       listing every aws_iam_policy and reading its ownership markers\n\n"
        "Rerun without -dry-run to write it.\n"
    )


class CarveSetHarness(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.cfg = config.Config(run_id="cs01", run_dir=pathlib.Path(self.tmp.name) / "run", binary="choudoufu")
        self.carve = pathlib.Path(self.tmp.name) / "carve.json"
        self.carve.write_text(json.dumps({"moves": [
            {"address": "aws_iam_role.team_a", "from_estate": "mono", "to_estate": "team-a",
             "children": ["aws_iam_role_policy.team_a_inline"]},
            {"address": "aws_iam_policy.team_a", "from_estate": "mono", "to_estate": "team-a"},
        ]}))

    def _index_aws(self, present: dict[str, str]):
        """Fake the tag-index read: `present` maps a tofu-address to the
        tofu-estate the index reports for it. list-roles returns none, so the
        landing is decided by the tagging index alone."""
        def fake_aws(cfg, *args, **kw):
            if "get-resources" in args:
                estate = args[args.index("--tag-filters") + 1].split("Values=")[1]
                items = [
                    {"ResourceARN": f"arn:aws:x:::{addr}",
                     "Tags": [{"Key": "tofu-estate", "Value": est}, {"Key": "tofu-address", "Value": addr}]}
                    for addr, est in present.items() if est == estate
                ]
                return _result(json.dumps({"ResourceTagMappingList": items}))
            if "list-roles" in args:
                return _result('{"Roles":[]}')
            raise AssertionError(f"unexpected aws call {args}")
        return fake_aws

    def test_clean_set_both_landed_is_ok_and_emits_per_move_and_set(self):
        present = {"aws_iam_role.team_a": "team-a", "aws_iam_policy.team_a": "team-a"}
        with mock.patch.object(guard, "aws", self._index_aws(present)), \
             mock.patch.object(guard, "chdf", lambda cfg, *a, **kw: _result(CLEAN)):
            v = govern.read_carve_set(self.cfg, self.carve)
        self.assertTrue(v.ok)
        feed = events.read(self.cfg)
        verdicts = {e["name"]: e["ok"] for e in feed if e["kind"] == "verdict"}
        self.assertTrue(verdicts["carve:aws_iam_role.team_a"])
        self.assertTrue(verdicts["carve:aws_iam_policy.team_a"])
        self.assertTrue(verdicts["carve-set"])

    def test_a_tag_that_did_not_land_fails_the_set(self):
        present = {"aws_iam_role.team_a": "team-a"}  # the policy never landed
        with mock.patch.object(guard, "aws", self._index_aws(present)), \
             mock.patch.object(guard, "chdf", lambda cfg, *a, **kw: _result(CLEAN)):
            v = govern.read_carve_set(self.cfg, self.carve)
        self.assertFalse(v.ok)
        stuck = [m for m in v.per_move if not m.landed]
        self.assertEqual([m.address for m in stuck], ["aws_iam_policy.team_a"])
        self.assertFalse({e["name"]: e["ok"] for e in events.read(self.cfg) if e["kind"] == "verdict"}["carve-set"])

    def test_a_dirty_destination_plan_fails_the_set(self):
        present = {"aws_iam_role.team_a": "team-a", "aws_iam_policy.team_a": "team-a"}
        def plan_by_estate(cfg, *a, **kw):
            # a plan runs in the estate's workdir; team-a comes back dirty
            cwd = kw.get("cwd", "")
            return _result(DIRTY if "team-a" in cwd else CLEAN)
        with mock.patch.object(guard, "aws", self._index_aws(present)), \
             mock.patch.object(guard, "chdf", plan_by_estate):
            v = govern.read_carve_set(self.cfg, self.carve)
        self.assertFalse(v.ok)
        self.assertFalse(v.all_clean)
        self.assertTrue(v.all_landed)

    def test_preview_emits_one_event_per_move_with_the_tag_writes(self):
        def fake_chdf(cfg, *a, **kw):
            # a = ("live-mv","-dry-run","-no-color","-from-estate",frm,addr,addr)
            frm, addr = a[4], a[5]
            return _result(_dry_run(frm, "team-a", addr))
        with mock.patch.object(guard, "chdf", fake_chdf):
            previews = govern.preview_carve(self.cfg, self.carve)
        self.assertEqual(len(previews), 2)
        self.assertTrue(all(p.ok and not p.written for p in previews))
        pv = [e for e in events.read(self.cfg) if e["kind"] == "preview"]
        self.assertEqual(len(pv), 2)
        first = pv[0]
        self.assertEqual(first["tag_writes"][0], {"key": "tofu-estate", "from": "mono", "to": "team-a"})
        self.assertIsNone(first["refusal"])
        self.assertIn("aws_iam_role_policy.team_a_inline", first["children"])

    def test_preview_captures_a_refusal_and_marks_it_not_ok(self):
        refusal = "\nError: Address not declared in this estate\n\n  aws_iam_policy.team_a is not declared in estate team-a.\n"
        def fake_chdf(cfg, *a, **kw):
            return _result("", rc=1, stderr=refusal) if a[5] == "aws_iam_policy.team_a" else _result(_dry_run(a[4], "team-a", a[5]))
        with mock.patch.object(guard, "chdf", fake_chdf):
            previews = govern.preview_carve(self.cfg, self.carve)
        refused = [p for p in previews if not p.ok]
        self.assertEqual([p.address for p in refused], ["aws_iam_policy.team_a"])
        self.assertEqual(refused[0].refusal.summary, "Address not declared in this estate")
        ev = {e["address"]: e["refusal"] for e in events.read(self.cfg) if e["kind"] == "preview"}
        self.assertIsNone(ev["aws_iam_role.team_a"])
        self.assertEqual(ev["aws_iam_policy.team_a"]["summary"], "Address not declared in this estate")


if __name__ == "__main__":
    unittest.main()
