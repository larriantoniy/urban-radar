"""No-network tests for the Urban Radar Telegram review callback contract."""

from __future__ import annotations

import asyncio
import importlib.util
import json
import sys
import types
import unittest
from pathlib import Path
from unittest import mock


MODULE_PATH = Path(__file__).with_name("__init__.py")
SPEC = importlib.util.spec_from_file_location("telegram_review_contract", MODULE_PATH)
assert SPEC is not None and SPEC.loader is not None
contract = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = contract
SPEC.loader.exec_module(contract)


class FakeReviewAction:
    def __init__(self) -> None:
        self.calls: list[tuple[str, int, str]] = []

    def approve(self, draft_id: int, actor: str) -> None:
        self.calls.append(("approve", draft_id, actor))

    def reject(self, draft_id: int, actor: str) -> None:
        self.calls.append(("reject", draft_id, actor))

    def publish(self, draft_id: int) -> dict[str, object]:
        del draft_id
        return {"result": "BLOCKED", "reason": "TEST"}


class FakeAdapter:
    def __init__(self, authorized: bool) -> None:
        self.authorized = authorized
        self.auth_calls: list[tuple[str, dict[str, object]]] = []

    def _is_callback_user_authorized(self, actor: str, **kwargs: object) -> bool:
        self.auth_calls.append((actor, kwargs))
        return self.authorized


def callback(data: object, user_id: object = 42):
    user = types.SimpleNamespace(id=user_id, first_name="Radar")
    chat = types.SimpleNamespace(type="private")
    message = types.SimpleNamespace(chat_id=1001, chat=chat, message_thread_id=None)
    return types.SimpleNamespace(data=data, from_user=user, message=message)


class FakeCallbackQuery:
    def __init__(self, data: object, *, user_id: object = 42, text: str = "Новый пост готов\n\nТекст\n\nИсточник: https://example.test") -> None:
        self.data = data
        self.from_user = types.SimpleNamespace(id=user_id, first_name="Radar")
        self.message = types.SimpleNamespace(chat_id=1001, chat=types.SimpleNamespace(type="private"), message_thread_id=None, text=text)
        self.answers: list[dict[str, object]] = []
        self.reply_markup_edits: list[object] = []
        self.text_edits: list[dict[str, object]] = []

    async def answer(self, **kwargs) -> None:
        self.answers.append(kwargs)

    async def edit_message_reply_markup(self, **kwargs) -> None:
        self.reply_markup_edits.append(kwargs.get("reply_markup"))

    async def edit_message_text(self, **kwargs) -> None:
        self.text_edits.append(kwargs)


class ResultReviewAction(FakeReviewAction):
    def __init__(self, result: str = "APPLIED", error: Exception | None = None) -> None:
        super().__init__()
        self.result = result
        self.error = error

    def approve(self, draft_id: int, actor: str) -> object:
        if self.error:
            raise self.error
        super().approve(draft_id, actor)
        return contract.ReviewBridgeResult(self.result, draft_id, "APPROVED")

    def reject(self, draft_id: int, actor: str) -> object:
        if self.error:
            raise self.error
        super().reject(draft_id, actor)
        return contract.ReviewBridgeResult(self.result, draft_id, "REJECTED")

    def publish(self, draft_id: int) -> dict[str, object]:
        return {"result": "PUBLISHED", "publication_id": 7, "external_post_id": "987"}


class BridgeAction(ResultReviewAction):
    def __init__(self, publication: dict[str, object], *, approve_error: Exception | None = None) -> None:
        super().__init__("APPLIED", approve_error)
        self.publication = publication
        self.events: list[str] = []

    def approve(self, draft_id: int, actor: str) -> object:
        self.events.append("approve")
        return super().approve(draft_id, actor)

    def publish(self, draft_id: int) -> dict[str, object]:
        del draft_id
        self.events.append("publish")
        return self.publication

    def retry(self, publication_id: int) -> dict[str, object]:
        self.events.append(f"retry:{publication_id}")
        return self.publication


