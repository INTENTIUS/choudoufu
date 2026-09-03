"""The stage runs a phase as a subprocess and reads its standing back from
files, so a fake CLI is enough to prove the seam."""
from __future__ import annotations

import json
import pathlib
import sys
import tempfile
import unittest

from tlmig import stage

FAKE_CLI = [sys.executable, "-c",
            "import sys, json, pathlib; phase, run_id = sys.argv[1], sys.argv[2]; "
            "d = pathlib.Path('runs') / run_id; d.mkdir(parents=True, exist_ok=True); "
            "f = open(d / 'events.jsonl', 'a'); "
            "f.write(json.dumps({'ts': '2026-09-03T05:00:00+00:00', 'run_id': run_id, 'phase': phase, 'kind': 'phase', 'name': phase, 'status': 'start'}) + '\\n'); "
            "f.write(json.dumps({'ts': '2026-09-03T05:00:01+00:00', 'run_id': run_id, 'phase': phase, 'kind': 'note', 'text': 'the beat spoke'}) + '\\n'); "
            "f.write(json.dumps({'ts': '2026-09-03T05:00:02+00:00', 'run_id': run_id, 'phase': phase, 'kind': 'phase', 'name': phase, 'status': 'end', 'seconds': 2.0}) + '\\n'); "
            "print('narration on stdout'); sys.exit(0 if phase != 'boom' else 3)",
            "{phase}", "{run_id}"]


class Running(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.cwd = pathlib.Path.cwd()
        import os
        os.chdir(self.tmp.name)

    def tearDown(self):
        import os
        os.chdir(self.cwd)
        self.tmp.cleanup()

    def test_a_phase_runs_logs_and_is_read_back_from_events(self):
        st = stage.Stage("abc123", cli=FAKE_CLI)
        self.assertEqual(st.status("setup"), "not started")
        rec = st.start("setup")
        self.assertEqual(stage.wait(rec, timeout=30), 0)
        self.assertEqual(st.status("setup"), "done")
        self.assertIn("narration on stdout", st.tail("setup"))
        self.assertEqual(st.notes("setup"), ["the beat spoke"])
        # A second Stage over the same run reads the phase's standing from
        # the event log, the way a phase run from a terminal would show.
        self.assertEqual(stage.Stage("abc123", cli=FAKE_CLI).status("setup"), "done")

    def test_a_failing_phase_says_so(self):
        st = stage.Stage("abc123", cli=FAKE_CLI)
        rec = st.start("boom")
        self.assertEqual(stage.wait(rec, timeout=30), 3)
        self.assertEqual(st.status("boom"), "failed (exit 3)")

    def test_starting_twice_while_running_returns_the_same_record(self):
        st = stage.Stage("abc123", cli=[sys.executable, "-c", "import time; time.sleep(1)", "{phase}", "{run_id}"])
        a = st.start("slow")
        b = st.start("slow")
        self.assertIs(a, b)
        stage.wait(a, timeout=30)

    def test_argv_fills_the_template(self):
        st = stage.Stage("abc123")
        self.assertEqual(st.argv("carve")[-4:], ["carve", "--run", "abc123", "--auto"])


class Registry(unittest.TestCase):
    def setUp(self):
        import os
        self.tmp = tempfile.TemporaryDirectory(); self.cwd = pathlib.Path.cwd(); os.chdir(self.tmp.name)

    def tearDown(self):
        import os
        os.chdir(self.cwd); self.tmp.cleanup()

    def test_for_run_returns_the_same_stage_and_keeps_its_phases(self):
        a = stage.for_run("reg111", cli=[sys.executable, "-c", "import time; time.sleep(0.3)", "{phase}", "{run_id}"])
        rec = a.click("setup", 1)
        b = stage.for_run("reg111", binary="/x/choudoufu")
        self.assertIs(a, b)
        self.assertIs(b.phases["setup"], rec)
        self.assertIs(b.click("setup", 1), rec)        # the same click count starts nothing new
        self.assertIs(b.click("setup", 1).proc, rec.proc)
        self.assertEqual(b.env["CHOUDOUFU_BIN"], "/x/choudoufu")
        stage.wait(rec, timeout=30)


class OneAtATime(unittest.TestCase):
    def test_a_click_during_another_phase_is_refused_and_forgotten(self):
        st = stage.Stage("busy11", cli=[sys.executable, "-c", "import time; time.sleep(1.5)", "{phase}", "{run_id}"])
        a = st.click("setup", 1)
        self.assertIsNone(st.click("slow-plan", 1))
        self.assertEqual(st.refused, ("slow-plan", "setup"))
        self.assertNotIn("slow-plan", st.phases)
        stage.wait(a, timeout=30)
        self.assertIsNotNone(st.click("slow-plan", 2))     # the next click, once setup ended, starts it
        stage.wait(st.phases["slow-plan"], timeout=30)
