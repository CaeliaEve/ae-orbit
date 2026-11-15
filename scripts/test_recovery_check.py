import importlib.util
import pathlib
import unittest


SCRIPT = pathlib.Path(__file__).with_name("recovery_check.py")
SPEC = importlib.util.spec_from_file_location("recovery_check", SCRIPT)
module = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(module)


class RecoveryCheckTest(unittest.TestCase):
    def test_snapshot_reports_backend_and_each_oc_separately(self):
        result = module.summarize_snapshot(
            {"online": True, "last_seen": "2026-11-13T00:00:00Z"},
            [
                {"name": "主设备", "role": "primary", "online": True, "last_seen": "2026-11-13T00:00:00Z"},
                {"name": "数量助手", "role": "quantity_worker", "online": False, "last_seen": "2026-11-12T23:50:00Z"},
            ],
            expected_devices=2,
        )
        self.assertEqual(result["network"], "online")
        self.assertEqual(result["devices"], {"total": 2, "online": 1, "offline": 1})
        self.assertFalse(result["ready"])
        self.assertEqual(result["reason"], "oc_offline")

    def test_snapshot_waits_for_game_heartbeat_when_no_device_has_reported(self):
        result = module.summarize_snapshot(
            {"online": False, "last_seen": None},
            [{"name": "主设备", "role": "primary", "online": False, "last_seen": None}],
            expected_devices=1,
        )
        self.assertEqual(result["reason"], "heartbeat_waiting")
        self.assertFalse(result["ready"])

    def test_snapshot_requires_all_expected_devices(self):
        result = module.summarize_snapshot(
            {"online": True},
            [{"name": "主设备", "role": "primary", "online": True}],
            expected_devices=2,
        )
        self.assertEqual(result["reason"], "device_count")
        self.assertFalse(result["ready"])

    def test_origin_is_normalized_without_credentials_or_path(self):
        self.assertEqual(module.normalize_origin("https://ae.example.com/"), "https://ae.example.com")
        with self.assertRaises(ValueError):
            module.normalize_origin("https://user:pass@ae.example.com")
        with self.assertRaises(ValueError):
            module.normalize_origin("https://ae.example.com/app")


if __name__ == "__main__":
    unittest.main()
