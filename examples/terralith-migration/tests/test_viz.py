"""The renderer is a function of a run directory. These tests replay the
synthetic run under tests/fixtures/sample-run, which walks every phase the
beats emit, and check the picture the room would see at each boundary."""
from __future__ import annotations

import json
import pathlib
import tempfile
import unittest

from tlmig import viz

FIXTURE = pathlib.Path(__file__).parent / "fixtures" / "sample-run"


def short(state: viz.RunState, address: str) -> str:
    return viz._short_estate(state, state.estate_of(address))


class DeclaredResources(unittest.TestCase):
    def test_come_from_the_configs(self):
        state = viz.load_run(FIXTURE, upto=0)
        self.assertEqual(len(state.resources), 21)
        self.assertEqual([viz._short_estate(state, e) for e in state.estates], ["monolith", "team-a", "team-b", "team-c"])
        inline = state.resources["aws_iam_role_policy.team_a_inline"]
        self.assertFalse(inline.taggable)
        self.assertEqual(inline.parent, "aws_iam_role.team_a")


class TheMap(unittest.TestCase):
    def test_follows_the_live_tag_phase_by_phase(self):
        b = viz.phase_boundaries(FIXTURE)
        self.assertEqual(list(b), ["setup", "slow-plan", "decompose", "fast-plan", "carve", "guard"])

        after_setup = viz.load_run(FIXTURE, upto=b["setup"])
        self.assertEqual(short(after_setup, "aws_iam_role.team_a"), "monolith")
        self.assertEqual(short(after_setup, "aws_cloudwatch_log_group.team_c_2"), "monolith")

        after_decompose = viz.load_run(FIXTURE, upto=b["decompose"])
        self.assertEqual(short(after_decompose, "aws_iam_role.team_a"), "team-a")
        self.assertEqual(short(after_decompose, "aws_iam_role_policy.team_a_inline"), "team-a")

        after_carve = viz.load_run(FIXTURE, upto=b["carve"])
        # The role moved; its untaggable children follow the parent's live
        # tag; the managed policy and the log groups stay where they were.
        self.assertEqual(short(after_carve, "aws_iam_role.team_a"), "team-b")
        self.assertEqual(short(after_carve, "aws_iam_role_policy.team_a_inline"), "team-b")
        self.assertEqual(short(after_carve, "aws_iam_role_policy_attachment.team_a"), "team-b")
        self.assertEqual(short(after_carve, "aws_iam_policy.team_a"), "team-a")
        self.assertEqual(short(after_carve, "aws_cloudwatch_log_group.team_a_0"), "team-a")


class Phases(unittest.TestCase):
    def test_carry_status_and_timing(self):
        state = viz.load_run(FIXTURE)
        by = {p.name: p for p in state.phases}
        self.assertEqual(by["setup"].status, "done")
        self.assertEqual(by["setup"].seconds, 31.0)
        self.assertEqual(by["teardown"].status, "pending")
        mid = viz.load_run(FIXTURE, upto=viz.phase_boundaries(FIXTURE)["setup"] + 1)
        self.assertIsNotNone(mid.active_phase)
        self.assertEqual(mid.active_phase.name, "slow-plan")


class TheLedger(unittest.TestCase):
    def test_names_writes_labels_and_refusals(self):
        state = viz.load_run(FIXTURE)
        actions = [r.action for r in state.ledger]
        self.assertIn("stand the monolith up", actions)            # a cmd label wins over argv
        self.assertIn("retag team-a's role into team-b", actions)
        self.assertTrue(any(r.action.startswith("verdict carve:") and r.ok for r in state.ledger))
        refusals = [r for r in state.ledger if r.answer == "Client.UnauthorizedOperation"]
        self.assertEqual(len(refusals), 2)
        self.assertTrue(all(r.ok is False and r.write for r in refusals))
        self.assertEqual({r.actor for r in refusals}, {"chdf-boundary-alice", "chdf-boundary-bob"})
        plans = [r for r in state.ledger if "plan" in r.action]      # labelled or not
        self.assertEqual(len(plans), 4)
        self.assertTrue(all(not r.write for r in plans))


class MeasuresAndVerdicts(unittest.TestCase):
    def test_are_read(self):
        state = viz.load_run(FIXTURE)
        self.assertEqual([(m.requests, m.refresh, m.cache_hits) for m in state.measures], [(58, True, 0), (5, False, 5)])
        self.assertEqual(state.measures[0].reference, {"emulator": {"monolith": 166}})
        v = state.verdicts[0]
        self.assertIs(v["ok"], True)
        self.assertEqual(v["moved_estates"], {"tlmig-sample-team-a-role": "tlmig-sample-team-b"})


class Rendering(unittest.TestCase):
    def test_html_carries_every_panel(self):
        state = viz.load_run(FIXTURE)
        page = viz.render_html(state)
        for needle in ("tlmig-sample", "<svg", "class='strip'", "class='ledger'", "Client.UnauthorizedOperation",
                       "requests per plan", "children kept", "58 requests", "5 cache hits"):
            self.assertIn(needle, page, needle)
        for name in viz.PHASES:
            self.assertIn(f">{name}", page)
        self.assertIn("class='ph done'", page)
        self.assertIn("class='ph pending'", page)

    def test_page_is_a_document_with_optional_refresh(self):
        doc = viz.render_page(FIXTURE, refresh_seconds=None)
        self.assertTrue(doc.startswith("<!doctype html>"))
        self.assertNotIn("http-equiv='refresh'", doc)
        self.assertIn("http-equiv='refresh' content='3'", viz.render_page(FIXTURE, refresh_seconds=3))

    def test_an_empty_run_directory_still_renders(self):
        with tempfile.TemporaryDirectory() as d:
            (pathlib.Path(d) / "manifest.json").write_text(json.dumps({"run_id": "x", "prefix": "tlmig-x", "region": "us-east-1", "estates": []}))
            page = viz.render_html(viz.load_run(d))
        self.assertIn("no estate configs yet", page)
        self.assertIn("no commands yet", page)

    def test_the_other_event_spelling_is_accepted(self):
        for spelling in (
            {"kind": "phase_start", "phase": "setup"},
            {"kind": "command", "phase": "setup", "argv": ["aws", "sts", "get-caller-identity"], "rc": 0, "seconds": 0.4},
            {"kind": "measurement", "phase": "slow-plan", "label": "m", "requests": 7, "cache_hits": 0, "seconds": 1.0},
            {"kind": "phase_end", "phase": "setup", "seconds": 2.0},
        ):
            with self.subTest(kind=spelling["kind"]), tempfile.TemporaryDirectory() as d:
                (pathlib.Path(d) / "events.jsonl").write_text(json.dumps({"ts": "2026-09-03T05:00:00+00:00", "run_id": "x", **spelling}) + "\n")
                state = viz.load_run(d)
                self.assertEqual(state.events_seen, 1)
                viz.render_html(state)


if __name__ == "__main__":
    unittest.main()
