"""The carve plan: rules fill rows, overrides win, only real moves are moves."""
from __future__ import annotations

import pathlib
import tempfile
import unittest

from tlmig import carve

RES = [("aws_iam_role.team_a", "aws_iam_role", "mono"), ("aws_iam_role_policy.team_a_inline", "aws_iam_role_policy", "mono"),
       ("aws_iam_policy.team_a", "aws_iam_policy", "mono"), ("aws_cloudwatch_log_group.team_a_0", "aws_cloudwatch_log_group", "mono"),
       ("module.data.aws_instance.db", "aws_instance", "mono"), ("aws_iam_role.team_b", "aws_iam_role", "team-b")]


class Rules(unittest.TestCase):
    def test_parse_and_problems(self):
        rules, problems = carve.parse_rules("module data -> data\n# comment\nprefix aws_iam_ -> iam\ntype aws_cloudwatch_log_group -> logs\nnonsense line\nname team_a => team-a\n")
        self.assertEqual([(r.match, r.value, r.to) for r in rules], [("module", "data", "data"), ("prefix", "aws_iam_", "iam"), ("type", "aws_cloudwatch_log_group", "logs"), ("name", "team_a", "team-a")])
        self.assertEqual(len(problems), 1)
        self.assertIn("line 5", problems[0])

    def test_later_rules_win_and_overrides_win_over_rules(self):
        rules, _ = carve.parse_rules("prefix aws_iam_ -> iam\nname team_a -> team-a")
        self.assertEqual(carve.destination("aws_iam_role.team_a", "aws_iam_role", rules), "team-a")
        self.assertEqual(carve.destination("aws_iam_role.team_b", "aws_iam_role", rules), "iam")
        self.assertEqual(carve.destination("aws_iam_role.team_a", "aws_iam_role", rules, override="elsewhere"), "elsewhere")
        self.assertEqual(carve.destination("aws_instance.x", "aws_instance", rules), carve.KEEP)


class Plan(unittest.TestCase):
    def test_only_real_moves_and_children_never(self):
        rules, _ = carve.parse_rules("module data -> data\nname team_a -> team-a\ntype aws_iam_role_policy -> team-a")
        doc = carve.plan("mono", RES, rules, overrides={"aws_cloudwatch_log_group.team_a_0": "keep"})
        moves = {m["address"]: m["to"] for m in doc["moves"]}
        self.assertTrue(all(m["from"] == "mono" for m in doc["moves"]))
        self.assertEqual(moves, {"aws_iam_role.team_a": "team-a", "aws_iam_role_policy.team_a_inline": "team-a", "aws_iam_policy.team_a": "team-a", "module.data.aws_instance.db": "data"})
        self.assertNotIn("aws_iam_role.team_b", moves)          # already there: not a move
        self.assertEqual(doc["estates"], ["team-a", "data"])
        self.assertEqual(doc["from"], "mono")
        self.assertEqual(len(doc["rules"]), 3)

    def test_override_by_estate_and_address_wins_for_that_estate_only(self):
        res = [("aws_iam_role.team", "aws_iam_role", "mono"), ("aws_iam_role.team", "aws_iam_role", "team-b")]
        doc = carve.plan("mono", res, [], overrides={"mono:aws_iam_role.team": "team-a"})
        self.assertEqual(doc["moves"], [{"address": "aws_iam_role.team", "from": "mono", "to": "team-a"}])

    def test_save_load_describe(self):
        with tempfile.TemporaryDirectory() as d:
            doc = carve.plan("mono", RES, carve.parse_rules("name team_a -> team-a")[0])
            p = carve.save(d, doc)
            self.assertEqual(p, pathlib.Path(d) / "carve.json")
            self.assertEqual(carve.load(d), doc)
            self.assertIn("team-a: 4 moves", carve.describe(doc)[0])
            self.assertEqual(carve.describe({"estates": [], "moves": []}), ["no moves: every row keeps its estate"])