class CallbackContractTests(unittest.TestCase):
    def invoke(self, data: object, *, authorized: bool = True, user_id: object = 42):
        action = FakeReviewAction()
        adapter = FakeAdapter(authorized)
        handled = asyncio.run(contract.handle_callback_query(callback(data, user_id), adapter, action))
        return handled, action, adapter

    def test_authorized_approve_invokes_exactly_once(self) -> None:
        handled, action, adapter = self.invoke("ur:approve:19")
        self.assertTrue(handled)
        self.assertEqual(action.calls, [("approve", 19, "telegram:42")])
        self.assertEqual(len(adapter.auth_calls), 1)

    def test_authorized_reject_invokes_exactly_once(self) -> None:
        handled, action, _ = self.invoke("ur:reject:29")
        self.assertTrue(handled)
        self.assertEqual(action.calls, [("reject", 29, "telegram:42")])

    def test_attach_token_is_parsed_but_not_a_review_decision(self) -> None:
        self.assertEqual(contract.parse_callback_data("ur:attach:29"), contract.ParsedCallback("attach", 29))
        handled, action, adapter = self.invoke("ur:attach:29")
        self.assertFalse(handled)
        self.assertEqual(action.calls, [])
        self.assertEqual(len(adapter.auth_calls), 1)

    def test_unauthorized_user_cannot_invoke_action(self) -> None:
        handled, action, adapter = self.invoke("ur:approve:19", authorized=False)
        self.assertFalse(handled)
        self.assertEqual(action.calls, [])
        self.assertEqual(len(adapter.auth_calls), 1)

    def test_missing_user_identity_cannot_invoke_action(self) -> None:
        handled, action, adapter = self.invoke("ur:approve:19", user_id=None)
        self.assertFalse(handled)
        self.assertEqual(action.calls, [])
        self.assertEqual(adapter.auth_calls, [])

    def test_wrong_namespace_cannot_invoke_action(self) -> None:
        handled, action, adapter = self.invoke("other:approve:19")
        self.assertFalse(handled)
        self.assertEqual(action.calls, [])
        self.assertEqual(adapter.auth_calls, [])

    def test_malformed_callbacks_cannot_invoke_action(self) -> None:
        for data in ("ur:", "ur:approve", "ur:approve:", "ur:approve:a:b", "ur:approve:0", "ur:approve:-1"):
            with self.subTest(data=data):
                handled, action, adapter = self.invoke(data)
                self.assertFalse(handled)
                self.assertEqual(action.calls, [])
                self.assertEqual(adapter.auth_calls, [])

    def test_unknown_action_cannot_invoke_action(self) -> None:
        handled, action, adapter = self.invoke("ur:publish:19")
        self.assertFalse(handled)
        self.assertEqual(action.calls, [])
        self.assertEqual(adapter.auth_calls, [])

    def test_draft_id_is_propagated_without_transformation(self) -> None:
        draft_id = 9_223_372_036_854_775_807
        handled, action, _ = self.invoke(f"ur:approve:{draft_id}")
        self.assertTrue(handled)
        self.assertEqual(action.calls[0][1], draft_id)

    def test_parser_enforces_telegram_callback_limit_and_token_shape(self) -> None:
        self.assertIsNone(contract.parse_callback_data("ur:approve:" + "1" * 20))
        self.assertIsNone(contract.parse_callback_data("ur:approve:has space"))
        self.assertIsNone(contract.parse_callback_data("ur:approve:" + "1" * 60))


