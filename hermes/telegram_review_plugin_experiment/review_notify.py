#!/opt/hermes-venv/bin/python
"""Plugin-owned one-shot Telegram review notification sender.

This deliberately uses pinned Hermes configuration and python-telegram-bot,
but has no agent, prompt, callback handling, or database dependency. It reads
one deterministic JSON payload from stdin and emits only confirmed delivery
identity JSON to stdout.
"""

from __future__ import annotations

import asyncio
import json
import sys
from dataclasses import dataclass
from typing import Any, Callable


MAX_CALLBACK_DATA_BYTES = 64
MAX_TEXT_UTF16_UNITS = 4096


@dataclass(frozen=True)
class NotificationRequest:
    draft_id: int
    text: str
    source_url: str
    approve_callback_data: str
    reject_callback_data: str
    attach_callback_data: str


class NotificationConfigurationError(RuntimeError):
    pass


def parse_notification_request(value: object) -> NotificationRequest:
    if not isinstance(value, dict):
        raise ValueError("notification request must be an object")
    draft_id = value.get("content_draft_id")
    text = value.get("text")
    source_url = value.get("source_url")
    approve = value.get("approve_callback_data")
    reject = value.get("reject_callback_data")
    attach = value.get("attach_callback_data", f"ur:attach:{draft_id}")
    if isinstance(draft_id, bool) or not isinstance(draft_id, int) or draft_id <= 0:
        raise ValueError("content_draft_id must be a positive integer")
    if not all(isinstance(field, str) and field for field in (text, source_url, approve, reject, attach)):
        raise ValueError("notification text, source URL, and callback data are required")
    if not approve == f"ur:approve:{draft_id}" or not reject == f"ur:reject:{draft_id}" or not attach == f"ur:attach:{draft_id}":
        raise ValueError("callback data does not match the Urban Radar contract")
    if any(len(value.encode("utf-8")) > MAX_CALLBACK_DATA_BYTES for value in (approve, reject, attach)):
        raise ValueError("callback data exceeds Telegram limit")
    if len(text.encode("utf-16-le")) // 2 > MAX_TEXT_UTF16_UNITS:
        raise ValueError("notification text exceeds Telegram limit")
    return NotificationRequest(draft_id, text, source_url, approve, reject, attach)


async def send_review_notification(
    request: NotificationRequest,
    *,
    config_loader: Callable[[], Any] | None = None,
    bot_factory: Callable[..., Any] | None = None,
) -> dict[str, str]:
    """Send a plain PTB message to Hermes' configured Telegram home channel."""
    if config_loader is None:
        from gateway.config import load_gateway_config

        config_loader = load_gateway_config
    if bot_factory is None:
        from telegram import Bot

        bot_factory = Bot
    from gateway.config import Platform
    from telegram import InlineKeyboardButton, InlineKeyboardMarkup

    config = config_loader()
    platform_config = config.platforms.get(Platform.TELEGRAM)
    if not platform_config or not platform_config.token:
        raise NotificationConfigurationError("Telegram is not configured in Hermes")
    home = platform_config.home_channel
    if home is None or not str(home.chat_id).strip():
        raise NotificationConfigurationError("TELEGRAM_HOME_CHANNEL is not configured")

    kwargs: dict[str, Any] = {
        "chat_id": home.chat_id,
        "text": request.text,
        "reply_markup": InlineKeyboardMarkup(
            [[
                InlineKeyboardButton("✅ Опубликовать", callback_data=request.approve_callback_data),
                InlineKeyboardButton("❌ Отклонить", callback_data=request.reject_callback_data),
            ], [InlineKeyboardButton("📷 Добавить фото", callback_data=request.attach_callback_data)]]
        ),
    }
    if getattr(home, "thread_id", None):
        try:
            kwargs["message_thread_id"] = int(home.thread_id)
        except (TypeError, ValueError) as exc:
            raise NotificationConfigurationError("Telegram home-channel thread ID must be numeric") from exc

    bot = bot_factory(token=platform_config.token)
    async with bot:
        message = await bot.send_message(**kwargs)
    message_id = getattr(message, "message_id", None)
    if isinstance(message_id, bool) or message_id is None:
        raise RuntimeError("Telegram did not return a message ID")
    return {"channel": f"telegram:{home.chat_id}", "external_id": str(message_id)}


def main() -> int:
    try:
        request = parse_notification_request(json.load(sys.stdin))
        result = asyncio.run(send_review_notification(request))
        json.dump(result, sys.stdout, ensure_ascii=False)
        sys.stdout.write("\n")
        return 0
    except Exception as exc:
        # Do not serialize configuration, token, or provider error internals.
        json.dump({"error": type(exc).__name__}, sys.stdout)
        sys.stdout.write("\n")
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
