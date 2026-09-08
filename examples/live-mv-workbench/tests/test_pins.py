"""The three pins that decide what a demo run actually measures, and the one
place each is declared.

``tlmig/config.py``'s CHOUDOUFU_VERSION is the release preflight asserts. The
compose stack has to ship the same one, in two places (the build ARG that
downloads the binary and the runtime env preflight reads), and the Dockerfile
carries a third as its own default for a `docker build` run outside the
justfile. Before #970 all three read v0.10.1 while the current release was
v0.15.0, and nothing said so: a drifted default fails at the far end, inside
a container, as a version refusal the reader has to decode.

The floci digest is the fourth. The demo runs the engine against the same
emulator the gauntlet measures it on, so ``live/floci-image`` is its source
of truth; a demo pinned to a stale image is a demo whose refusals are a
different emulator's.
"""

from __future__ import annotations

import pathlib
import re
import unittest

from tlmig import config

_HERE = pathlib.Path(__file__).resolve().parent.parent
_COMPOSE = _HERE / "compose" / "docker-compose.yml"
_DOCKERFILE = _HERE / "compose" / "Dockerfile"
# The example lives at examples/live-mv-workbench inside the checkout.
_REPO = _HERE.parent.parent


class VersionPin(unittest.TestCase):
    def test_the_pin_is_a_release_tag(self):
        self.assertRegex(config.CHOUDOUFU_VERSION, r"^v\d+\.\d+\.\d+$")

    def test_compose_defaults_track_config_py(self):
        """Both compose defaults - the build-time download and the runtime
        version preflight asserts - name config.py's release."""
        text = _COMPOSE.read_text()
        defaults = re.findall(r"\$\{(?:RUNTIME_)?CHOUDOUFU_VERSION:-([^}]+)\}", text)
        self.assertEqual(len(defaults), 2, f"expected both compose defaults, found {defaults}")
        for got in defaults:
            self.assertEqual(got, config.CHOUDOUFU_VERSION)

    def test_the_dockerfile_arg_tracks_config_py(self):
        m = re.search(r"^ARG CHOUDOUFU_VERSION=(\S+)$", _DOCKERFILE.read_text(), re.M)
        self.assertIsNotNone(m, "compose/Dockerfile declares no CHOUDOUFU_VERSION ARG")
        self.assertEqual(m.group(1), config.CHOUDOUFU_VERSION)


class FlociPin(unittest.TestCase):
    def test_the_demo_runs_the_emulator_the_gauntlet_measures(self):
        """The compose floci image is live/floci-image's own pin. Skipped
        only when the example is read outside a checkout (a copied-out
        directory), never when the file is present and disagrees."""
        pin = _REPO / "live" / "floci-image"
        if not pin.exists():
            self.skipTest(f"{pin} not present; the example is not inside a choudoufu checkout")
        want = pin.read_text().strip()
        m = re.search(r"\$\{FLOCI_IMAGE:-([^}]+)\}", _COMPOSE.read_text())
        self.assertIsNotNone(m, "compose declares no FLOCI_IMAGE default")
        self.assertEqual(m.group(1), want)


if __name__ == "__main__":
    unittest.main()
