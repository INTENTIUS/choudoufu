"""The local pin: CHOUDOUFU_VERSION=local builds this checkout and preflight
accepts what it built, recording the label."""
from __future__ import annotations

import json
import os
import pathlib
import tempfile
import unittest
from unittest import mock

from tlmig import config, guard, localbuild


class FakeProc:
    def __init__(self, rc, out, err=""):
        self.returncode, self.stdout, self.stderr = rc, out, err


class RepoRoot(unittest.TestCase):
    def test_finds_the_checkout_the_example_lives_in(self):
        root = localbuild.repo_root()
        self.assertIsNotNone(root)
        self.assertTrue((root / "cmd" / "choudoufu" / "main.go").exists())

    def test_none_outside_a_checkout(self):
        with tempfile.TemporaryDirectory() as d:
            self.assertIsNone(localbuild.repo_root(pathlib.Path(d) / "x.py"))


class Ensure(unittest.TestCase):
    def test_builds_into_the_cache_keyed_by_describe(self):
        with tempfile.TemporaryDirectory() as d:
            root = pathlib.Path(d)
            calls = []

            def fake_run(argv, **kw):
                calls.append(argv)
                if argv[:2] == ["git", "-C"]:
                    return FakeProc(0, "v0.10.0-3-gabc1234\n")
                pathlib.Path(argv[argv.index("-o") + 1]).write_text("bin")   # go build -o <path>
                return FakeProc(0, "")

            with mock.patch.object(localbuild.subprocess, "run", side_effect=fake_run), \
                 mock.patch.object(localbuild, "cache_dir", return_value=root / "build"):
                path = localbuild.ensure(root, log=lambda m: None)
                self.assertEqual(path, str(root / "build" / "v0.10.0-3-gabc1234" / "choudoufu"))
                self.assertTrue(any(a[:2] == ["go", "build"] for a in calls))
                n = len(calls)
                # clean tree, cached: no second build
                localbuild.ensure(root, log=lambda m: None)
                self.assertEqual([a for a in calls[n:] if a[:2] == ["go", "build"]], [])
                self.assertEqual(localbuild.cached(root), root / "build" / "v0.10.0-3-gabc1234" / "choudoufu")

    def test_a_dirty_tree_never_trusts_the_cache(self):
        with tempfile.TemporaryDirectory() as d:
            root = pathlib.Path(d)
            builds = []

            def fake_run(argv, **kw):
                if argv[:2] == ["git", "-C"]:
                    return FakeProc(0, "abc1234-dirty\n")
                builds.append(argv); pathlib.Path(argv[argv.index("-o") + 1]).write_text("bin"); return FakeProc(0, "")

            with mock.patch.object(localbuild.subprocess, "run", side_effect=fake_run), \
                 mock.patch.object(localbuild, "cache_dir", return_value=root / "build"):
                localbuild.ensure(root, log=lambda m: None); localbuild.ensure(root, log=lambda m: None)
                self.assertEqual(len(builds), 2)
                self.assertIsNone(localbuild.cached(root))

    def test_a_failed_build_says_why(self):
        with tempfile.TemporaryDirectory() as d:
            root = pathlib.Path(d)
            def fake_run(argv, **kw):
                return FakeProc(0, "abc1234\n") if argv[:2] == ["git", "-C"] else FakeProc(2, "", "undefined: foo")
            with mock.patch.object(localbuild.subprocess, "run", side_effect=fake_run), \
                 mock.patch.object(localbuild, "cache_dir", return_value=root / "build"), \
                 self.assertRaises(RuntimeError) as cm:
                localbuild.ensure(root, log=lambda m: None)
            self.assertIn("undefined: foo", str(cm.exception))


