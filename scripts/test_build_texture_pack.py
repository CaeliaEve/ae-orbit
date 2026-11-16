import json
import sys
import tempfile
import unittest
import zipfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from build_texture_pack import asset_paths, build_manifest, ensure_output_outside_source, write_pack


class TexturePackTests(unittest.TestCase):
    def test_only_registered_png_assets_are_included(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "assets").mkdir()
            (root / "assets/stone.png").write_bytes(b"png")
            (root / "secret.txt").write_text("ignore", encoding="utf-8")
            manifest = {
                "format": "elysium.source",
                "revision": 41,
                "files": [
                    {"kind": "asset", "path": "assets/stone.png"},
                    {"kind": "asset", "path": "../secret.txt"},
                    {"kind": "environment", "path": "environment.json"},
                ],
            }
            self.assertEqual(asset_paths(manifest, root), ["assets/stone.png"])
            self.assertEqual(build_manifest(manifest, ["assets/stone.png"])["format"], "ae-orbit.texture-pack")

    def test_pack_is_readable_and_output_cannot_be_inside_source(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "assets").mkdir()
            (root / "assets/stone.png").write_bytes(b"png")
            (root / "manifest.json").write_text(json.dumps({
                "format": "elysium.source",
                "revision": 41,
                "files": [{"kind": "asset", "path": "assets/stone.png"}],
            }), encoding="utf-8")
            with self.assertRaises(ValueError):
                ensure_output_outside_source(root, root / "pack.zip")
            output = root.parent / "pack.zip"
            count, digest = write_pack(root, output)
            self.assertEqual(count, 1)
            self.assertEqual(len(digest), 64)
            with zipfile.ZipFile(output) as archive:
                self.assertEqual(sorted(archive.namelist()), ["assets/stone.png", "manifest.json"])


if __name__ == "__main__":
    unittest.main()
