"""read_carve_cost emits one measure event per destination estate, with the
estate and refresh fields the page's cost bars read. The cloud is faked at
the guard boundary: the fake writes the TF_LOG file measure_plan counts, so
a known request count flows through to the event."""

from __future__ import annotations

import json
import pathlib
import tempfile
import unittest
from unittest import mock

from tlmig import config, events, govern, guard


def _result(stdout: str = "", rc: int = 0) -> guard.Result:
    return guard.Result(argv=["fake"], returncode=rc, stdout=stdout, stderr="", seconds=0.2)


class CarveCost(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.cfg = config.Config(run_id="cc01", run_dir=pathlib.Path(self.tmp.name) / "run", binary="choudoufu")
        self.carve = pathlib.Path(self.tmp.name) / "carve.json"
        self.carve.write_text(json.dumps({"moves": [
            {"address": "aws_iam_role.team_a", "from_estate": "mono", "to_estate": "team-a"},
            {"address": "aws_iam_role.team_b", "from_estate": "mono", "to_estate": "team-b"},
        ]}))

    def _chdf_writing(self, requests_by_estate: dict[str, int]):
        """A fake plan that writes measure's TF_LOG file with N request lines,
        picking N by which estate's workdir it runs in."""
        def fake(cfg, *a, cwd="", env=None, **kw):
            path = env and env.get("TF_LOG_PATH")
            if path:
                n = next((c for e, c in requests_by_estate.items() if e in cwd), 0)
                pathlib.Path(path).parent.mkdir(parents=True, exist_ok=True)
                pathlib.Path(path).write_text("HTTP Request Sent\n" * n)
            return _result("No changes.\n")
        return fake

    def test_one_measure_event_per_destination_with_estate_and_refresh(self):
        with mock.patch.object(guard, "chdf", self._chdf_writing({"team-a": 41, "team-b": 39})):
            ms = govern.read_carve_cost(self.cfg, self.carve)
        self.assertEqual([m.estate for m in ms], ["team-a", "team-b"])
        self.assertEqual([m.requests for m in ms], [41, 39])
        measures = [e for e in events.read(self.cfg) if e["kind"] == "measure"]
        self.assertEqual(len(measures), 2)
        by_estate = {e["estate"]: e for e in measures}
        self.assertEqual(by_estate["team-a"]["requests"], 41)
        self.assertFalse(by_estate["team-a"]["refresh"])
        self.assertFalse(by_estate["team-b"]["refresh"])

    def test_refresh_true_is_the_full_plan(self):
        with mock.patch.object(guard, "chdf", self._chdf_writing({"team-a": 158, "team-b": 160})):
            ms = govern.read_carve_cost(self.cfg, self.carve, refresh=True)
        self.assertTrue(all(m.refresh for m in ms))
        self.assertEqual([m.requests for m in ms], [158, 160])


if __name__ == "__main__":
    unittest.main()
