from pathlib import Path

import importlib.util
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("bridge", Path(__file__).with_name("bridge.py"))
bridge = importlib.util.module_from_spec(spec)
spec.loader.exec_module(bridge)


class BridgeTests(unittest.TestCase):
    def test_channel_policy_persists_and_rejects_invalid_targets(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / "channels.json"
            policy = bridge.ChannelPolicy(path)
            self.assertEqual(policy.read()["groups"], [])
            policy.save({"groups": ["1081348823"], "allow_private": True})
            self.assertEqual(bridge.ChannelPolicy(path).read()["groups"], ["1081348823"])
            policy.save({"groups": []})
            self.assertEqual(policy.read()["groups"], [])
            for bad in [{"groups": ["all"]}, {"groups": ["1081348823"], "allow_private": "yes"}]:
                with self.assertRaises(ValueError):
                    policy.save(bad)
    def test_crash_journal_never_replays_send(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / "journal.sqlite"
            delivery = {"id": "event-1", "attempt_id": "attempt-1"}
            bridge.ReceiptJournal(path).started(delivery)
            recovered = bridge.ReceiptJournal(path)
            self.assertEqual(recovered.pending()[0]["receipt"]["state"], "uncertain")
            receipt = {"attempt_id": "attempt-1", "state": "confirmed", "external_id": "123"}
            recovered.completed("event-1", receipt)
            self.assertEqual(bridge.ReceiptJournal(path).pending()[0]["receipt"], receipt)
            recovered.acknowledged("event-1")
            self.assertEqual(recovered.pending(), [])

    def test_mention_uses_structured_segment_and_keeps_quantity(self):
        delivery = {"id": "abcd1234", "channel": "group", "recipient": "12345678",
                    "payload": {"title": "合成完成", "item": "流体", "quantity": "9007199254740993", "unit": "mB"}}
        segments = bridge.message_segments(delivery, True)
        self.assertEqual(segments[0], {"type": "at", "data": {"qq": "12345678"}})
        self.assertIn("9007199254740993", segments[-1]["data"]["text"])
        delivery["channel"] = "private"
        self.assertEqual(bridge.message_segments(delivery, True)[0]["type"], "text")

    def test_power_formats_exact_eu_and_stale_observation(self):
        energy={"status":"online","stored_eu":"9007199254740993","capacity_eu":"18014398509481986","percent":"50.00","observed_at":"2026-11-21T14:00:00Z","flow":{"input_eut":"1000","output_eut":"2000","loss_eut":"10"}}
        text=bridge.notification_text({"id":"test", "payload":{"title":"蓝波顿告警","energy":energy}})
        self.assertIn("9,007,199,254,740,993",text)
        self.assertIn("电量：50.00%",text)
        self.assertIn("输入：1,000 EU/t",text)
        self.assertIn("11-21 22:00:00",text)
        energy["status"]="offline"
        text=bridge.notification_text({"id":"test", "payload":{"title":"蓝波顿电量","devices":[energy]}})
        self.assertIn("最后观测储能",text)
        self.assertNotIn("当前储能",text)
        self.assertIn("当前电量未确认",text)


if __name__ == "__main__":
    unittest.main()
