"""The writer and the reader agree by construction: a carve.json that
tlmig.carve.plan produces is read back by moveset.load_carve into the same
moves. This is the one test that fails if either side changes the grammar
without the other, which is exactly the drift the three-way contract risks."""

from __future__ import annotations

import unittest

from tlmig import carve, moveset


class Roundtrip(unittest.TestCase):
    def test_planner_output_reads_as_a_move_set(self):
        rules, problems = carve.parse_rules(
            "prefix aws_iam_role.team_a -> team-a\nname team_b -> team-b\n"
        )
        self.assertEqual(problems, [])
        # (address, type, current estate), the shape the page hands plan()
        resources = [
            ("aws_iam_role.team_a", "aws_iam_role", "mono"),
            ("aws_iam_role.team_b", "aws_iam_role", "mono"),
            ("aws_iam_role.stay", "aws_iam_role", "mono"),  # matches nothing, keeps
        ]
        doc = carve.plan("mono", resources, rules)
        cs = moveset.load_carve(__import__("json").dumps(doc))
        moved = {m.address: (m.from_estate, m.to_estate) for m in cs.moves}
        self.assertEqual(moved, {
            "aws_iam_role.team_a": ("mono", "team-a"),
            "aws_iam_role.team_b": ("mono", "team-b"),
        })
        self.assertNotIn("aws_iam_role.stay", moved)          # a kept row is not a move
        self.assertEqual(set(cs.dest_estates), {"team-a", "team-b"})
        self.assertTrue(all(m.target == m.address for m in cs.moves))  # no rename here

    def test_an_empty_plan_is_refused_as_no_moves(self):
        doc = carve.plan("mono", [("aws_iam_role.stay", "aws_iam_role", "mono")], [])
        self.assertEqual(doc["moves"], [])
        with self.assertRaises(ValueError):
            moveset.load_carve(__import__("json").dumps(doc))


if __name__ == "__main__":
    unittest.main()
