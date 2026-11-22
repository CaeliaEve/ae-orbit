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
        self.assertIn("当前储能：9.01 P EU",text)
        self.assertIn("电量：50%",text)
        self.assertEqual(len(text.splitlines()), 3)
        for unwanted in ["AE Orbit ·", "输入", "输出", "容量", "时间"]:
            self.assertNotIn(unwanted,text)
        energy["status"]="offline"
        text=bridge.notification_text({"id":"test", "payload":{"title":"蓝波顿电量","devices":[energy]}})
        self.assertIn("最后观测储能",text)
        self.assertNotIn("当前储能",text)
        self.assertIn("监控 OC 离线",text)
        self.assertIn("11-21 22:00:00",text)

    def test_compact_warning_uses_latest_energy_and_has_no_internal_footer(self):
        old={"status":"online","stored_eu":"180000000000","capacity_eu":"1000000000000"}
        latest={"status":"online","stored_eu":"186000000000","capacity_eu":"1000000000000"}
        delivery={"id":"64b2708f12345678","payload":{"title":"蓝波顿低电量告警","device":"蓝波顿超级电容库","alert_kind":"low_energy","energy":old,"current_energy":latest,"reason":"verbose diagnostic","at":"2026-11-22T00:00:00Z"}}
        self.assertEqual(bridge.notification_text(delivery),"⚠️ 蓝波顿电量预警\n当前储能：186 G EU\n电量：18.6%")
        self.assertNotIn("AE Orbit",bridge.notification_text({"id":"64b2708f","payload":{"title":"合成完成"}}))

    def test_short_energy_and_multiple_devices_remain_distinct(self):
        self.assertEqual(bridge.short_energy(999),"999 EU")
        self.assertEqual(bridge.short_energy(2*10**18+1),"2 E EU")
        power={"devices":[{"device":n,"status":"online","stored_eu":"1000","capacity_eu":"10000"} for n in ["主电容库","备用电容库"]]}
        text=bridge.notification_text({"id":"test","payload":power})
        self.assertIn("主电容库",text);self.assertIn("备用电容库",text)
        self.assertEqual(text.count("电量：10%"),2)


if __name__ == "__main__":
    unittest.main()
