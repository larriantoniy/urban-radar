"""No-network contract tests for the plugin-owned review notification sender."""

from __future__ import annotations

import asyncio
import importlib.util
import sys
import types
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("review_notify.py")
SPEC = importlib.util.spec_from_file_location("telegram_review_notify", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
notify = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = notify
SPEC.loader.exec_module(notify)


class InlineKeyboardButton:
    def __init__(self, text, callback_data) -> None:
        self.text = text
        self.callback_data = callback_data


class InlineKeyboardMarkup:
    def __init__(self, keyboard) -> None:
        self.inline_keyboard = keyboard


class FakeBot:
    sent = []

    def __init__(self, token) -> None:
        self.token = token

    async def __aenter__(self):
        return self

    async def __aexit__(self, *args):
        return False

    async def send_message(self, **kwargs):
        self.sent.append(kwargs)
        return types.SimpleNamespace(message_id=1234)


class NotificationSenderTests(unittest.TestCase):
    def setUp(self) -> None:
        self.old_modules = {name: sys.modules.get(name) for name in ("telegram", "gateway", "gateway.config")}
        telegram = types.ModuleType("telegram")
        telegram.InlineKeyboardButton = InlineKeyboardButton
        telegram.InlineKeyboardMarkup = InlineKeyboardMarkup
        gateway = types.ModuleType("gateway")
        gateway_config = types.ModuleType("gateway.config")
        self.telegram_platform = object()
        gateway_config.Platform = types.SimpleNamespace(TELEGRAM=self.telegram_platform)
        sys.modules["telegram"] = telegram
        sys.modules["gateway"] = gateway
        sys.modules["gateway.config"] = gateway_config
        FakeBot.sent = []

    def tearDown(self) -> None:
        for name, value in self.old_modules.items():
            if value is None:
                sys.modules.pop(name, None)
            else:
                sys.modules[name] = value

    def request(self, draft_id=42):
        return notify.parse_notification_request(
            {
                "content_draft_id": draft_id,
                "text": "Новый пост готов\n\nТекст\n\nИсточник: https://example.test/source",
                "source_url": "https://example.test/source",
                "approve_callback_data": f"ur:approve:{draft_id}",
                "reject_callback_data": f"ur:reject:{draft_id}",
            }
        )

    def config(self):
        home = types.SimpleNamespace(chat_id="1001", thread_id=None)
        telegram = types.SimpleNamespace(token="not-a-real-token", home_channel=home)
        return types.SimpleNamespace(platforms={self.telegram_platform: telegram})

    def test_sender_uses_hermes_home_channel_and_native_inline_keyboard(self) -> None:
        result = asyncio.run(notify.send_review_notification(self.request(), config_loader=self.config, bot_factory=FakeBot))
        self.assertEqual(result, {"channel": "telegram:1001", "external_id": "1234"})
        self.assertEqual(len(FakeBot.sent), 1)
        sent = FakeBot.sent[0]
        self.assertEqual(sent["chat_id"], "1001")
        self.assertEqual(sent["text"], self.request().text)
        buttons = [button for row in sent["reply_markup"].inline_keyboard for button in row]
        self.assertEqual([(button.text, button.callback_data) for button in buttons], [
            ("✅ Опубликовать", "ur:approve:42"),
            ("❌ Отклонить", "ur:reject:42"),
            ("📷 Добавить фото", "ur:attach:42"),
        ])

    def test_invalid_callback_contract_and_missing_home_channel_fail_before_send(self) -> None:
        with self.assertRaises(ValueError):
            notify.parse_notification_request(
                {"content_draft_id": 42, "text": "x", "source_url": "u", "approve_callback_data": "ur:approve:43", "reject_callback_data": "ur:reject:42"}
            )
        config = types.SimpleNamespace(platforms={self.telegram_platform: types.SimpleNamespace(token="x", home_channel=None)})
        with self.assertRaises(notify.NotificationConfigurationError):
            asyncio.run(notify.send_review_notification(self.request(), config_loader=lambda: config, bot_factory=FakeBot))
        self.assertEqual(FakeBot.sent, [])


if __name__ == "__main__":
    unittest.main()
