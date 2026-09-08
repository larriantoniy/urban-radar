"""Local contract experiment for deterministic Urban Radar Telegram callbacks.

This is deliberately not a production plugin: its default action rejects every
request, it has no database access, and it never sends a Telegram message.  It
only proves the pinned Hermes plugin boundary used by a future review transport.
"""

from __future__ import annotations

import re
import json
import logging
import os
import subprocess
from dataclasses import dataclass
from typing import Protocol


CALLBACK_PREFIX = "ur:"
CALLBACK_PATTERN = r"^ur:"
MAX_CALLBACK_DATA_BYTES = 64
MAX_DRAFT_ID_DIGITS = 19
_DRAFT_ID_RE = re.compile(r"^[1-9][0-9]{0,18}$")
_ACTIONS = frozenset({"approve", "reject"})
_FINAL_STATUS_LINES = frozenset({
    "✅ Одобрено",
    "❌ Отклонено",
    "✅ Уже было одобрено",
    "❌ Уже было отклонено",
})

logger = logging.getLogger(__name__)


class ReviewAction(Protocol):
    """The only side-effect boundary exposed by this experiment."""

    def approve(self, draft_id: int, actor: str) -> object: ...

    def reject(self, draft_id: int, actor: str) -> object: ...


@dataclass(frozen=True)
class ParsedCallback:
    action: str
    draft_id: int


class ReviewBridgeError(RuntimeError):
    """The deterministic Urban Radar review subprocess did not confirm action."""


class ReviewRuntimeConfigurationError(RuntimeError):
    """The gateway cannot safely invoke the deterministic review command."""


@dataclass(frozen=True)
class ReviewBridgeResult:
    result: str
    draft_id: int
    review_state: str


class RejectingReviewAction:
    """Safe default for an unconfigured experimental plugin."""

    def approve(self, draft_id: int, actor: str) -> None:
        del draft_id, actor
        raise RuntimeError("Urban Radar review action is not configured")

    def reject(self, draft_id: int, actor: str) -> None:
        del draft_id, actor
        raise RuntimeError("Urban Radar review action is not configured")


class CLIReviewAction:
    """Narrow subprocess bridge to Urban Radar's deterministic review CLI."""

    def __init__(self, command: str, runner=subprocess.run) -> None:
        self.command = command
        self.runner = runner

    def approve(self, draft_id: int, actor: str) -> ReviewBridgeResult:
        return self._run("approve", draft_id, actor)

    def reject(self, draft_id: int, actor: str) -> ReviewBridgeResult:
        return self._run("reject", draft_id, actor)

    def _run(self, action: str, draft_id: int, actor: str) -> ReviewBridgeResult:
        completed = self.runner(
            [self.command, "content", "review", action, str(draft_id), "--actor", actor],
            capture_output=True,
            check=False,
            text=True,
        )
        if completed.returncode != 0:
            raise ReviewBridgeError(f"Urban Radar review command exited {completed.returncode}")
        try:
            payload = json.loads(completed.stdout)
        except (TypeError, json.JSONDecodeError) as exc:
            raise ReviewBridgeError("Urban Radar review command returned malformed JSON") from exc
        expected_state = "APPROVED" if action == "approve" else "REJECTED"
        if not isinstance(payload, dict) or payload.get("result") not in {"APPLIED", "IDEMPOTENT"}:
            raise ReviewBridgeError("Urban Radar review command did not confirm a review result")
        if payload.get("draft_id") != draft_id or payload.get("review_state") != expected_state:
            raise ReviewBridgeError("Urban Radar review command confirmed the wrong draft or state")
        return ReviewBridgeResult(payload["result"], draft_id, expected_state)


class CapturingReviewAction:
    """Preserve the bool callback contract while retaining CLI result for UX."""

    def __init__(self, delegate: ReviewAction) -> None:
        self.delegate = delegate
        self.result: object | None = None

    def approve(self, draft_id: int, actor: str) -> object:
        self.result = self.delegate.approve(draft_id, actor)
        return self.result

    def reject(self, draft_id: int, actor: str) -> object:
        self.result = self.delegate.reject(draft_id, actor)
        return self.result


def preflight_review_runtime(environ: dict[str, str] | None = None) -> str:
    """Validate the gateway-owned review bridge without starting it.

    The command and DATABASE_URL belong to the gateway process environment,
    never an interactive shell. Values are intentionally not included in
    errors or logs.
    """
    env = os.environ if environ is None else environ
    command = str(env.get("URBAN_RADAR_REVIEW_COMMAND", "")).strip()
    if not command:
        raise ReviewRuntimeConfigurationError("URBAN_RADAR_REVIEW_COMMAND is not set")
    if not os.path.isabs(command):
        raise ReviewRuntimeConfigurationError("URBAN_RADAR_REVIEW_COMMAND must be an absolute executable path")
    if not os.path.isfile(command) or not os.access(command, os.X_OK):
        raise ReviewRuntimeConfigurationError("URBAN_RADAR_REVIEW_COMMAND is not executable")
    if not str(env.get("DATABASE_URL", "")).strip():
        raise ReviewRuntimeConfigurationError("DATABASE_URL is not set for the gateway process")
    return command


def parse_callback_data(data: object) -> ParsedCallback | None:
    """Accept only ``ur:approve:<content_draft_id>`` or reject equivalent.

    A positive signed-64-bit draft ID is compact, exact, and contains no draft
    text or secret. Its decimal representation fits Telegram's 64-byte limit.
    """
    if not isinstance(data, str):
        return None
    if len(data.encode("utf-8")) > MAX_CALLBACK_DATA_BYTES:
        return None
    parts = data.split(":")
    if len(parts) != 3 or parts[0] != "ur":
        return None
    action, draft_id_text = parts[1], parts[2]
    if action not in _ACTIONS or not _DRAFT_ID_RE.fullmatch(draft_id_text):
        return None
    draft_id = int(draft_id_text)
    if draft_id > 9_223_372_036_854_775_807:
        return None
    return ParsedCallback(action=action, draft_id=draft_id)


