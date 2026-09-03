import unittest
from tlmig import mover


class MoveBlock(unittest.TestCase):
    def test_moves_top_level_resource(self):
        src = 'resource "aws_iam_role" "r" {\n  name = "x"\n}\nresource "aws_iam_policy" "p" {}\n'
        ns, nd, moved = mover.move_block(src, 'terraform {\n  live { estate = "b" }\n}\n', "aws_iam_role.r")
        self.assertTrue(moved)
        self.assertNotIn('"aws_iam_role"', ns)
        self.assertIn('"aws_iam_policy"', ns)
        self.assertIn('"aws_iam_role"', nd)

    def test_moves_module_call(self):
        ns, nd, moved = mover.move_block('module "net" {\n  source = "./net"\n}\n', "", "module.net")
        self.assertTrue(moved)
        self.assertIn('module "net"', nd)
        self.assertNotIn("module", ns)

    def test_indexed_not_auto_moved(self):
        _, _, moved = mover.move_block('resource "aws_x" "y" {}\n', "", "aws_x.y[0]")
        self.assertFalse(moved)

    def test_missing_block_not_moved(self):
        _, _, moved = mover.move_block('resource "a" "b" {}\n', "", "aws_x.y")
        self.assertFalse(moved)

    def test_nested_braces_kept_whole(self):
        ns, nd, moved = mover.move_block('resource "aws_iam_role" "r" {\n  x { y = 1 }\n}\n', "", "aws_iam_role.r")
        self.assertTrue(moved)
        self.assertIn("x { y = 1 }", nd)
        self.assertEqual(ns.strip(), "")


if __name__ == "__main__":
    unittest.main()
