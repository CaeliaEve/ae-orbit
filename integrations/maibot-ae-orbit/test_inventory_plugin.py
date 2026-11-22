"""Isolated SDK contract tests; HTTP is stubbed and no adapter is called."""
import asyncio
import importlib.util
from pathlib import Path
import sys
import types
import unittest

# The runtime SDK is tested on the MaiBot host. A minimal import surface also
# permits local tests without installing or importing any host internals.
try:
    import maibot_sdk
except ImportError:
    sdk = types.ModuleType("maibot_sdk")
    def decorator(*args, **kwargs):
        return lambda f: f
    for name in ["API", "Command", "HookHandler", "HomeCard"]:
        setattr(sdk, name, decorator)
    for name in ["MaiBotPlugin", "PluginConfigBase", "ConfigWidget", "ConfigWidgetState"]:
        setattr(sdk, name, type(name, (), {"__init__": lambda self, **kwargs: None}))
    sdk.Field = lambda **kwargs: None
    sys.modules["maibot_sdk"] = sdk
    types_sdk = types.ModuleType("maibot_sdk.types")
    types_sdk.HookMode = types.SimpleNamespace(BLOCKING="blocking")
    types_sdk.HookOrder = types.SimpleNamespace(EARLY="early")
    sys.modules["maibot_sdk.types"] = types_sdk

package = types.ModuleType("inventory_test_package")
package.__path__ = [str(Path(__file__).parent)]
sys.modules[package.__name__] = package
spec = importlib.util.spec_from_file_location(package.__name__ + ".plugin", Path(__file__).with_name("plugin.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class InventoryPluginTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self):
        self.plugin = object.__new__(module.AEOrbitPlugin)
        self.plugin._inventory_keywords = {"燃料", "fuel mk2"}
        self.plugin._policy = {"groups": ["1081348823"]}
        self.plugin._bot_id = "12345678"
        self.plugin._sending_enabled = True
        self.plugin._wake = asyncio.Event()
        self.calls = []
        async def http(path, body, method="POST"):
            self.calls.append((path, body, method))
            return {"queued": True}
        self.plugin._http = http
        self.message = {"platform": "qq", "scope": "group", "account_id": "12345678", "message_id": "msg-one",
                        "message_info": {"group_info": {"group_id": "1081348823"}, "user_info": {"user_id": "98765432"}},
                        "raw_message": [{"type": "text", "data": "燃料"}]}

    async def test_keyword_mentions_route_and_abort_only_after_queueing(self):
        for text in ["燃料", "/燃料", "库存 燃料", "/库存 燃料", " FUEL   MK2 ", "燃料还有多少？"]:
            self.message["raw_message"][0]["data"] = text
            result = await self.plugin.inventory_message(self.message)
            self.assertEqual(result["action"],"abort")
        self.assertEqual(len(self.calls), 6)
        self.assertEqual(len({call[1]["request_key"] for call in self.calls}), 1)
        self.message["message_id"] = "msg-two"
        await self.plugin.inventory_message(self.message)
        self.assertNotEqual(self.calls[0][1]["request_key"], self.calls[-1][1]["request_key"])

    async def test_other_chat_and_existing_commands_do_not_request_or_intercept(self):
        for text in ["/电量", "今天有人上线吗", "今天天气很好", "/help"]:
            self.message["raw_message"][0]["data"] = text
            self.assertEqual((await self.plugin.inventory_message(self.message))["action"],"continue")
        self.assertEqual(self.calls, [])

    async def test_scope_account_group_message_id_and_send_policy(self):
        for key, value in [("platform", "other"), ("account_id", "other"), ("message_id", ""), ("is_notify", True)]:
            original = self.message.copy()
            self.message[key] = value
            self.assertEqual((await self.plugin.inventory_message(self.message))["action"],"continue")
            self.message = original
        self.message["message_info"]["group_info"]["group_id"] = "99999999"
        self.assertEqual((await self.plugin.inventory_message(self.message))["action"],"continue")
        self.message["message_info"]["group_info"]["group_id"] = "1081348823"
        self.plugin._sending_enabled = False
        self.assertEqual((await self.plugin.inventory_message(self.message))["action"],"continue")
        self.assertEqual(self.calls, [])

    async def test_real_snowluma_scope_is_optional_and_not_chat_type(self):
        for scope in [None, "snowluma-connection"]:
            self.message["scope"] = scope
            self.assertEqual((await self.plugin.inventory_message(self.message))["action"], "abort")
        self.message["message_info"]["group_info"] = None
        self.assertEqual((await self.plugin.inventory_message(self.message))["action"], "continue")
        self.assertEqual(len(self.calls), 2)

    async def test_overlapping_keywords_are_one_request_and_bot_does_not_echo(self):
        self.plugin._inventory_keywords = {"燃料", "硅岩燃料"}
        self.message["raw_message"] = [{"type": "at", "data": "12345678"}, {"type": "text", "data": "/硅岩燃料还有多少"}]
        self.assertEqual((await self.plugin.inventory_message(self.message))["action"], "abort")
        self.assertEqual(len(self.calls), 1)
        self.assertEqual(self.calls[0][1]["keywords"], ["燃料", "硅岩燃料"])
        self.message["message_info"]["user_info"]["user_id"] = "12345678"
        self.assertEqual((await self.plugin.inventory_message(self.message))["action"], "continue")
        self.assertEqual(len(self.calls), 1)

    async def test_no_match_after_backend_configuration_change(self):
        async def http(*args):
            return {"queued": False}
        self.plugin._http = http
        self.assertEqual((await self.plugin.inventory_message(self.message))["action"],"continue")
        self.message["raw_message"]=[{"type": "image", "data": "燃料"}]
        self.assertEqual((await self.plugin.inventory_message(self.message))["action"],"continue")

if __name__ == "__main__":
    unittest.main()
