"""This run's own CloudTrail record: the lookup filters by the run's prefix,
parses tag writes, and gives up gracefully when history lags."""
from __future__ import annotations

import datetime
import json
import pathlib
import tempfile
import unittest
from unittest import mock

from tlmig import config, receipt


class FakeRes:
    def __init__(self, ok, stdout):
        self.ok, self.stdout = ok, stdout


def trail(name, prefix, tags, arn="arn:aws:sts::354867293429:assumed-role/x/y", err=None, t="2026-09-03T06:44:10Z"):
    ev = {"eventTime": t, "eventName": name, "userIdentity": {"arn": arn},
          "requestParameters": {"roleName": f"{prefix}-team-a-role", "tags": [{"key": k, "value": v} for k, v in tags.items()]}}
    if err:
        ev["errorCode"] = err
    return {"EventId": f"{name}-{t}", "EventTime": t, "CloudTrailEvent": json.dumps(ev)}


class Lookup(unittest.TestCase):
    def cfg(self, d):
        return config.Config(run_id="abc123", run_dir=pathlib.Path(d), binary="/x/choudoufu")

    def test_filters_to_the_run_prefix_and_parses_tag_writes(self):
        with tempfile.TemporaryDirectory() as d:
            cfg = self.cfg(d)
            def fake_aws(cfg_, *args, **kw):
                name = next(a.split("=")[-1] for a in args if a.startswith("AttributeKey=EventName"))
                if name == "TagRole":
                    return FakeRes(True, json.dumps({"Events": [trail("TagRole", cfg.prefix, {"tofu-estate": f"{cfg.prefix}-team-b"}),
                                                                trail("TagRole", "tlmig-other", {"tofu-estate": "elsewhere"})]}))
                return FakeRes(True, json.dumps({"Events": []}))
            with mock.patch.object(receipt.guard, "aws", side_effect=fake_aws), mock.patch.object(receipt.ui, "kv"):
                ct = receipt.lookup_run_cloudtrail(cfg, since=datetime.datetime(2026, 9, 3, 6, 40, tzinfo=datetime.timezone.utc), max_wait=0)
            self.assertEqual(len(ct.events), 1)
            e = ct.events[0]
            self.assertEqual((e.role, e.resource, e.tags, e.error), ("x/y", "tlmig-abc123-team-a-role", {"tofu-estate": "tlmig-abc123-team-b"}, None))
            self.assertEqual(ct.account, config.ACCOUNT_ID)

    def test_gives_up_empty_when_history_lags(self):
        with tempfile.TemporaryDirectory() as d:
            cfg = self.cfg(d)
            with mock.patch.object(receipt.guard, "aws", return_value=FakeRes(True, json.dumps({"Events": []}))), \
                 mock.patch.object(receipt.ui, "kv"), mock.patch.object(receipt.time, "sleep") as slept:
                ct = receipt.lookup_run_cloudtrail(cfg, since=datetime.datetime.now(datetime.timezone.utc), max_wait=1, poll=1)
            self.assertEqual(ct.events, ())
            self.assertTrue(slept.called)

    def test_principal_names(self):
        self.assertEqual(receipt._principal("arn:aws:sts::1:assumed-role/alice/sess"), "alice/sess")
        self.assertEqual(receipt._principal("arn:aws:iam::1:user/alex"), "user alex")
        self.assertEqual(receipt._principal("arn:aws:iam::1:root"), "root")
