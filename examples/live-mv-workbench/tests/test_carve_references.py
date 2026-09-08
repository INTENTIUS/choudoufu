"""The planner's cost input: references[] out of `live-check -json`.

internal/live/check/references.go says what this is for in as many words -
"the planner input the issue's own Why section asks for: a move of Address
costs one data-source filter rewrite per entry here (tlmig/carve.py, the
workbench's planner, has no input for this today)". It has one now.

The documents below are views.LiveCheckReference's own JSON tags (from,
estate, address, read_by), written from that struct rather than from the
parser, and their shape is live/e2e/estate-references' fixture: one data
source filtered on a producer's two marker tags, read by one resource.
"""

from __future__ import annotations

import json
import pathlib
import tempfile
import unittest
from unittest import mock

from tlmig import carve, config, events, govern, guard, viz


def check_doc(estate: str, references: list[dict]) -> dict:
    """One live-check -json document, cut down to the keys the planner reads."""
    return {"dir": ".", "estate": estate, "blocked": False, "exit_code": 0,
            "instances": [], "references": references, "checked": ["roster"]}


NETWORK_REF = {"from": "data.aws_vpc.team_a_network", "estate": "tl-team-a",
               "address": "aws_vpc.main", "read_by": ["aws_subnet.app"]}


class ReadReferences(unittest.TestCase):
    def test_the_document_is_read_field_for_field(self):
        refs = carve.references_from_check(check_doc("tl-team-b", [NETWORK_REF]))
        self.assertEqual(len(refs), 1)
        r = refs[0]
        self.assertEqual(r.source, "data.aws_vpc.team_a_network")
        self.assertEqual(r.estate, "tl-team-a")
        self.assertEqual(r.address, "aws_vpc.main")
        self.assertEqual(r.read_by, ("aws_subnet.app",))
        # in_estate defaults to the document's own estate: the configuration
        # that declares the data source, which is not the one it reads
        self.assertEqual(r.in_estate, "tl-team-b")

    def test_a_document_with_no_references_prices_nothing_rather_than_failing(self):
        self.assertEqual(carve.references_from_check(check_doc("tl-team-b", [])), [])
        self.assertEqual(carve.references_from_check({"estate": "x"}), [])

    def test_a_reference_naming_an_estate_and_no_address_matches_no_move(self):
        """tag:tofu-estate alone is a legal filter and live-check reports it
        with an empty address. It says which estate is read, not which
        instance, so no single move can be blamed for it."""
        vague = {"from": "data.aws_vpc.any", "estate": "tl-team-a", "read_by": ["aws_subnet.app"]}
        refs = carve.references_from_check(check_doc("tl-team-b", [vague]))
        self.assertEqual(carve.rewrites_for("aws_vpc.main", "tl-team-a", refs), [])


