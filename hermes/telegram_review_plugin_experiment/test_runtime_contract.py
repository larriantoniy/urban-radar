"""Static production-runtime contract tests; no Docker or network required."""

from __future__ import annotations

import re
import unittest
from pathlib import Path


REPOSITORY_ROOT = Path(__file__).resolve().parents[2]


def compose_service(compose: str, name: str) -> str:
    match = re.search(rf"^  {re.escape(name)}:\n(.*?)(?=^  [A-Za-z0-9_-]+:|\Z)", compose, re.MULTILINE | re.DOTALL)
    if match is None:
        raise AssertionError(f"Compose service {name!r} is absent")
    return match.group(1)


class ProductionRuntimeContractTests(unittest.TestCase):
    def test_pinned_hermes_messaging_extra_and_offline_import_smoke_are_built(self) -> None:
        dockerfile = (REPOSITORY_ROOT / "Dockerfile").read_text(encoding="utf-8")
        self.assertIn("uv sync --locked --extra messaging", dockerfile)
        self.assertIn("from telegram import Bot, InlineKeyboardButton, InlineKeyboardMarkup", dockerfile)

    def test_one_shot_review_plugin_receives_the_gateway_loader_contract(self) -> None:
        compose = (REPOSITORY_ROOT / "compose.yaml").read_text(encoding="utf-8")
        one_shot = compose_service(compose, "urban-radar")
        self.assertIn("URBAN_RADAR_REVIEW_COMMAND: /usr/local/bin/urban-radar", one_shot)
        self.assertIn("URBAN_RADAR_REVIEW_COMMAND_TIMEOUT_SECONDS: ${URBAN_RADAR_REVIEW_COMMAND_TIMEOUT_SECONDS:-45}", one_shot)


if __name__ == "__main__":
    unittest.main()
