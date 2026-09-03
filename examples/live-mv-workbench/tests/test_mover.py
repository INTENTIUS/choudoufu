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



class StageBlocks(unittest.TestCase):
    """stage_blocks carries each move's block into its destination config so a
    cross-estate carve can be dry-run, and leaves alone what it must."""

    def setUp(self):
        import tempfile, pathlib
        from tlmig import config
        self.tmp = tempfile.TemporaryDirectory()
        self.cfg = config.Config(run_id="sb01", run_dir=pathlib.Path(self.tmp.name) / "run", binary="choudoufu")

    def tearDown(self):
        self.tmp.cleanup()

    def _write(self, estate, hcl):
        from tlmig import env
        wd = self.cfg.workdir(estate); wd.mkdir(parents=True, exist_ok=True)
        (wd / "main.tf").write_text(hcl)

    def _carve(self, moves):
        from tlmig import carve
        carve.save(self.cfg.run_dir, {"from": "src", "estates": [], "moves": moves, "rules": []})
        return carve.path(self.cfg.run_dir)

    def test_stages_a_cross_estate_block(self):
        from tlmig import mover
        self._write("a", 'terraform {\n  live { estate = "a" }\n}\nresource "aws_iam_role" "r" {\n  name = "x"\n}\n')
        self._write("b", 'terraform {\n  live { estate = "b" }\n}\n')
        cp = self._carve([{"address": "aws_iam_role.r", "from": "a", "to": "b"}])
        rep = mover.stage_blocks(self.cfg, cp)
        self.assertEqual(rep.staged, [("aws_iam_role.r", "b")])
        self.assertIn('"aws_iam_role"', (self.cfg.workdir("b") / "main.tf").read_text())
        self.assertNotIn('"aws_iam_role"', (self.cfg.workdir("a") / "main.tf").read_text())

    def test_already_declared_is_left_untouched(self):
        from tlmig import mover
        self._write("a", 'resource "aws_iam_role" "r" {}\n')
        before = 'terraform {\n  live { estate = "b" }\n}\nresource "aws_iam_role" "r" {\n  name = "y"\n}\n'
        self._write("b", before)
        cp = self._carve([{"address": "aws_iam_role.r", "from": "a", "to": "b"}])
        rep = mover.stage_blocks(self.cfg, cp)
        self.assertEqual(rep.already, [("aws_iam_role.r", "b")])
        self.assertEqual(rep.staged, [])
        self.assertEqual((self.cfg.workdir("b") / "main.tf").read_text(), before)  # unchanged
        self.assertIn('"aws_iam_role"', (self.cfg.workdir("a") / "main.tf").read_text())  # source kept

    def test_indexed_and_rename_are_left_for_the_operator(self):
        from tlmig import mover
        self._write("a", 'resource "aws_x" "y" {}\n')
        self._write("b", "")
        cp = self._carve([
            {"address": "aws_x.y[0]", "from": "a", "to": "b"},
            {"address": "aws_x.y", "from": "a", "to": "b", "new_address": "aws_x.z"},
        ])
        rep = mover.stage_blocks(self.cfg, cp)
        self.assertEqual(sorted(a for a, _ in rep.manual), ["aws_x.y", "aws_x.y[0]"])
        self.assertEqual(rep.staged, [])


    def test_preview_staging_restores_every_config(self):
        from tlmig import mover
        a_before = 'terraform {\n  live { estate = "a" }\n}\nresource "aws_iam_role" "r" {\n  name = "x"\n}\n'
        b_before = 'terraform {\n  live { estate = "b" }\n}\n'
        self._write("a", a_before)
        self._write("b", b_before)
        cp = self._carve([{"address": "aws_iam_role.r", "from": "a", "to": "b"}])
        with mover.staged_for_preview(self.cfg, cp) as rep:
            self.assertEqual(rep.staged, [("aws_iam_role.r", "b")])
            # inside the block the destination sees the staged block
            self.assertIn('"aws_iam_role"', (self.cfg.workdir("b") / "main.tf").read_text())
        # after the block every config is byte-for-byte what it was
        self.assertEqual((self.cfg.workdir("a") / "main.tf").read_text(), a_before)
        self.assertEqual((self.cfg.workdir("b") / "main.tf").read_text(), b_before)


if __name__ == "__main__":
    unittest.main()
