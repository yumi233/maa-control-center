import importlib.util
import json
import os
from pathlib import Path
import sys
import tempfile
import types
import unittest
from unittest.mock import AsyncMock, patch

# The command transport is tested independently of the AstrBot daemon. Runtime
# loading is verified separately against the installed AstrBot package.
for name in ["astrbot", "astrbot.api", "astrbot.api.event", "astrbot.api.star"]:
    sys.modules[name] = types.ModuleType(name)
sys.modules["astrbot.api.event"].filter = types.SimpleNamespace(command=lambda *_: lambda f: f)
sys.modules["astrbot.api.event"].AstrMessageEvent = object


class FakeStar:
    def __init__(self, context):
        self.context = context


sys.modules["astrbot.api.star"].Context = object
sys.modules["astrbot.api.star"].Star = FakeStar
spec = importlib.util.spec_from_file_location("maa_plugin", Path(__file__).with_name("main.py"))
plugin = importlib.util.module_from_spec(spec)
spec.loader.exec_module(plugin)


class Event:
    unified_msg_origin = "fixture:GroupMessage:group"
    message_obj = types.SimpleNamespace(message_id="fixture-message")
    stopped = False

    def stop_event(self):
        self.stopped = True

    def get_sender_id(self):
        return "fixture-uid"

    def plain_result(self, text):
        return text


class PluginTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.config = {"base_url": "https://fixture.invalid", "routes": [
            {"umo": Event.unified_msg_origin, "account_id": "secondary", "label": "fixture account", "token": "f" * 64}]}
        path = Path(self.temp.name) / "connection.json"
        path.write_text(json.dumps(self.config))
        self.env = patch.dict(os.environ, {"MAACC_BOT_CONFIG": str(path)})
        self.env.start()
        self.addCleanup(self.env.stop)

    async def test_closed_command_and_platform_identity(self):
        event = Event()
        with patch.object(plugin, "request_command", AsyncMock(return_value="queued")) as request:
            reply = [x async for x in plugin.MAAControl(None).maa(event, "开始")]
        self.assertEqual(reply, ["queued"])
        self.assertTrue(event.stopped)
        sent = request.call_args.args[1]
        self.assertEqual(sent["command"], "start")
        self.assertEqual(sent["sender_id"], "fixture-uid")
        self.assertEqual(sent["message_id"], "fixture-message")
        self.assertEqual(sent["umo"], Event.unified_msg_origin)

    async def test_unknown_group_and_command_do_not_call_backend(self):
        for action, umo in [("改源石预算", Event.unified_msg_origin), ("开始", "fixture:GroupMessage:other")]:
            event = Event()
            event.unified_msg_origin = umo
            with patch.object(plugin, "request_command", AsyncMock()) as request:
                [x async for x in plugin.MAAControl(None).maa(event, action)]
                request.assert_not_called()

    async def test_missing_message_identity_never_starts(self):
        event = Event()
        event.message_obj = types.SimpleNamespace(message_id="")
        with patch.object(plugin, "request_command", AsyncMock()) as request:
            replies = [x async for x in plugin.MAAControl(None).maa(event, "开始")]
            request.assert_not_called()
        self.assertIn("消息 ID", replies[0])

    async def test_timeout_is_not_retried(self):
        with patch.object(plugin, "request_command", AsyncMock(side_effect=TimeoutError)) as request:
            replies = [x async for x in plugin.MAAControl(None).maa(Event(), "停止")]
            self.assertEqual(request.call_count, 1)
            self.assertIn("未确认", replies[0])

    def test_multiple_accounts_require_explicit_target(self):
        self.config["routes"].append({"umo": Event.unified_msg_origin, "account_id": "primary", "label": "other", "token": "e" * 64})
        with self.assertRaises(ValueError):
            plugin.select_route(self.config, Event.unified_msg_origin, "start")
        route, target, _ = plugin.select_route(self.config, Event.unified_msg_origin, "start", "other")
        self.assertEqual(route["account_id"], "primary")
        self.assertEqual(target, "primary")

    def test_report_id_remains_scoped_to_route(self):
        route, target, job = plugin.select_route(self.config, Event.unified_msg_origin, "report", "a" * 64)
        self.assertEqual(route["account_id"], "secondary")
        self.assertEqual(target, "")
        self.assertEqual(job, "a" * 64)


if __name__ == "__main__":
    unittest.main()
