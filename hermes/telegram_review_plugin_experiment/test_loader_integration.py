"""Pinned-Hermes loader integration test for the review callback experiment.

Run with the Python interpreter from the pinned Hermes checkout:

  HERMES_PINNED_SOURCE=/path/to/hermes-agent \
    /path/to/hermes-agent/venv/bin/python -m unittest \
    hermes/telegram_review_plugin_experiment/test_loader_integration.py

No Telegram SDK/network is required.  The actual Hermes plugin loader and
TelegramAdapter registration methods run; only PTB's in-memory Application
registry is replaced because python-telegram-bot is intentionally absent.
"""

from __future__ import annotations

import importlib
import importlib.util
import asyncio
import os
import re
import shutil
import subprocess
import sys
import tempfile
import types
import unittest
from pathlib import Path


PINNED_HERMES_COMMIT = "05f548f35dd3242bf2ff74743e9112acde251f77"
PLUGIN_NAME = "urban-radar-telegram-review-experiment"
PLUGIN_SOURCE = Path(__file__).resolve().parent


class _Filter:
    def __and__(self, other):
        return self

    def __or__(self, other):
        return self

    def __invert__(self):
        return self


class _Handler:
    def __init__(self, *args, **kwargs) -> None:
        self.args = args
        self.pattern = kwargs.get("pattern")


class _CallbackQueryHandler(_Handler):
    pass


class _InlineKeyboardButton:
    def __init__(self, text, callback_data) -> None:
        self.text = text
        self.callback_data = callback_data


class _InlineKeyboardMarkup:
    def __init__(self, keyboard) -> None:
        self.inline_keyboard = keyboard


class _FakeBot:
    sent: list[dict] = []

    def __init__(self, token) -> None:
        self.token = token

    async def __aenter__(self):
        return self

    async def __aexit__(self, *args):
        return False

    async def send_message(self, **kwargs):
        self.sent.append(kwargs)
        return types.SimpleNamespace(message_id=701)


class _FakeApplication:
    def __init__(self) -> None:
        self.handlers: dict[int, list[object]] = {}

    def add_handler(self, handler: object, group: int = 0) -> None:
        self.handlers.setdefault(group, []).append(handler)


def _install_ptb_wiring_fakes() -> None:
    """Provide only the PTB symbols used by TelegramAdapter._register_handlers."""
    telegram = types.ModuleType("telegram")

    telegram.InlineKeyboardButton = _InlineKeyboardButton
    telegram.InlineKeyboardMarkup = _InlineKeyboardMarkup
    telegram_ext = types.ModuleType("telegram.ext")
    telegram_ext.CallbackQueryHandler = _CallbackQueryHandler
    sys.modules["telegram"] = telegram
    sys.modules["telegram.ext"] = telegram_ext


