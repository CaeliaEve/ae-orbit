#!/usr/bin/env python3
"""Build a derived NESQL++ catalog with exact, untagged fallback records.

The primary export owns every identity it contains. Only absent, no-NBT
identities from the fallback export are copied, together with their own text
and image references. Both source directories remain unchanged.
"""
import argparse
import gzip
import hashlib
import json
import shutil
import zipfile
from pathlib import Path, PurePosixPath


def encoded(value):
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode("utf-8")


def digest(data):
    return hashlib.sha256(data).hexdigest()


def safe_path(value):
    if not isinstance(value, str) or "\\" in value or ":" in value or "\x00" in value:
        raise ValueError("unsafe catalog path")
    path = PurePosixPath(value)
    if path.is_absolute() or any(part in ("", ".", "..") for part in value.split("/")):
        raise ValueError("unsafe catalog path")
    return path


def read_export(root, textures=None):
    manifest = json.loads((root / "manifest.json").read_text(encoding="utf-8"))
    if manifest.get("format") != "elysium.source" or manifest.get("revision", 0) < 41:
        raise ValueError("expected NESQL++ revision 41 or later")
    if manifest.get("state", "complete") not in ("", "complete"):
        raise ValueError("incomplete export")
    if not {"items", "assets", "strings"}.issubset(manifest.get("scope", {}).get("collections", [])):
        raise ValueError("missing required collections")
    records = {kind: {} for kind in ("items", "assets", "strings")}
    files = {}
    for entry in manifest["files"]:
        path = str(safe_path(entry["path"]))
        if path in files:
            raise ValueError("duplicate manifest file")
        files[path] = entry
        data = textures.read(path) if textures and entry['kind'] == 'asset' else (root / path).read_bytes()
        if len(data) != entry["bytes"] or digest(data) != entry["sha256"]:
            raise ValueError("source checksum mismatch: " + path)
        if entry.get("encoding") != "jsonl.gzip":
            continue
        decoded = gzip.decompress(data)
        rows = [json.loads(line) for line in decoded.splitlines() if line.strip()]
        if len(decoded) != entry["decodedBytes"] or len(rows) != entry["rows"]:
            raise ValueError("source record count mismatch: " + path)
        kind = entry["kind"]
        if kind not in records:
            continue
        for row in rows:
            key = (row["id"], row["locale"]) if kind == "strings" else row["id"]
            if key in records[kind] and records[kind][key] != row:
                raise ValueError("conflicting source record: " + str(key))
            records[kind][key] = row
    return manifest, records, files


def write_records(target, kind, values):
    path = f"records/{kind}/ae-orbit-compat.jsonl.gz"
    decoded = b"".join(encoded(row) + b"\n" for row in values)
    data = gzip.compress(decoded, compresslevel=6, mtime=0)
    destination = target / path
    if destination.exists():
        raise ValueError("compatibility file already exists")
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_bytes(data)
    return {"path": path, "kind": kind, "encoding": "jsonl.gzip", "bytes": len(data),
            "decodedBytes": len(decoded), "rows": len(values), "sha256": digest(data)}


def merge_catalog(primary, fallback, output, fallback_textures=None):
    primary, fallback, output = (Path(path).resolve() for path in (primary, fallback, output))
    for source in (primary, fallback):
        if output == source or source in output.parents or output in source.parents:
            raise ValueError("output must be separate from both immutable sources")
    if output.exists():
        raise ValueError("output already exists")
    main_manifest, main, main_files = read_export(primary)
    archive = zipfile.ZipFile(fallback_textures) if fallback_textures else None
    try:
        old_manifest, old, old_files = read_export(fallback, archive)
    finally:
        if archive:
            archive.close()
    main_plain = {(row["registry"], row["meta"]) for row in main["items"].values() if row.get("nbt") is None}
    selected = []
    seen = set(main_plain)
    for row in sorted(old["items"].values(), key=lambda row: row["id"]):
        identity = (row["registry"], row["meta"])
        if row.get("nbt") is not None or identity in seen:
            continue
        if row["id"] in main["items"]:
            raise ValueError("fallback ID conflicts with primary identity")
        selected.append(row)
        seen.add(identity)
    asset_ids = {row["icon"] for row in selected}
    text_ids = {value for row in selected for value in [row.get("name"), *row.get("tooltip", [])] if value}
    appended_assets = []
    appended_strings = []
    extra_files = {}
    for asset_id in sorted(asset_ids):
        asset = old["assets"].get(asset_id)
        if not asset:
            raise ValueError("fallback asset reference missing")
        if asset_id in main["assets"]:
            if main["assets"][asset_id] != asset:
                raise ValueError("conflicting fallback asset")
            continue
        path = str(safe_path(asset["path"]))
        if path not in old_files or old_files[path]["kind"] != "asset":
            raise ValueError("fallback image missing from manifest")
        if path in main_files:
            if main_files[path]["sha256"] != old_files[path]["sha256"]:
                raise ValueError("conflicting fallback image")
        else:
            extra_files[path] = old_files[path]
        appended_assets.append(asset)
    for text_id in sorted(text_ids):
        values = [row for (key, _), row in old["strings"].items() if key == text_id]
        if not values:
            raise ValueError("fallback text reference missing")
        for row in values:
            key = (row["id"], row["locale"])
            if key in main["strings"]:
                if main["strings"][key] != row:
                    raise ValueError("conflicting fallback text")
            else:
                appended_strings.append(row)
    output.mkdir(parents=True)
    # Copy only declared data, avoiding connection metadata or exporter work files.
    for path in main_files:
        destination = output / path
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(primary / path, destination)
    for path in extra_files:
        destination = output / path
        destination.parent.mkdir(parents=True, exist_ok=True)
        if fallback_textures:
            with zipfile.ZipFile(fallback_textures) as textures:
                destination.write_bytes(textures.read(path))
        else:
            shutil.copyfile(fallback / path, destination)
    files = list(main_manifest["files"]) + [extra_files[path] for path in sorted(extra_files)]
    for kind, values in (("items", selected), ("assets", appended_assets), ("strings", appended_strings)):
        if values:
            files.append(write_records(output, kind, values))
    provenance = {"primary": main_manifest["id"], "fallback": old_manifest["id"],
                  "policy": "primary-exact-with-absent-untagged-fallback-v1", "fallback_item_ids": [row["id"] for row in selected]}
    manifest = {**main_manifest, "id": digest(encoded(provenance)), "files": files,
                "producer": {"name": "ae-orbit-derived-catalog", "version": "1"}, "composition": provenance}
    (output / "manifest.json").write_bytes(encoded(manifest) + b"\n")
    report = {"id": manifest["id"], "primary": main_manifest["id"], "fallback": old_manifest["id"],
              "primary_plain": len(main_plain), "compatibility_plain": len(selected), "total_plain": len(seen),
              "items": len(main["items"]) + len(selected), "assets": len(main["assets"]) + len(appended_assets),
              "png_files": len([entry for entry in files if entry["kind"] == "asset"]),
              "compatibility": [{"registry": row["registry"], "meta": row["meta"], "id": row["id"]} for row in selected]}
    (output.parent / (output.name + "-report.json")).write_bytes(encoded(report) + b"\n")
    return report


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("primary", type=Path)
    parser.add_argument("fallback", type=Path)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--fallback-textures", type=Path, help="Verified PNG ZIP when fallback metadata was recovered separately")
    args = parser.parse_args()
    print(json.dumps(merge_catalog(args.primary, args.fallback, args.output, args.fallback_textures), ensure_ascii=False))