class Pricing(unittest.TestCase):
    REFS = [carve.Reference(source="data.aws_vpc.team_a_network", estate="tl-team-a",
                            address="aws_vpc.main", read_by=("aws_subnet.app", "aws_route_table.app"),
                            in_estate="tl-team-b"),
            carve.Reference(source="data.aws_vpc.other", estate="tl-team-c",
                            address="aws_vpc.other", read_by=("aws_subnet.c",), in_estate="tl-team-b")]

    def test_a_move_costs_one_rewrite_per_reader_of_the_data_source_that_names_it(self):
        doc = carve.plan("tl-team-a",
                         [("aws_vpc.main", "aws_vpc", "tl-team-a"),
                          ("aws_iam_role.r", "aws_iam_role", "tl-team-a")],
                         [carve.Rule("prefix", "aws_", "tl-team-b")],
                         references=self.REFS)
        by_addr = {m["address"]: m for m in doc["moves"]}
        self.assertEqual(by_addr["aws_vpc.main"]["rewrites"], 2)
        self.assertEqual(by_addr["aws_vpc.main"]["data_sources"], ["data.aws_vpc.team_a_network"])
        self.assertEqual(by_addr["aws_vpc.main"]["read_by"], ["aws_route_table.app", "aws_subnet.app"])
        # nothing reads the role across a boundary
        self.assertEqual(by_addr["aws_iam_role.r"]["rewrites"], 0)
        self.assertEqual(by_addr["aws_iam_role.r"]["data_sources"], [])
        self.assertEqual(carve.total_rewrites(doc), 2)

    def test_a_reference_to_another_estates_instance_is_not_this_moves_cost(self):
        """The second reference names tl-team-c's aws_vpc.other. Moving
        tl-team-a's aws_vpc.main must not be charged for it."""
        doc = carve.plan("tl-team-a", [("aws_vpc.main", "aws_vpc", "tl-team-a")],
                         [carve.Rule("type", "aws_vpc", "tl-team-b")], references=self.REFS)
        self.assertEqual(doc["moves"][0]["rewrites"], 2)
        self.assertEqual(carve.total_rewrites(doc), 2)

    def test_describe_says_the_rewrites_beside_the_moves(self):
        doc = carve.plan("tl-team-a",
                         [("aws_vpc.main", "aws_vpc", "tl-team-a"),
                          ("aws_iam_role.r", "aws_iam_role", "tl-team-a")],
                         [carve.Rule("prefix", "aws_", "tl-team-b")], references=self.REFS)
        self.assertEqual(carve.describe(doc),
                         ["tl-team-b: 2 moves, 2 filter rewrites (aws_vpc.main, aws_iam_role.r)"])

    def test_an_unpriced_plan_does_not_claim_a_cost_of_zero(self):
        """A planner with no references never ran live-check. Saying "0 filter
        rewrites" would be a measurement it did not make."""
        doc = carve.plan("tl-team-a", [("aws_vpc.main", "aws_vpc", "tl-team-a")],
                         [carve.Rule("type", "aws_vpc", "tl-team-b")])
        self.assertNotIn("rewrites", doc["moves"][0])
        self.assertEqual(carve.describe(doc), ["tl-team-b: 1 moves (aws_vpc.main)"])
        self.assertEqual(carve.total_rewrites(doc), 0)

    def test_one_rewrite_reads_as_singular(self):
        refs = [carve.Reference(source="data.aws_vpc.n", estate="a", address="aws_vpc.main",
                                read_by=("aws_subnet.app",))]
        doc = carve.plan("a", [("aws_vpc.main", "aws_vpc", "a")], [carve.Rule("type", "aws_vpc", "b")],
                         references=refs)
        self.assertIn("1 filter rewrite (", carve.describe(doc)[0])