class HermesWiringContractTests(unittest.TestCase):
    def invoke_bridge(self, action, query, *, authorized=True):
        captured = []

        class CallbackQueryHandler:
            def __init__(self, callback_fn, pattern: str) -> None:
                self.callback_fn = callback_fn
                self.pattern = pattern

        class InlineKeyboardButton:
            def __init__(self, text, callback_data) -> None:
                self.text, self.callback_data = text, callback_data

        class InlineKeyboardMarkup:
            def __init__(self, keyboard) -> None:
                self.inline_keyboard = keyboard

        telegram, telegram_ext = types.ModuleType("telegram"), types.ModuleType("telegram.ext")
        telegram.InlineKeyboardButton, telegram.InlineKeyboardMarkup = InlineKeyboardButton, InlineKeyboardMarkup
        telegram_ext.CallbackQueryHandler = CallbackQueryHandler
        old_telegram, old_telegram_ext = sys.modules.get("telegram"), sys.modules.get("telegram.ext")
        sys.modules["telegram"], sys.modules["telegram.ext"] = telegram, telegram_ext
        try:
            contract.build_telegram_handler(action)(types.SimpleNamespace(add_handler=captured.append), FakeAdapter(authorized))
            asyncio.run(captured[0].callback_fn(types.SimpleNamespace(callback_query=query), object()))
        finally:
            if old_telegram is None:
                del sys.modules["telegram"]
            else:
                sys.modules["telegram"] = old_telegram
            if old_telegram_ext is None:
                del sys.modules["telegram.ext"]
            else:
                sys.modules["telegram.ext"] = old_telegram_ext
        return query

    def test_approval_commits_before_one_publisher_invocation(self) -> None:
        action = BridgeAction({"result": "FAILED", "publication_id": 7})
        query = self.invoke_bridge(action, FakeCallbackQuery("ur:approve:19"))
        self.assertEqual(action.events, ["approve", "publish"])
        buttons = [button for row in query.text_edits[0]["reply_markup"].inline_keyboard for button in row]
        self.assertEqual([(button.text, button.callback_data) for button in buttons], [("🔁 Повторить публикацию", "ur:pub-retry:7")])

    def test_approval_failure_does_not_invoke_publisher(self) -> None:
        action = BridgeAction({"result": "PUBLISHED"}, approve_error=contract.ReviewBridgeError("no"))
        with self.assertLogs(contract.logger, "ERROR"):
            query = self.invoke_bridge(action, FakeCallbackQuery("ur:approve:19"))
        self.assertEqual(action.events, ["approve"])
        self.assertEqual(query.text_edits, [])

    def test_recovery_result_renders_reconciliation_controls(self) -> None:
        action = BridgeAction({"result": "RECOVERY_REQUIRED", "publication_id": 7})
        query = self.invoke_bridge(action, FakeCallbackQuery("ur:approve:19"))
        buttons = [button for row in query.text_edits[0]["reply_markup"].inline_keyboard for button in row]
        self.assertEqual([(button.text, button.callback_data) for button in buttons], [
            ("✅ Пост опубликован", "ur:pub-found:7"),
            ("❌ Пост не появился", "ur:pub-missing:7"),
        ])

    def test_unauthorized_retry_never_invokes_publisher(self) -> None:
        action = BridgeAction({"result": "PUBLISHED"})
        query = self.invoke_bridge(action, FakeCallbackQuery("ur:pub-retry:7"), authorized=False)
        self.assertEqual(action.events, [])
        self.assertEqual(query.text_edits, [])

    def test_retry_passes_persisted_publication_id_to_bridge(self) -> None:
        action = BridgeAction({"result": "PUBLISHED", "publication_id": 7})
        self.invoke_bridge(action, FakeCallbackQuery("ur:pub-retry:7"))
        self.assertEqual(action.events, ["retry:7"])

    def test_final_status_lines_cover_applied_and_idempotent_decisions(self) -> None:
        self.assertEqual(contract._review_status_line("approve", contract.ReviewBridgeResult("APPLIED", 19, "APPROVED")), "✅ Одобрено")
        self.assertEqual(contract._review_status_line("reject", contract.ReviewBridgeResult("APPLIED", 19, "REJECTED")), "❌ Отклонено")
        self.assertEqual(contract._review_status_line("approve", contract.ReviewBridgeResult("IDEMPOTENT", 19, "APPROVED")), "✅ Уже было одобрено")
        self.assertEqual(contract._review_status_line("reject", contract.ReviewBridgeResult("IDEMPOTENT", 19, "REJECTED")), "❌ Уже было отклонено")

    def test_register_uses_pinned_telegram_handler_api(self) -> None:
        factories = []
        context = types.SimpleNamespace(register_telegram_handler=factories.append)
        with mock.patch.dict(contract.os.environ, {
            "URBAN_RADAR_REVIEW_COMMAND": sys.executable,
            "DATABASE_URL": "postgres://test",
        }, clear=False):
            contract.register(context)
        self.assertEqual(len(factories), 1)

    def test_factory_registers_only_ur_namespace_handler(self) -> None:
        captured = []
        action = FakeReviewAction()
        adapter = FakeAdapter(True)

        class CallbackQueryHandler:
            def __init__(self, callback_fn, pattern: str) -> None:
                self.callback_fn = callback_fn
                self.pattern = pattern

        telegram = types.ModuleType("telegram")
        telegram_ext = types.ModuleType("telegram.ext")
        telegram_ext.CallbackQueryHandler = CallbackQueryHandler
        old_telegram = sys.modules.get("telegram")
        old_telegram_ext = sys.modules.get("telegram.ext")
        sys.modules["telegram"] = telegram
        sys.modules["telegram.ext"] = telegram_ext
        try:
            application = types.SimpleNamespace(add_handler=captured.append)
            contract.build_telegram_handler(action)(application, adapter)
            asyncio.run(
                captured[0].callback_fn(
                    types.SimpleNamespace(callback_query=FakeCallbackQuery("ur:approve:19")),
                    object(),
                )
            )
        finally:
            if old_telegram is None:
                del sys.modules["telegram"]
            else:
                sys.modules["telegram"] = old_telegram
            if old_telegram_ext is None:
                del sys.modules["telegram.ext"]
            else:
                sys.modules["telegram.ext"] = old_telegram_ext

        self.assertEqual(len(captured), 1)
        self.assertEqual(captured[0].pattern, r"^ur:")
        self.assertEqual(action.calls, [("approve", 19, "telegram:42")])

    def test_applied_callback_acknowledges_and_removes_keyboard(self) -> None:
        captured = []
        action = ResultReviewAction("APPLIED")
        adapter = FakeAdapter(True)

        class CallbackQueryHandler:
            def __init__(self, callback_fn, pattern: str) -> None:
                self.callback_fn = callback_fn
                self.pattern = pattern

        telegram = types.ModuleType("telegram")
        telegram_ext = types.ModuleType("telegram.ext")
        telegram_ext.CallbackQueryHandler = CallbackQueryHandler
        old_telegram, old_telegram_ext = sys.modules.get("telegram"), sys.modules.get("telegram.ext")
        sys.modules["telegram"] = telegram
        sys.modules["telegram.ext"] = telegram_ext
        try:
            contract.build_telegram_handler(action)(types.SimpleNamespace(add_handler=captured.append), adapter)
            query = FakeCallbackQuery("ur:approve:19")
            asyncio.run(captured[0].callback_fn(types.SimpleNamespace(callback_query=query), object()))
        finally:
            if old_telegram is None:
                del sys.modules["telegram"]
            else:
                sys.modules["telegram"] = old_telegram
            if old_telegram_ext is None:
                del sys.modules["telegram.ext"]
            else:
                sys.modules["telegram.ext"] = old_telegram_ext

        self.assertEqual(action.calls, [("approve", 19, "telegram:42")])
        self.assertEqual(query.reply_markup_edits, [])
        self.assertEqual(query.text_edits, [{
            "text": "Новый пост готов\n\nТекст\n\nИсточник: https://example.test\n\n✅ Одобрено\n✅ Опубликовано\nVK post ID: 987",
            "reply_markup": None,
        }])
        self.assertEqual(query.answers, [{"text": "Одобрено", "show_alert": False}])

    def test_idempotent_callback_is_acknowledged_and_removes_keyboard(self) -> None:
        query = FakeCallbackQuery("ur:reject:19")
        action = ResultReviewAction("IDEMPOTENT")
        handler = contract.build_telegram_handler(action)

        class CallbackQueryHandler:
            def __init__(self, callback_fn, pattern: str) -> None:
                self.callback_fn = callback_fn
                self.pattern = pattern

        telegram, telegram_ext = types.ModuleType("telegram"), types.ModuleType("telegram.ext")
        telegram_ext.CallbackQueryHandler = CallbackQueryHandler
        old_telegram, old_telegram_ext = sys.modules.get("telegram"), sys.modules.get("telegram.ext")
        sys.modules["telegram"], sys.modules["telegram.ext"] = telegram, telegram_ext
        try:
            captured = []
            handler(types.SimpleNamespace(add_handler=captured.append), FakeAdapter(True))
            asyncio.run(captured[0].callback_fn(types.SimpleNamespace(callback_query=query), object()))
        finally:
            if old_telegram is None:
                del sys.modules["telegram"]
            else:
                sys.modules["telegram"] = old_telegram
            if old_telegram_ext is None:
                del sys.modules["telegram.ext"]
            else:
                sys.modules["telegram.ext"] = old_telegram_ext

        self.assertEqual(query.reply_markup_edits, [])
        self.assertEqual(query.text_edits, [{
            "text": "Новый пост готов\n\nТекст\n\nИсточник: https://example.test\n\n❌ Уже было отклонено",
            "reply_markup": None,
        }])
        self.assertEqual(query.answers, [{"text": "Уже отклонено", "show_alert": False}])

    def test_idempotent_callback_does_not_append_a_second_final_status(self) -> None:
        query = FakeCallbackQuery("ur:approve:19", text="Новый пост готов\n\nТекст\n\n✅ Одобрено")
        action = ResultReviewAction("IDEMPOTENT")

        class CallbackQueryHandler:
            def __init__(self, callback_fn, pattern: str) -> None:
                self.callback_fn = callback_fn
                self.pattern = pattern

        telegram, telegram_ext = types.ModuleType("telegram"), types.ModuleType("telegram.ext")
        telegram_ext.CallbackQueryHandler = CallbackQueryHandler
        old_telegram, old_telegram_ext = sys.modules.get("telegram"), sys.modules.get("telegram.ext")
        sys.modules["telegram"], sys.modules["telegram.ext"] = telegram, telegram_ext
        try:
            captured = []
            contract.build_telegram_handler(action)(types.SimpleNamespace(add_handler=captured.append), FakeAdapter(True))
            asyncio.run(captured[0].callback_fn(types.SimpleNamespace(callback_query=query), object()))
        finally:
            if old_telegram is None:
                del sys.modules["telegram"]
            else:
                sys.modules["telegram"] = old_telegram
            if old_telegram_ext is None:
                del sys.modules["telegram.ext"]
            else:
                sys.modules["telegram.ext"] = old_telegram_ext

        self.assertEqual(query.text_edits, [{
            "text": "Новый пост готов\n\nТекст\n\n✅ Одобрено\n✅ Опубликовано\nVK post ID: 987",
            "reply_markup": None,
        }])
        self.assertEqual(query.reply_markup_edits, [])
        self.assertEqual(query.answers, [{"text": "Уже одобрено", "show_alert": False}])

    def test_callback_error_logs_and_returns_alert_without_keyboard_edit(self) -> None:
        query = FakeCallbackQuery("ur:approve:19")
        action = ResultReviewAction(error=contract.ReviewBridgeError("boom"))

        class CallbackQueryHandler:
            def __init__(self, callback_fn, pattern: str) -> None:
                self.callback_fn = callback_fn
                self.pattern = pattern

        telegram, telegram_ext = types.ModuleType("telegram"), types.ModuleType("telegram.ext")
        telegram_ext.CallbackQueryHandler = CallbackQueryHandler
        old_telegram, old_telegram_ext = sys.modules.get("telegram"), sys.modules.get("telegram.ext")
        sys.modules["telegram"], sys.modules["telegram.ext"] = telegram, telegram_ext
        try:
            captured = []
            contract.build_telegram_handler(action)(types.SimpleNamespace(add_handler=captured.append), FakeAdapter(True))
            with self.assertLogs(contract.logger, "ERROR"):
                asyncio.run(captured[0].callback_fn(types.SimpleNamespace(callback_query=query), object()))
        finally:
            if old_telegram is None:
                del sys.modules["telegram"]
            else:
                sys.modules["telegram"] = old_telegram
            if old_telegram_ext is None:
                del sys.modules["telegram.ext"]
            else:
                sys.modules["telegram.ext"] = old_telegram_ext

        self.assertEqual(query.reply_markup_edits, [])
        self.assertEqual(query.text_edits, [])
        self.assertEqual(query.answers, [{"text": "Не удалось сохранить решение. Повторите позже.", "show_alert": True}])