class ConfigPin(unittest.TestCase):
    def test_a_release_pin_from_the_environment(self):
        with mock.patch.dict(os.environ, {"CHOUDOUFU_VERSION": "v9.9.9", "CHOUDOUFU_BIN": "/x/choudoufu"}):
            cfg = config.load("abc123")
        self.assertEqual((cfg.version, cfg.build, cfg.binary, cfg.local_build), ("v9.9.9", "v9.9.9", "/x/choudoufu", False))

    def test_the_default_pin_is_the_release_config_names(self):
        with mock.patch.dict(os.environ, {}, clear=False):
            os.environ.pop("CHOUDOUFU_VERSION", None)
            cfg = config.load("abc123")
        self.assertEqual((cfg.version, cfg.build), (config.CHOUDOUFU_VERSION, config.CHOUDOUFU_VERSION))

    def test_a_local_pin_with_an_explicit_binary_does_not_build(self):
        with mock.patch.dict(os.environ, {"CHOUDOUFU_VERSION": "local", "CHOUDOUFU_BIN": "/x/choudoufu"}), \
             mock.patch.object(localbuild, "describe", return_value="v0.10.0-3-gabc1234"), \
             mock.patch.object(localbuild, "ensure", side_effect=AssertionError("must not build")):
            cfg = config.load("abc123")
        self.assertTrue(cfg.local_build)
        self.assertEqual((cfg.binary, cfg.build), ("/x/choudoufu", "local v0.10.0-3-gabc1234"))


class PreflightLocal(unittest.TestCase):
    def _cfg(self, d, version, build):
        return config.Config(run_id="abc123", run_dir=pathlib.Path(d), binary="/x/choudoufu", version=version, build=build)

    def test_accepts_any_choudoufu_build_and_records_it(self):
        with tempfile.TemporaryDirectory() as d:
            cfg = self._cfg(d, "local", "local v0.10.0-3-gabc1234")
            answers = iter([FakeProc(0, "354867293429\n"), FakeProc(0, "choudoufu v0.10.1-dev (based on OpenTofu v1.13.0-dev)\n")])
            with mock.patch.object(guard.subprocess, "run", side_effect=lambda *a, **k: next(answers)):
                guard.preflight(cfg)
            lines = [json.loads(l) for l in (pathlib.Path(d) / "events.jsonl").read_text().splitlines()]
            facts = [e for e in lines if e["kind"] == "fact" and e["label"] == "build"]
            self.assertEqual(len(facts), 1)
            self.assertIn("v0.10.1-dev", facts[0]["value"])
            self.assertIn("local v0.10.0-3-gabc1234", facts[0]["value"])

    def test_a_release_pin_still_refuses_another_build(self):
        with tempfile.TemporaryDirectory() as d:
            cfg = self._cfg(d, "v0.10.0", "v0.10.0")
            answers = iter([FakeProc(0, "354867293429\n"), FakeProc(0, "choudoufu v0.10.1-dev\n")])
            with mock.patch.object(guard.subprocess, "run", side_effect=lambda *a, **k: next(answers)), \
                 self.assertRaises(guard.GuardError) as cm:
                guard.preflight(cfg)
            self.assertIn("CHOUDOUFU_VERSION=local", str(cm.exception))

    def test_a_non_choudoufu_binary_is_refused_under_either_pin(self):
        with tempfile.TemporaryDirectory() as d:
            cfg = self._cfg(d, "local", "local abc")
            answers = iter([FakeProc(0, "354867293429\n"), FakeProc(0, "OpenTofu v1.13.0\n")])
            with mock.patch.object(guard.subprocess, "run", side_effect=lambda *a, **k: next(answers)), \
                 self.assertRaises(guard.GuardError):
                guard.preflight(cfg)


class FetchRelease(unittest.TestCase):
    def test_cached_release_is_returned_without_a_download(self):
        with tempfile.TemporaryDirectory() as d:
            binary = pathlib.Path(d) / "v9.9.9" / "choudoufu"; binary.parent.mkdir(); binary.write_text("bin")
            with mock.patch.object(localbuild, "release_cache", return_value=binary), \
                 mock.patch.object(localbuild.subprocess, "run", side_effect=AssertionError("must not download")):
                self.assertEqual(localbuild.fetch_release("v9.9.9", log=lambda m: None), str(binary))

    def test_a_download_that_fails_says_why(self):
        with tempfile.TemporaryDirectory() as d:
            binary = pathlib.Path(d) / "v9.9.9" / "choudoufu"
            with mock.patch.object(localbuild, "release_cache", return_value=binary), \
                 mock.patch.object(localbuild.subprocess, "run", return_value=FakeProc(1, "", "release not found")), \
                 self.assertRaises(RuntimeError) as cm:
                localbuild.fetch_release("v9.9.9", log=lambda m: None)
            self.assertIn("release not found", str(cm.exception))
