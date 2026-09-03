import unittest
from tlmig import adopt


class ToLiveHcl(unittest.TestCase):
    def test_strips_backend_adds_live(self):
        out = adopt.to_live_hcl('terraform {\n  backend "s3" { bucket = "x" }\n}\nresource "aws_iam_role" "r" {}\n', "est")
        self.assertNotIn("backend", out)
        self.assertIn('estate = "est"', out)
        self.assertIn("resource", out)

    def test_nested_backend_removed_whole(self):
        out = adopt.to_live_hcl('terraform {\n  backend "s3" {\n    assume_role { role_arn = "y" }\n  }\n}\n', "est")
        self.assertNotIn("assume_role", out)
        self.assertIn("live {", out)

    def test_no_terraform_block_adds_one(self):
        out = adopt.to_live_hcl('resource "aws_iam_role" "r" {}\n', "est")
        self.assertIn("terraform {", out)
        self.assertIn('estate = "est"', out)

    def test_already_live_left_alone(self):
        out = adopt.to_live_hcl('terraform {\n  live { estate = "x" }\n}\n', "est")
        self.assertEqual(out.count("live {"), 1)


class ReportCount(unittest.TestCase):
    def test_count(self):
        self.assertEqual(adopt._count("a VERIFIED\nb VERIFIED\nc DRIFTED\n", "VERIFIED"), 2)
        self.assertEqual(adopt._count("a MISSING\n", "MISSING"), 1)


if __name__ == "__main__":
    unittest.main()