def _callback_user_id(query: object) -> str | None:
    user = getattr(query, "from_user", None)
    user_id = getattr(user, "id", None)
    if isinstance(user_id, bool) or user_id is None:
        return None
    value = str(user_id).strip()
    return value or None


async def handle_callback_query(query: object, adapter: object, action: ReviewAction) -> bool:
    """Authorize, parse, then invoke one exact deterministic review action.

    Returns True only after a ReviewAction method was invoked.  This function
    intentionally imports no agent, prompt, model, database, or Telegram client
    code; the PTB callback is its sole input.
    """
    parsed = parse_callback_data(getattr(query, "data", None))
    if parsed is None:
        return False

    user_id = _callback_user_id(query)
    if user_id is None:
        return False

    message = getattr(query, "message", None)
    chat = getattr(message, "chat", None)
    chat_id = getattr(message, "chat_id", None)
    chat_type = getattr(chat, "type", None)
    thread_id = getattr(message, "message_thread_id", None)
    user_name = getattr(getattr(query, "from_user", None), "first_name", None)

    # This is Hermes' pinned callback authorization gate.  Plugin handlers are
    # deliberately responsible for calling it because they run before core.
    is_authorized = getattr(adapter, "_is_callback_user_authorized", None)
    if not callable(is_authorized):
        return False
    if not is_authorized(
        user_id,
        chat_id=chat_id,
        chat_type=chat_type,
        thread_id=thread_id,
        user_name=user_name,
    ):
        return False

    if parsed.action == "approve":
        action.approve(parsed.draft_id, f"telegram:{user_id}")
    else:
        action.reject(parsed.draft_id, f"telegram:{user_id}")
    return True


async def _answer_callback(query: object, text: str, *, show_alert: bool) -> None:
    answer = getattr(query, "answer", None)
    if not callable(answer):
        logger.error("Urban Radar review callback cannot be acknowledged")
        return
    try:
        await answer(text=text, show_alert=show_alert)
    except Exception:
        logger.exception("Urban Radar review callback acknowledgement failed")


def _review_status_line(action: str, result: object | None) -> str | None:
    if isinstance(result, ReviewBridgeResult):
        if action == "approve":
            return "✅ Одобрено" if result.result == "APPLIED" else "✅ Уже было одобрено"
        return "❌ Отклонено" if result.result == "APPLIED" else "❌ Уже было отклонено"
    return None


def _append_review_status(text: str, status: str) -> str:
    """Preserve the original review message and append one final status once."""
    if any(text.endswith(existing) for existing in _FINAL_STATUS_LINES):
        return text
    return text + "\n\n" + status


async def _finalize_review_message(query: object, status: str | None) -> None:
    """Append status and remove controls without emitting another chat message."""
    message = getattr(query, "message", None)
    text = getattr(message, "text", None)
    if not status:
        return
    if not isinstance(text, str):
        logger.error("Urban Radar review callback cannot update review message text")
        return
    updated_text = _append_review_status(text, status)
    try:
        if updated_text != text:
            edit_text = getattr(query, "edit_message_text", None)
            if not callable(edit_text):
                raise RuntimeError("edit_message_text is unavailable")
            await edit_text(text=updated_text, reply_markup=None)
            return
        edit_markup = getattr(query, "edit_message_reply_markup", None)
        if not callable(edit_markup):
            raise RuntimeError("edit_message_reply_markup is unavailable")
        await edit_markup(reply_markup=None)
    except Exception:
        # The review decision is already durable at this point. Do not turn a
        # UI-cleanup failure into a different review outcome.
        logger.exception("Urban Radar review callback message finalization failed")


def _success_message(action: str, result: object | None) -> str:
    if isinstance(result, ReviewBridgeResult):
        if action == "approve":
            return "Одобрено" if result.result == "APPLIED" else "Уже одобрено"
        return "Отклонено" if result.result == "APPLIED" else "Уже отклонено"
    return "Решение сохранено"


def build_telegram_handler(action: ReviewAction):
    """Return a pinned-Hermes PTB handler factory for the ``ur:`` namespace."""

    async def on_callback(update, context) -> None:
        del context
        query = getattr(update, "callback_query", None)
        parsed = parse_callback_data(getattr(query, "data", None))
        captured_action = CapturingReviewAction(action)
        try:
            handled = await handle_callback_query(query, adapter, captured_action)
        except Exception:
            logger.exception("Urban Radar review callback action failed")
            await _answer_callback(query, "Не удалось сохранить решение. Повторите позже.", show_alert=True)
            return
        if not handled or parsed is None:
            await _answer_callback(query, "Действие недоступно.", show_alert=True)
            return
        await _finalize_review_message(query, _review_status_line(parsed.action, captured_action.result))
        await _answer_callback(query, _success_message(parsed.action, captured_action.result), show_alert=False)

    def wire(application, adapter_instance) -> None:
        nonlocal adapter
        adapter = adapter_instance
        # Import lazily, exactly as Hermes' register_telegram_handler contract
        # specifies, so this module remains network- and PTB-independent in tests.
        from telegram.ext import CallbackQueryHandler

        application.add_handler(CallbackQueryHandler(on_callback, pattern=CALLBACK_PATTERN))

    adapter = None
    return wire


def register(ctx) -> None:
    """Pinned Hermes entry point with gateway-owned runtime preflight."""
    command = preflight_review_runtime()
    action: ReviewAction = CLIReviewAction(command)
    ctx.register_telegram_handler(build_telegram_handler(action))
