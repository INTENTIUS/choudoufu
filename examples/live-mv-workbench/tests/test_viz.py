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
        self.assertEqual([p.name for p in state.phases][:2], ["setup", "slow-plan"])   # the sample run's own order first
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
        self.assertIn("the map fills in when setup runs", page)
        self.assertIn("nothing has run yet", page)

    def test_receipt_record_store_is_read_per_estate_and_reruns_overwrite(self):
        # 37's final contract: events.receipt is {carve, cloudtrail,
        # record_store: {estate: [address,...]}, source}; record_store is {}
        # when no store exists, and a rerun of receipt overwrites it.
        def run_with(receipts):
            evs = [{"kind": "phase", "name": "receipt", "status": "start"}]
            for r in receipts:
                evs.append({"kind": "receipt", "phase": "receipt", "receipt": r})
            evs.append({"kind": "phase", "name": "receipt", "status": "end"})
            with tempfile.TemporaryDirectory() as d:
                (pathlib.Path(d) / "manifest.json").write_text(json.dumps({"run_id": "x", "prefix": "tlmig-x", "region": "us-east-1", "estates": []}))
                (pathlib.Path(d) / "events.jsonl").write_text("\n".join(json.dumps(e) for e in evs) + "\n")
                return viz.load_run(d)

        store = {"tlmig-x-team-a": ["aws_iam_role.team_a", "aws_iam_role_policy.team_a_inline"], "tlmig-x-monolith": []}
        state = run_with([{"cloudtrail": {"events": []}, "record_store": store, "source": "s"}])
        self.assertEqual(state.record_store, store)
        html = viz.render_record_store(state)
        self.assertIn("aws_iam_role.team_a", html)
        self.assertIn(".tofu-records", html)

        # {} means no store: no panel, no crash.
        empty = run_with([{"cloudtrail": {"events": []}, "record_store": {}, "source": "s"}])
        self.assertEqual(empty.record_store, {})
        self.assertEqual(viz.render_record_store(empty), "")

        # a rerun overwrites with the fresh read.
        two = run_with([{"record_store": {"tlmig-x-team-a": ["old"]}}, {"record_store": {"tlmig-x-team-a": ["new"]}}])
        self.assertEqual(two.record_store, {"tlmig-x-team-a": ["new"]})

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


EMITTER = pathlib.Path(__file__).parent / "fixtures" / "emitter-run"


class TheEmitterRun(unittest.TestCase):
    """The run written by the real emitters (see its README): the map must
    move on the inventory lines alone, with no configs on disk."""

    def counts(self, state):
        return {viz._short_estate(state, e): sum(1 for r in state.resources.values() if state.estate_of(r.key) == e) for e in state.estates}

    def test_prefix_and_phases_come_from_the_events(self):
        state = viz.load_run(EMITTER)
        self.assertEqual(state.prefix, "tlmig-a1b2c3")
        self.assertEqual([p.name for p in state.phases], ["preflight", "setup", "carve", "guard", "measure", "receipt", "teardown"])
        self.assertTrue(all(p.status == "done" for p in state.phases))

    def test_the_map_moves_on_inventory_alone(self):
        b = viz.phase_boundaries(EMITTER)
        self.assertEqual(self.counts(viz.load_run(EMITTER, upto=b["setup"])), {"monolith": 12, "team-a": 0, "team-b": 0})
        self.assertEqual(self.counts(viz.load_run(EMITTER, upto=b["carve"])), {"monolith": 8, "team-a": 4, "team-b": 0})
        final = viz.load_run(EMITTER)
        self.assertEqual(self.counts(final), {"monolith": 0, "team-a": 0, "team-b": 0})
        self.assertEqual(sum(1 for r in final.resources.values() if r.gone), 12)
        self.assertEqual(sorted({r.team for r in final.resources.values()}), ["team-a", "team-b", "team-c"])

    def test_ledger_measures_and_verdicts(self):
        state = viz.load_run(EMITTER)
        labels = [r.action for r in state.ledger]
        self.assertIn("retag the role into team-a", labels)
        self.assertEqual(sum(1 for r in state.ledger if r.answer == "Client.UnauthorizedOperation"), 2)
        self.assertEqual([(m.requests, m.reference) for m in state.measures], [(41, {"emulator carved estate": 39}), (158, {"emulator monolith": 166})])
        self.assertEqual([v["name"] for v in state.verdicts], ["carve:tlmig-a1b2c3-team-a-role", "teardown"])
        page = viz.render_html(state)
        self.assertIn("nothing left", page)