class CLIReviewActionTests(unittest.TestCase):
    def test_authorized_callback_reaches_bridge_after_authentication(self) -> None:
        calls = []

        def runner(argv, **kwargs):
            calls.append(argv)
            return types.SimpleNamespace(
                returncode=0,
                stdout=json.dumps({"result": "APPLIED", "draft_id": 19, "review_state": "APPROVED"}),
            )

        action = contract.CLIReviewAction("urban-radar", runner)
        handled = asyncio.run(contract.handle_callback_query(callback("ur:approve:19"), FakeAdapter(True), action))
        self.assertTrue(handled)
        self.assertEqual(calls, [["urban-radar", "content", "review", "approve", "19", "--actor", "telegram:42"]])

    def test_unauthorized_callback_never_starts_bridge_process(self) -> None:
        calls = []

        def runner(*args, **kwargs):
            calls.append((args, kwargs))
            raise AssertionError("bridge must not be called")

        action = contract.CLIReviewAction("urban-radar", runner)
        handled = asyncio.run(contract.handle_callback_query(callback("ur:approve:19"), FakeAdapter(False), action))
        self.assertFalse(handled)
        self.assertEqual(calls, [])

    def test_subprocess_receives_exact_action_draft_and_actor(self) -> None:
        calls = []

        def runner(argv, **kwargs):
            calls.append((argv, kwargs))
            return types.SimpleNamespace(
                returncode=0,
                stdout=json.dumps({"result": "APPLIED", "draft_id": 19, "review_state": "APPROVED"}),
            )

        result = contract.CLIReviewAction("/opt/urban-radar/bin/urban-radar", runner).approve(19, "telegram:42")
        self.assertEqual(result.result, "APPLIED")
        self.assertEqual(
            calls[0][0],
            ["/opt/urban-radar/bin/urban-radar", "content", "review", "approve", "19", "--actor", "telegram:42"],
        )
        self.assertTrue(calls[0][1]["capture_output"])
        self.assertTrue(calls[0][1]["check"] is False)

    def test_subprocess_failure_and_malformed_output_fail_closed(self) -> None:
        for result in (
            types.SimpleNamespace(returncode=1, stdout=""),
            types.SimpleNamespace(returncode=0, stdout="not-json"),
            types.SimpleNamespace(returncode=0, stdout=json.dumps({"result": "APPLIED", "draft_id": 20, "review_state": "APPROVED"})),
        ):
            with self.subTest(result=result):
                action = contract.CLIReviewAction("urban-radar", lambda *args, **kwargs: result)
                with self.assertRaises(contract.ReviewBridgeError):
                    action.approve(19, "telegram:42")

    def test_publish_uses_existing_cli_and_accepts_only_known_result(self) -> None:
        calls = []

        def runner(argv, **kwargs):
            calls.append((argv, kwargs))
            return types.SimpleNamespace(returncode=0, stdout=json.dumps({"result": "FAILED", "publication_id": 7}))

        value = contract.CLIReviewAction("urban-radar", runner).publish(19)
        self.assertEqual(value, {"result": "FAILED", "publication_id": 7})
        self.assertEqual(calls[0][0], ["urban-radar", "content", "publish", "19"])

    def test_retry_addresses_one_persisted_publication(self) -> None:
        calls = []

        def runner(argv, **kwargs):
            calls.append((argv, kwargs))
            return types.SimpleNamespace(returncode=0, stdout=json.dumps({"result": "FAILED", "publication_id": 7}))

        self.assertEqual(contract.CLIReviewAction("urban-radar", runner).retry(7)["publication_id"], 7)
        self.assertEqual(calls[0][0], ["urban-radar", "publication", "retry", "7"])

    def test_publication_result_text_and_controls_are_deterministic(self) -> None:
        self.assertEqual(
            contract._publication_text("Карточка", {"result": "BLOCKED", "reason": "PAYLOAD_MISMATCH"}),
            "Карточка\n\n✅ Одобрено\n⚠️ Публикация заблокирована: PAYLOAD_MISMATCH",
        )
        self.assertEqual(
            contract._publication_text("Карточка\n\n✅ Одобрено\n⚠️ Публикация не выполнена", {"result": "PUBLISHED", "external_post_id": "987"}),
            "Карточка\n\n✅ Одобрено\n✅ Опубликовано\nVK post ID: 987",
        )


class ReviewRuntimePreflightTests(unittest.TestCase):
    def test_requires_gateway_owned_absolute_executable_and_database_url(self) -> None:
        with self.assertRaises(contract.ReviewRuntimeConfigurationError):
            contract.preflight_review_runtime({})
        with self.assertRaises(contract.ReviewRuntimeConfigurationError):
            contract.preflight_review_runtime({"URBAN_RADAR_REVIEW_COMMAND": "urban-radar", "DATABASE_URL": "postgres://test"})
        with self.assertRaises(contract.ReviewRuntimeConfigurationError):
            contract.preflight_review_runtime({"URBAN_RADAR_REVIEW_COMMAND": sys.executable})
        self.assertEqual(
            contract.preflight_review_runtime({"URBAN_RADAR_REVIEW_COMMAND": sys.executable, "DATABASE_URL": "postgres://test"}),
            sys.executable,
        )


if __name__ == "__main__":
    unittest.main()