class HermesReviewPluginLoaderIntegrationTests(unittest.TestCase):
    def setUp(self) -> None:
        source_value = os.environ.get("HERMES_PINNED_SOURCE", "").strip()
        if not source_value:
            self.fail("HERMES_PINNED_SOURCE must point at the pinned Hermes checkout")
        self.hermes_source = Path(source_value).resolve()
        actual_commit = subprocess.check_output(
            ["git", "-C", str(self.hermes_source), "rev-parse", "HEAD"], text=True
        ).strip()
        self.assertEqual(actual_commit, PINNED_HERMES_COMMIT)

        self.tempdir = tempfile.TemporaryDirectory(prefix="urban-radar-hermes-loader-")
        self.addCleanup(self.tempdir.cleanup)
        self.hermes_home = Path(self.tempdir.name)
        self.plugin_target = self.hermes_home / "plugins" / PLUGIN_NAME
        shutil.copytree(PLUGIN_SOURCE, self.plugin_target, ignore=shutil.ignore_patterns("__pycache__", "*.pyc"))
        (self.hermes_home / "config.yaml").write_text(
            "plugins:\n  enabled:\n    - urban-radar-telegram-review-experiment\n",
            encoding="utf-8",
        )

        self.previous_home = os.environ.get("HERMES_HOME")
        self.previous_review_command = os.environ.get("URBAN_RADAR_REVIEW_COMMAND")
        self.previous_database_url = os.environ.get("DATABASE_URL")
        os.environ["HERMES_HOME"] = str(self.hermes_home)
        os.environ["URBAN_RADAR_REVIEW_COMMAND"] = sys.executable
        os.environ["DATABASE_URL"] = "postgres://test"
        sys.path.insert(0, str(self.hermes_source))
        self.addCleanup(self._restore_environment)

        # Imports happen only after HERMES_HOME is isolated.
        self.plugins = importlib.import_module("hermes_cli.plugins")
        self.plugins._reset_plugin_managers_for_tests()

    def _restore_environment(self) -> None:
        self.plugins._reset_plugin_managers_for_tests()
        if str(self.hermes_source) in sys.path:
            sys.path.remove(str(self.hermes_source))
        if self.previous_home is None:
            os.environ.pop("HERMES_HOME", None)
        else:
            os.environ["HERMES_HOME"] = self.previous_home
        if self.previous_review_command is None:
            os.environ.pop("URBAN_RADAR_REVIEW_COMMAND", None)
        else:
            os.environ["URBAN_RADAR_REVIEW_COMMAND"] = self.previous_review_command
        if self.previous_database_url is None:
            os.environ.pop("DATABASE_URL", None)
        else:
            os.environ["DATABASE_URL"] = self.previous_database_url

    def test_real_loader_registers_urban_radar_handler_before_core_catch_all(self) -> None:
        # Real pinned discovery: HERMES_HOME/plugins -> manifest -> register(ctx).
        self.plugins.discover_plugins(force=True)
        manager = self.plugins.get_plugin_manager()

        loaded = [
            plugin
            for plugin in manager._plugins.values()
            if plugin.manifest.name == PLUGIN_NAME
        ]
        self.assertEqual(len(loaded), 1)
        self.assertTrue(loaded[0].enabled)
        self.assertIsNotNone(loaded[0].module)

        factories = manager.get_platform_handler_factories("telegram")
        urban_factories = [factory for factory, name in factories if name == PLUGIN_NAME]
        self.assertEqual(len(urban_factories), 1)

        # Keep the real adapter wiring methods; fake only unavailable PTB's
        # in-memory handler registry. No adapter connect(), polling, or HTTP.
        _install_ptb_wiring_fakes()
        adapter_module = importlib.import_module("plugins.platforms.telegram.adapter")
        adapter_module.CallbackQueryHandler = _CallbackQueryHandler
        adapter_module.TelegramMessageHandler = _Handler
        adapter_module.InlineQueryHandler = _Handler
        adapter_module.TypeHandler = _Handler
        adapter_module.Update = object
        adapter_module.filters = types.SimpleNamespace(
            TEXT=_Filter(),
            COMMAND=_Filter(),
            LOCATION=_Filter(),
            VENUE=_Filter(),
            PHOTO=_Filter(),
            VIDEO=_Filter(),
            AUDIO=_Filter(),
            VOICE=_Filter(),
            Document=types.SimpleNamespace(ALL=_Filter()),
            Sticker=types.SimpleNamespace(ALL=_Filter()),
        )

        gateway_config = importlib.import_module("gateway.config")
        adapter = adapter_module.TelegramAdapter(
            gateway_config.PlatformConfig(enabled=True, token="test-token")
        )
        application = _FakeApplication()

        # This is the exact pinned connect-time ordering: plugin factories first,
        # then TelegramAdapter._register_handlers() adds its catch-all callback.
        adapter._wire_plugin_handlers(application)
        adapter._register_handlers(application)

        callbacks = [
            handler
            for handler in application.handlers[0]
            if isinstance(handler, _CallbackQueryHandler)
        ]
        self.assertGreaterEqual(len(callbacks), 2)
        self.assertEqual(callbacks[0].pattern, r"^ur:")
        self.assertIsNone(callbacks[1].pattern)

        urban_pattern = re.compile(callbacks[0].pattern)
        self.assertIsNotNone(urban_pattern.match("ur:approve:19"))
        self.assertIsNone(urban_pattern.match("ea:once:17"))
        self.assertIsNone(urban_pattern.match("sc:once:confirm"))

    def test_loaded_plugin_owned_sender_forms_pinned_ptb_inline_keyboard_call(self) -> None:
        self.plugins.discover_plugins(force=True)
        manager = self.plugins.get_plugin_manager()
        loaded = [plugin for plugin in manager._plugins.values() if plugin.manifest.name == PLUGIN_NAME]
        self.assertEqual(len(loaded), 1)
        self.assertTrue(loaded[0].enabled)

        _install_ptb_wiring_fakes()
        spec = importlib.util.spec_from_file_location("loaded_urban_radar_review_notify", self.plugin_target / "review_notify.py")
        assert spec is not None and spec.loader is not None
        notify = importlib.util.module_from_spec(spec)
        sys.modules[spec.name] = notify
        spec.loader.exec_module(notify)

        gateway_config = importlib.import_module("gateway.config")
        home = types.SimpleNamespace(chat_id="1001", thread_id=None)
        telegram_config = types.SimpleNamespace(token="test-token", home_channel=home)
        config = types.SimpleNamespace(platforms={gateway_config.Platform.TELEGRAM: telegram_config})
        request = notify.parse_notification_request({
            "content_draft_id": 19,
            "text": "Новый пост готов\n\nТекст\n\nИсточник: https://example.test/source",
            "source_url": "https://example.test/source",
            "approve_callback_data": "ur:approve:19",
            "reject_callback_data": "ur:reject:19",
        })
        _FakeBot.sent = []
        result = asyncio.run(notify.send_review_notification(request, config_loader=lambda: config, bot_factory=_FakeBot))
        self.assertEqual(result, {"channel": "telegram:1001", "external_id": "701"})
        self.assertEqual(len(_FakeBot.sent), 1)
        buttons = _FakeBot.sent[0]["reply_markup"].inline_keyboard[0]
        self.assertEqual([(button.text, button.callback_data) for button in buttons], [
            ("✅ Опубликовать", "ur:approve:19"),
            ("❌ Отклонить", "ur:reject:19"),
        ])


if __name__ == "__main__":
    unittest.main()