class CompactPicture(unittest.TestCase):
    def test_is_map_measures_and_verdict_only_or_nothing(self):
        b = viz.phase_boundaries(FIXTURE)
        full = viz.render_html(viz.load_run(FIXTURE, upto=b["carve"]), compact=True)
        self.assertIn("<svg", full)
        self.assertNotIn("class='strip'", full)
        self.assertNotIn("class='ledger'", full)
        # With configs on disk the map has cells before any event; with none
        # (the emitter run) and no inventory yet, the compact picture is empty.
        self.assertIn("<svg", viz.render_html(viz.load_run(FIXTURE, upto=0), compact=True))
        self.assertEqual(viz.render_html(viz.load_run(EMITTER, upto=0), compact=True), "")
        self.assertIn("requests per plan", viz.render_html(viz.load_run(FIXTURE), compact=True))


class PhaseLedger(unittest.TestCase):
    def test_one_phase_rows_only(self):
        state = viz.load_run(FIXTURE)
        html = viz.render_phase_ledger(state, "carve")
        self.assertIn("role into team-b", html)        # the apostrophe in the label is HTML-escaped
        self.assertNotIn("stand the monolith up", html)
        self.assertEqual(viz.render_phase_ledger(state, "nope"), "")


class DeltaPicture(unittest.TestCase):
    def test_shows_only_what_the_phase_changed(self):
        b = viz.phase_boundaries(FIXTURE)
        setup = viz.load_run(FIXTURE, upto=b["setup"])
        slow = viz.load_run(FIXTURE, upto=b["slow-plan"])
        # setup fills the map; the slow plan changes no ownership but adds a measure
        self.assertIn("<svg", viz.render_delta(setup, viz.load_run(FIXTURE, upto=0)))
        d = viz.render_delta(slow, setup)
        self.assertNotIn("who owns what", d)
        self.assertIn("requests per plan", d)
        self.assertIn("58 requests", d)
        # the carve moves ownership: the map is back
        self.assertIn("<svg", viz.render_delta(viz.load_run(FIXTURE, upto=b["carve"]), viz.load_run(FIXTURE, upto=b["fast-plan"])))
        # a phase that changed nothing visible renders empty
        self.assertEqual(viz.render_delta(slow, slow), "")


class Payoff(unittest.TestCase):
    def test_each_beat_has_a_sentence_from_its_own_numbers(self):
        b = viz.phase_boundaries(FIXTURE)
        at = lambda n: viz.load_run(FIXTURE, upto=b[n])
        self.assertIn("15 taggable resources", viz.payoff("setup", at("setup"), viz.load_run(FIXTURE, upto=0)))
        self.assertIn("58 requests", viz.payoff("slow-plan", at("slow-plan"), at("setup")))
        self.assertIn("3 team estates", viz.payoff("decompose", at("decompose"), at("slow-plan")))
        self.assertIn("11.6x fewer", viz.payoff("fast-plan", at("fast-plan"), at("decompose")))
        self.assertIn("changed owner into team-b", viz.payoff("carve", at("carve"), at("fast-plan")))
        self.assertIn("Verdict holds", viz.payoff("guard", at("guard"), at("carve")))
        self.assertIn("2 refused", viz.payoff("receipt", viz.load_run(FIXTURE), at("guard")))
        self.assertEqual(viz.payoff("teardown", viz.load_run(FIXTURE), None), "")   # not run in the sample

    def test_teardown_and_emitter_run(self):
        state = viz.load_run(EMITTER)
        self.assertIn("Nothing carrying this run's prefix remains", viz.payoff("teardown", state, None))
        self.assertIn("nothing touched", viz.payoff("preflight", state, None))