class ReadThroughTheFence(unittest.TestCase):
    """govern.read_references runs one live-check per estate through guard, so
    the calls land in the transcript and the event feed like every other read."""

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.cfg = config.Config(run_id="cr01", run_dir=pathlib.Path(self.tmp.name) / "run", binary="choudoufu")
        for estate in ("tl-team-a", "tl-team-b"):
            self.cfg.workdir(estate).mkdir(parents=True)

    def _result(self, stdout: str, rc: int = 0) -> guard.Result:
        return guard.Result(argv=["fake"], returncode=rc, stdout=stdout, stderr="", seconds=0.0)

    def test_one_live_check_json_per_estate_and_the_references_out_of_it(self):
        seen = []
        def fake(cfg, *a, cwd="", **kw):
            seen.append((list(a), cwd))
            estate = "tl-team-b" if "tl-team-b" in cwd else "tl-team-a"
            refs = [NETWORK_REF] if estate == "tl-team-b" else []
            return self._result(json.dumps(check_doc(estate, refs)))
        with mock.patch.object(guard, "chdf", fake):
            refs = govern.read_references(self.cfg, ["tl-team-a", "tl-team-b"])
        self.assertEqual([a[0][0] for a in seen], ["live-check", "live-check"])
        self.assertTrue(all("-json" in a[0] for a in seen))
        self.assertEqual([r.source for r in refs], ["data.aws_vpc.team_a_network"])
        self.assertEqual(refs[0].in_estate, "tl-team-b")
        feed = [e for e in events.read(self.cfg) if e["kind"] == "reference"]
        self.assertEqual(len(feed), 1)
        self.assertEqual(feed[0]["source"], "data.aws_vpc.team_a_network")
        self.assertEqual(feed[0]["estate"], "tl-team-a")
        self.assertEqual(feed[0]["address"], "aws_vpc.main")
        self.assertEqual(feed[0]["read_by"], ["aws_subnet.app"])
        self.assertEqual(feed[0]["in_estate"], "tl-team-b")

    def test_an_estate_whose_check_prints_no_document_is_skipped_not_guessed(self):
        with mock.patch.object(guard, "chdf", lambda cfg, *a, **kw: self._result("boom", rc=1)):
            refs = govern.read_references(self.cfg, ["tl-team-a"])
        self.assertEqual(refs, [])

    def test_findings_do_not_stop_the_references_being_read(self):
        """live-check exits nonzero when it has findings. Its document still
        carries references[], and a plan that refused to price a move because
        some unrelated instance was refused would be the worse answer."""
        doc = check_doc("tl-team-b", [NETWORK_REF]) | {"blocked": True, "exit_code": 1}
        with mock.patch.object(guard, "chdf", lambda cfg, *a, **kw: self._result(json.dumps(doc), rc=1)):
            refs = govern.read_references(self.cfg, ["tl-team-b"])
        self.assertEqual([r.address for r in refs], ["aws_vpc.main"])


class TheRecordedRun(unittest.TestCase):
    """The preview-run fixture carries the whole chain: HCL declaring the
    cross-estate read, the reference event live-check produced from it, and a
    plan priced off the feed the way the page prices one in replay.

    The event was not invented: `choudoufu v0.15.0 live-check -json` run on
    tests/fixtures/preview-run/estates/tlmig-sample-team-b prints exactly
    {"from": "data.aws_vpc.team_a_network", "estate": "tlmig-sample-team-a",
     "address": "aws_vpc.main", "read_by": ["aws_subnet.app"]}.
    """

    FIXTURE = pathlib.Path(__file__).parent / "fixtures" / "preview-run"

    def test_the_hcl_declares_the_read_the_event_reports(self):
        team_b = (self.FIXTURE / "estates" / "tlmig-sample-team-b" / "main.tf").read_text()
        self.assertIn('data "aws_vpc" "team_a_network"', team_b)
        self.assertIn('values = ["tlmig-sample-team-a"]', team_b)
        self.assertIn('values = ["aws_vpc.main"]', team_b)
        self.assertIn("vpc_id     = data.aws_vpc.team_a_network.id", team_b)
        team_a = (self.FIXTURE / "estates" / "tlmig-sample-team-a" / "main.tf").read_text()
        self.assertIn('resource "aws_vpc" "main"', team_a)

    def test_the_page_prices_the_move_off_the_feed(self):
        state = viz.load_run(self.FIXTURE)
        refs = [carve.Reference(source=x["source"], estate=x["estate"], address=x["address"],
                                read_by=tuple(x["read_by"]), in_estate=x["in_estate"])
                for x in state.references]
        self.assertEqual(len(refs), 1)
        doc = carve.plan("tlmig-sample-team-a",
                         [("aws_vpc.main", "aws_vpc", "tlmig-sample-team-a")],
                         [carve.Rule("type", "aws_vpc", "tlmig-sample-team-c")],
                         references=refs)
        self.assertEqual(doc["moves"][0]["rewrites"], 1)
        self.assertEqual(doc["moves"][0]["data_sources"], ["data.aws_vpc.team_a_network"])
        self.assertIn("1 filter rewrite", carve.describe(doc)[0])


if __name__ == "__main__":
    unittest.main()
