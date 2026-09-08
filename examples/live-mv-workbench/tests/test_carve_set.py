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


def _dry_run(frm: str, to: str, addr: str, followers: list | None = None) -> str:
    """The document `live-mv -json -dry-run` prints for one cross-estate move,
    MarshalIndent'd the way views.StatelessMvJSONHuman renders it."""
    doc = {
        "resource": {"type": "aws_iam_policy", "live_id": f"arn:aws:iam::354867293429:policy/{addr}"},
        "from": {"estate": frm, "address": addr, "marker": addr},
        "to": {"estate": to, "address": addr, "marker": addr},
        "dry_run": True, "written": False, "verified": False, "found_by": "LIST",
    }
    if followers:
        doc["followers"] = followers
    return json.dumps(doc, indent=2) + "\n"


def _refusal_doc(frm: str, to: str, addr: str, summary: str, detail: str, code: str = "") -> str:
    """The document a refused run prints: -json renders one on every path past
    argument parsing, so a refusal needs no second parser. res is nil on every
    refusal path in internal/command/live_mv.go, so the endpoints carry the two
    addresses the command line named and no estate at all."""
    doc = {"from": {"address": addr}, "to": {"address": addr},
           "dry_run": True, "written": False, "verified": False,
           "refusal": {"summary": summary, "detail": detail}}
    if code:
        doc["refusal"]["code"] = code
    return json.dumps(doc, indent=2) + "\n"


def _mv_args(a: tuple) -> tuple[str, str, str]:
    """(from-estate, old address, new address) out of a live-mv argv, read by
    flag rather than by position so adding one does not silently shift them."""
    args = list(a)
    frm = args[args.index("-from-estate") + 1]
    positional = [x for i, x in enumerate(args) if not x.startswith("-") and args[i - 1] != "-from-estate"]
    return frm, positional[1], positional[2]


class CarveSetHarness(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.cfg = config.Config(run_id="cs01", run_dir=pathlib.Path(self.tmp.name) / "run", binary="choudoufu")
        self.carve = pathlib.Path(self.tmp.name) / "carve.json"
        self.carve.write_text(json.dumps({"moves": [
            {"address": "aws_iam_role.team_a", "from": "mono", "to": "team-a",
             "children": ["aws_iam_role_policy.team_a_inline"]},
            {"address": "aws_iam_policy.team_a", "from": "mono", "to": "team-a"},
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

    def test_preview_asks_for_the_document(self):
        """-json, not the human report: the reconstruction the workbench used
        to do is what internal/command/views/live_mv.go names as the reason
        -json exists (#791)."""
        seen = []
        def fake_chdf(cfg, *a, **kw):
            seen.append(list(a))
            frm, old, new = _mv_args(a)
            return _result(_dry_run(frm, "team-a", old))
        with mock.patch.object(guard, "chdf", fake_chdf):
            govern.preview_carve(self.cfg, self.carve)
        for argv in seen:
            self.assertEqual(argv[0], "live-mv")
            self.assertIn("-json", argv)
            self.assertIn("-dry-run", argv)

    def test_preview_emits_one_event_per_move_with_the_tag_writes(self):
        def fake_chdf(cfg, *a, **kw):
            frm, old, new = _mv_args(a)
            followers = [{"address": "aws_iam_role_policy.team_a_inline", "type": "aws_iam_role_policy"}] \
                if old == "aws_iam_role.team_a" else None
            return _result(_dry_run(frm, "team-a", old, followers=followers))
        with mock.patch.object(guard, "chdf", fake_chdf):
            previews = govern.preview_carve(self.cfg, self.carve)
        self.assertEqual(len(previews), 2)
        self.assertTrue(all(p.ok and not p.written for p in previews))
        pv = [e for e in events.read(self.cfg) if e["kind"] == "preview"]
        self.assertEqual(len(pv), 2)
        first = pv[0]
        self.assertEqual(first["tag_writes"][0], {"key": "tofu-estate", "from": "mono", "to": "team-a"})
        self.assertIsNone(first["refusal"])
        self.assertEqual(first["found_by"], "LIST")
        self.assertIn("aws_iam_role_policy.team_a_inline", first["children"])

    def test_preview_captures_a_refusal_and_marks_it_not_ok(self):
        def fake_chdf(cfg, *a, **kw):
            frm, old, new = _mv_args(a)
            if old == "aws_iam_policy.team_a":
                # a refusal still prints its document, and exits nonzero
                return _result(_refusal_doc(frm, "team-a", old,
                                            "Address not declared in this estate",
                                            "aws_iam_policy.team_a is not declared in estate team-a.",
                                            code="destination_not_declared"), rc=1)
            return _result(_dry_run(frm, "team-a", old))
        with mock.patch.object(guard, "chdf", fake_chdf):
            previews = govern.preview_carve(self.cfg, self.carve)
        refused = [p for p in previews if not p.ok]
        self.assertEqual([p.address for p in refused], ["aws_iam_policy.team_a"])
        self.assertEqual(refused[0].refusal.summary, "Address not declared in this estate")
        self.assertEqual(refused[0].refusal.code, "destination_not_declared")
        # the refused move claims no tag write: the document reported no
        # endpoints to write between, and the plan's own estates are not a
        # licence to invent one
        self.assertEqual(refused[0].tag_writes, ())
        ev = {e["address"]: e["refusal"] for e in events.read(self.cfg) if e["kind"] == "preview"}
        self.assertIsNone(ev["aws_iam_role.team_a"])
        self.assertEqual(ev["aws_iam_policy.team_a"]["summary"], "Address not declared in this estate")
        self.assertEqual(ev["aws_iam_policy.team_a"]["code"], "destination_not_declared")


if __name__ == "__main__":
    unittest.main()
