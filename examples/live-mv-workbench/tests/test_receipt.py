import json
import pathlib
import tempfile
import unittest

from tlmig import receipt

# Evidence lines exactly as carve-by-retag printed them on 2026-09-03
# (v0.10.0, the run whose numbers the artifact quotes).
CARVE_LOG = """choudoufu smoke v1.0.0 - scenario: carve-by-retag

=== 2. one command adopts it, and the state file is deleted ===
      38 of 79 resource instance(s) are eligible for stamping (VERIFIED or DRIFTED).
      the monolith plans clean with the state file gone: 166 requests

=== 3. carve a team out: six blocks move, three tags are rewritten ===
        tofu-estate    "tl-terralith" -> "tl-team-1"
        live ID        tl-team-0001-profile
      aws iam list-role-tags tl-team-0001-role: tofu-estate=tl-team-1; inline policies still attached: tl-team-0001-inline

=== 5. carve across a reference ===
      aws iam list-role-tags tl-svc-0000-exec-role: tofu-estate=tl-iam

=== 6. a plan costs what its estate holds ===
      monolith plan: 166 requests   ·   team estate plan: 39 requests

=== 7. teardown - three estates, each by its own destroy ===
      monolith: 71 destroyed
      iam: 2 destroyed
      team1: 6 destroyed

PASS: smoke scenario 'carve-by-retag' - every claim held (smoke v1.0.0)
"""


class CarveParsing(unittest.TestCase):
    def test_the_artifact_numbers(self):
        r = receipt.parse_carve(CARVE_LOG)
        self.assertTrue(r.passed)
        self.assertEqual((r.stamped, r.declared), (38, 79))
        self.assertEqual((r.monolith_plan_requests, r.carved_plan_requests), (166, 39))
        self.assertEqual(r.followed, (71, 2, 6))
        self.assertEqual(r.total_destroyed, 79)
        self.assertEqual(r.retags, (("tl-terralith", "tl-team-1"),))
        self.assertEqual(r.role_estates, {"tl-team-0001-role": "tl-team-1", "tl-svc-0000-exec-role": "tl-iam"})

    def test_a_moved_line_fails_here_first(self):
        broken = CARVE_LOG.replace("team estate plan: 39 requests", "team plan: 39 requests")
        with self.assertRaises(ValueError) as cm:
            receipt.parse_carve(broken)
        self.assertIn("plan cost", str(cm.exception))

    def test_no_pass_line_is_recorded_not_hidden(self):
        r = receipt.parse_carve(CARVE_LOG.replace("PASS: smoke scenario", "FAIL: smoke scenario"))
        self.assertFalse(r.passed)


class CloudTrail(unittest.TestCase):
    def test_repository_evidence_parses(self):
        doc = {
            "claim": 13, "captured": "2026-09-03T04:43Z", "account": "354867293429", "region": "us-east-2",
            "events": [
                {"eventTime": "2026-09-03T04:39:31Z", "eventName": "CreateTags",
                 "userIdentity.arn": "arn:aws:sts::354867293429:assumed-role/chdf-boundary-alice/chdf-boundary-alice",
                 "resources": ["i-01e1006285c2b37b3"], "tags": {"Name": "database-v2"}, "errorCode": None},
                {"eventTime": "2026-09-03T04:40:32Z", "eventName": "CreateTags",
                 "userIdentity.arn": "arn:aws:sts::354867293429:assumed-role/chdf-boundary-bob/chdf-boundary-bob",
                 "resources": ["i-01e1006285c2b37b3"], "tags": {"tofu-estate": "boundary-data"},
                 "errorCode": "Client.UnauthorizedOperation"},
            ],
        }
        with tempfile.TemporaryDirectory() as d:
            p = pathlib.Path(d) / "ct.json"
            p.write_text(json.dumps(doc))
            ct = receipt.load_cloudtrail(p)
        self.assertEqual(len(ct.events), 2)
        self.assertEqual(ct.events[0].role, "chdf-boundary-alice")
        self.assertEqual([e.role for e in ct.denied], ["chdf-boundary-bob"])
        self.assertEqual(ct.denied[0].tags, {"tofu-estate": "boundary-data"})

    def test_the_repository_file_is_where_receipt_expects_it(self):
        self.assertTrue(receipt.CLOUDTRAIL_EVIDENCE.exists(), receipt.CLOUDTRAIL_EVIDENCE)
        ct = receipt.load_cloudtrail()
        self.assertEqual(len(ct.events), 5)
        self.assertEqual(len(ct.denied), 2)


if __name__ == "__main__":
    unittest.main()
