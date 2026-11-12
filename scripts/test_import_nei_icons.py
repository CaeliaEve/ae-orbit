"""Focused checks for the offline NEI identity-to-icon importer."""
import csv
import importlib.util
import json
import pathlib
import struct
import tempfile
import unittest
import zlib


SCRIPT = pathlib.Path(__file__).with_name("import_nei_icons.py")
SPEC = importlib.util.spec_from_file_location("import_nei_icons", SCRIPT)
importer = importlib.util.module_from_spec(SPEC)
if SCRIPT.exists():
    SPEC.loader.exec_module(importer)

HEADER = ["Item Name", "Item ID", "Item meta", "Has NBT", "Display Name"]


def png(red=90, alpha=255):
    def chunk(kind, data):
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data))

    pixels = b"\0" + bytes((red, 70, 50, alpha)) * 2
    return (b"\x89PNG\r\n\x1a\n"
            + chunk(b"IHDR", struct.pack(">IIBBBBB", 2, 2, 8, 6, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(pixels * 2)) + chunk(b"IEND", b""))


class ImportIconsTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="ae-nei-")
        self.root = pathlib.Path(self.temp.name)
        self.source = self.root / "dumps"
        self.source.mkdir()
        (self.source / "itempanel_icons").mkdir()
        self.output = self.root / "output"

    def tearDown(self):
        self.temp.cleanup()

    def snapshot(self, rows, names=None):
        with (self.source / "itempanel.csv").open("w", encoding="utf-8", newline="") as file:
            writer = csv.writer(file)
            writer.writerow(HEADER)
            writer.writerows(rows)
        lines = []
        for _, numeric_id, meta, tagged, _ in rows:
            tag = 'tag:{text:"nested,id:999s,quote:\\"x\\""},' if tagged == "true" else ""
            lines.append(f"{{{tag}Damage:{meta}s,id:{numeric_id}s}}")
        (self.source / "itempanel.json").write_text("\n".join(lines) + "\n", encoding="utf-8")
        for index, name in enumerate(names or importer.icon_filenames(rows)):
            (self.source / "itempanel_icons" / name).write_bytes(png(index + 1))

    def run_import(self):
        return importer.import_icons(self.source, self.output)

    def test_csv_quotes_sanitization_and_collision_order(self):
        rows = [
            ["test:first", "1", "0", "false", 'A/B, "quoted"'],
            ["test:second", "2", "0", "false", 'A?B, "quoted"'],
            ["test:third", "3", "0", "false", 'A_B, _quoted__2'],
        ]
        self.snapshot(rows)
        manifest, audit = self.run_import()
        self.assertEqual(set(manifest["icons"]), {"test:first@0", "test:second@0", "test:third@0"})
        self.assertEqual(manifest["stats"]["mapped"], 3)
        self.assertEqual(audit["skipped"], [])
        self.assertEqual(importer.icon_filenames(rows)[:2],
                         ["A_B, _quoted_.png", "A_B, _quoted__2.png"])
        self.assertTrue(all((self.output / name).is_file() for name in manifest["icons"].values()))

    def test_tagged_and_ambiguous_visual_keys_are_not_guessed(self):
        rows = [
            ["test:tagged", "1", "0", "true", "Tagged"],
            ["test:variant", "2", "0", "false", "Plain"],
            ["test:variant", "2", "0", "true", "Variant"],
            ["test:normal", "3", "0", "false", "Normal"],
        ]
        self.snapshot(rows)
        manifest, audit = self.run_import()
        self.assertEqual(set(manifest["icons"]), {"test:normal@0"})
        reasons = [row["reason"] for row in audit["skipped"]]
        self.assertEqual(reasons.count("ambiguous_visual_key"), 2)
        self.assertEqual(reasons.count("nbt_variant"), 1)
        self.assertNotIn("nested", json.dumps(audit))

    def test_known_suspicious_motor_images_are_quarantined_by_identity(self):
        rows = [
            ["gregtech:gt.metaitem.01", "7615", "32600", "false", "Motor"],
            ["gregtech:gt.metaitem.01", "7615", "32601", "false", "Motor MV"],
            ["gregtech:gt.metaitem.01", "7615", "32603", "false", "Motor EV"],
            ["test:normal", "3", "0", "false", "Normal"],
        ]
        self.snapshot(rows)
        manifest, audit = self.run_import()
        self.assertEqual(set(manifest["icons"]), {"test:normal@0"})
        self.assertEqual([row["reason"] for row in audit["skipped"]],
                         ["quarantined_identity"] * 3)

    def test_missing_name_is_reported_without_guessing_other_png(self):
        rows = [["test:first", "1", "0", "false", "First"],
                ["test:second", "2", "0", "false", "Second"]]
        self.snapshot(rows, ["Changed.png", "Second.png"])
        manifest, audit = self.run_import()
        self.assertEqual(set(manifest["icons"]), {"test:second@0"})
        self.assertEqual(audit["skipped"][0]["reason"], "missing_image")
        self.assertEqual(audit["unreferenced_images"], ["Changed.png"])

    def test_snapshot_identity_mismatch_fails_before_output(self):
        self.snapshot([["test:first", "1", "0", "false", "First"]])
        (self.source / "itempanel.json").write_text("{Damage:1s,id:1s}\n", encoding="utf-8")
        with self.assertRaisesRegex(ValueError, "snapshot"):
            self.run_import()
        self.assertFalse(self.output.exists())

    def test_corrupt_and_fully_transparent_images_do_not_enter_manifest(self):
        rows = [["test:empty", "1", "0", "false", "Empty"],
                ["test:bad", "2", "0", "false", "Bad"]]
        self.snapshot(rows)
        (self.source / "itempanel_icons/Empty.png").write_bytes(png(alpha=0))
        (self.source / "itempanel_icons/Bad.png").write_bytes(png()[:-5])
        manifest, audit = self.run_import()
        self.assertEqual(manifest["icons"], {})
        self.assertEqual({row["reason"] for row in audit["skipped"]},
                         {"transparent_image", "invalid_png"})

    def test_identical_content_is_deduplicated_and_output_is_reproducible(self):
        rows = [["test:first", "1", "0", "false", "First"],
                ["test:second", "2", "0", "false", "Second"]]
        self.snapshot(rows)
        (self.source / "itempanel_icons/Second.png").write_bytes(
            (self.source / "itempanel_icons/First.png").read_bytes())
        first, _ = self.run_import()
        before = (self.output / "manifest.json").read_bytes()
        second, _ = self.run_import()
        self.assertEqual(first, second)
        self.assertEqual(before, (self.output / "manifest.json").read_bytes())
        self.assertEqual(len(list(self.output.glob("*.png"))), 1)
        self.assertEqual(first["stats"]["assets"], 1)

    def test_legacy_registered_names_preserve_spaces(self):
        self.snapshot([["BiblioCraft:Armor Stand", "1727", "0", "false", "Stand"]])
        manifest, _ = self.run_import()
        self.assertIn("BiblioCraft:Armor Stand@0", manifest["icons"])


if __name__ == "__main__":
    unittest.main()
