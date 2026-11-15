import json
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from import_nesql_catalog import build_manifest, ensure_output_outside_source, image_path, normalized_item


class NesqlCatalogTests(unittest.TestCase):
    def test_image_path_rejects_traversal_and_non_item_paths(self):
        self.assertEqual(image_path("item/minecraft/stone~0.png"), "item/minecraft/stone~0.png")
        self.assertEqual(image_path("../outside.png"), "")
        self.assertEqual(image_path("assets/minecraft/stone.png"), "")

    def test_item_preserves_nbt_and_sorted_tooltips(self):
        item = normalized_item(
            {
                "id": "minecraft:stone/0",
                "internal_name": "minecraft:stone",
                "item_damage": "0",
                "nbt": "{foo:1}",
                "image_file_path": "item/minecraft/stone~0.png",
            },
            {"minecraft:stone/0": ["line 2", "line 1"]},
        )
        self.assertEqual(item["nbt"], "{foo:1}")
        self.assertEqual(item["tooltips"], ["line 2", "line 1"])

    def test_manifest_deduplicates_exact_rows_but_keeps_conflicts(self):
        rows = [
            {"id": "1", "internal_name": "minecraft:stone", "item_damage": "0"},
            {"id": "2", "internal_name": "minecraft:stone", "item_damage": "0"},
        ]
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory)
            for name in ("image.zip", "nesql-db.script", "nesql-db.properties"):
                (source / name).write_bytes(b"fixture")
            manifest = build_manifest(rows, [], source)
            self.assertEqual(len(manifest["items"]), 1)
            self.assertEqual(manifest["source"]["duplicate_exact"], 1)

    def test_manifest_keeps_conflicting_rows_for_ambiguous_lookup(self):
        rows = [
            {"id": "1", "internal_name": "minecraft:stone", "item_damage": "0", "image_file_path": "item/minecraft/stone~0.png"},
            {"id": "2", "internal_name": "minecraft:stone", "item_damage": "0", "image_file_path": "item/minecraft/stone~0-alt.png"},
        ]
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory)
            for name in ("image.zip", "nesql-db.script", "nesql-db.properties"):
                (source / name).write_bytes(b"fixture")
            manifest = build_manifest(rows, [], source)
            self.assertEqual(len(manifest["items"]), 2)
            self.assertEqual(manifest["source"]["duplicate_conflict"], 1)

    def test_source_output_boundary_is_explicitly_checked(self):
        source = Path("C:/exports/nesql")
        with self.assertRaises(ValueError):
            ensure_output_outside_source(source, source / "catalog")
        ensure_output_outside_source(source, Path("C:/catalog/nesql"))


if __name__ == "__main__":
    unittest.main()