class TwoPlans(unittest.TestCase):
    def test_the_carve_verdict_names_both_estates_and_what_each_plan_proves(self):
        state = viz.load_run(EMITTER)
        v = next(v for v in state.verdicts if v["name"].startswith("carve"))
        self.assertEqual(v["destination_estate"], "tlmig-a1b2c3-team-a")
        html = viz.render_verdicts(state)
        self.assertIn("plan of the source estate", html)
        self.assertIn("plan of the destination estate", html)
        self.assertIn("What left must not be destroyed or rebuilt", html)
        self.assertIn("What arrived must already be its own", html)
        self.assertEqual(html.count("class='plan'"), 2)

    def test_sample_run_names_source_and_destination_from_the_plans(self):
        state = viz.load_run(FIXTURE)
        v = next(v for v in state.verdicts if v["name"].startswith("carve"))
        self.assertEqual((viz._short_estate(state, v["source_estate"]), viz._short_estate(state, v["destination_estate"])), ("team-a", "team-b"))


class VerdictEstatesFromLines(unittest.TestCase):
    def test_a_recording_without_plan_commands_still_names_both_estates(self):
        state = viz.load_run(EMITTER)
        v = next(v for v in state.verdicts if v["name"].startswith("carve"))
        self.assertEqual(viz._short_estate(state, v["source_estate"]), "monolith")
        self.assertEqual(viz._short_estate(state, v["destination_estate"]), "team-a")


class Tips(unittest.TestCase):
    def test_every_phase_has_both_registers(self):
        from tlmig import tips
        for phase in viz.PHASES:
            self.assertTrue(tips.tip(phase, "beginner"), phase)
            self.assertTrue(tips.tip(phase, "expert"), phase)
        self.assertEqual(tips.tip("nope", "beginner"), "")


PREVIEW = pathlib.Path(__file__).parent / "fixtures" / "preview-run"


class Projection(unittest.TestCase):
    def test_previews_are_read_and_a_later_one_replaces_an_earlier(self):
        state = viz.load_run(PREVIEW)
        self.assertEqual([p["address"] for p in state.previews], ["aws_iam_role.team_a", "aws_iam_policy.team_a", "aws_cloudwatch_log_group.team_a_0"])
        self.assertTrue(state.previews[2]["refusal"])

    def test_the_projection_moves_passed_moves_and_their_children_only(self):
        state = viz.load_run(PREVIEW)
        after = viz.project(state)
        self.assertEqual(short(state, "aws_iam_role.team_a"), "team-a")
        self.assertEqual(short(after, "aws_iam_role.team_a"), "team-b")
        self.assertEqual(short(after, "aws_iam_role_policy.team_a_inline"), "team-b")     # follows the parent
        self.assertEqual(short(after, "aws_iam_policy.team_a"), "team-b")
        self.assertEqual(short(after, "aws_cloudwatch_log_group.team_a_0"), "team-a")     # refused: unchanged
        self.assertEqual(short(after, "aws_cloudwatch_log_group.team_a_1"), "team-a")
        self.assertEqual(short(state, "aws_iam_role.team_a"), "team-a")                    # the original is untouched

    def test_rendering(self):
        state = viz.load_run(PREVIEW)
        table = viz.render_previews(state)
        self.assertIn("3 planned moves, 1 refused", table)
        self.assertIn("the destination does not declare", table)
        self.assertIn("would write", table)
        both = viz.render_projection(state)
        self.assertEqual(both.count("<svg"), 2)
        self.assertEqual(viz.render_previews(viz.load_run(FIXTURE)), "")
