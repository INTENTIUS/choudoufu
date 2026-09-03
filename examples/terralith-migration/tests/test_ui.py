"""The evidence helpers render what a read saw, cut long output, and say the
estate's shape before its addresses. Rendered into a plain console so the
assertions are on text, never on colour."""

from __future__ import annotations

import io
import unittest
from unittest import mock

from rich.console import Console

from tlmig import ui


def _plain() -> Console:
    return Console(file=io.StringIO(), width=120, force_terminal=False, color_system=None)


class EvidenceTest(unittest.TestCase):
    def test_lines_are_shown_verbatim_and_indented(self) -> None:
        con = _plain()
        with mock.patch.object(ui, "console", con):
            ui.evidence(["tofu-estate=tlmig-x-team-a", "PolicyNames: tlmig-x-team-a-inline"])
        out = con.file.getvalue()
        self.assertIn("      tofu-estate=tlmig-x-team-a\n", out)
        self.assertIn("      PolicyNames: tlmig-x-team-a-inline\n", out)

    def test_long_output_is_cut_with_a_count(self) -> None:
        con = _plain()
        with mock.patch.object(ui, "console", con):
            ui.evidence([f"line {i}" for i in range(20)], limit=5)
        out = con.file.getvalue()
        self.assertIn("line 4", out)
        self.assertNotIn("line 5", out)
        self.assertIn("15 more line(s) not shown", out)

    def test_proof_reads_like_the_smoke(self) -> None:
        con = _plain()
        with mock.patch.object(ui, "console", con):
            ui.proof("No changes, twice.")
        self.assertIn("-> No changes, twice.", con.file.getvalue())


class InventoryTest(unittest.TestCase):
    ITEMS = [
        {"id": "arn:aws:iam::1:role/p-team-a-role", "type": "iam:role", "address": "aws_iam_role.team_a", "tags": {}},
        {"id": "arn:aws:logs:us-east-1:1:log-group:/p/team-a/svc-0", "type": "logs:log-group", "address": "aws_cloudwatch_log_group.team_a_0", "tags": {}},
        {"id": "arn:aws:logs:us-east-1:1:log-group:/p/team-a/svc-1", "type": "logs:log-group", "address": "aws_cloudwatch_log_group.team_a_1", "tags": {}},
    ]

    def test_count_then_shape_then_addresses(self) -> None:
        con = _plain()
        with mock.patch.object(ui, "console", con):
            ui.inventory("p-team-a", self.ITEMS)
        out = con.file.getvalue()
        self.assertIn("p-team-a inventory 3 resource(s)", out)
        self.assertIn("1 iam:role, 2 logs:log-group", out)
        self.assertLess(out.index("3 resource(s)"), out.index("1 iam:role"))
        self.assertLess(out.index("1 iam:role"), out.index("aws_iam_role.team_a"))
        self.assertIn("aws_cloudwatch_log_group.team_a_1  arn:aws:logs:us-east-1:1:log-group:/p/team-a/svc-1", out)

    def test_an_empty_estate_is_one_line(self) -> None:
        con = _plain()
        with mock.patch.object(ui, "console", con):
            ui.inventory("p-team-b", [])
        out = con.file.getvalue().strip().splitlines()
        self.assertEqual(len(out), 1)
        self.assertIn("0 resource(s)", out[0])

    def test_a_missing_address_is_named_not_blank(self) -> None:
        con = _plain()
        with mock.patch.object(ui, "console", con):
            ui.inventory("p-x", [{"id": "arn:x", "type": "iam:role", "address": "", "tags": {}}])
        self.assertIn("(no address)  arn:x", con.file.getvalue())


if __name__ == "__main__":
    unittest.main()
