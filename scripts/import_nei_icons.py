"""Import NEI's offline item-panel snapshot without guessing item identities.

The item-panel CSV and SNBT sequence must agree. Only unique, untagged
registry/meta pairs become base icons; this is not an NBT-aware icon resolver.
"""
import argparse
import collections
import csv
import hashlib
import json
import os
import pathlib
import re
import struct
import sys
import tempfile
import zlib


ROOT = pathlib.Path(__file__).resolve().parents[1]
HEADER = ["Item Name", "Item ID", "Item meta", "Has NBT", "Display Name"]
ILLEGAL = frozenset(chr(n) for n in (*range(32), 34, 60, 62, 124, 58, 42, 63, 92, 47))
# Legacy 1.7 registries include names such as "BiblioCraft:Armor Stand".
REGISTRY = re.compile(r"[^:\x00-\x1f]+:[^:\x00-\x1f]+\Z")
SHORT = re.compile(r"(-?\d+)[sS]\Z")
# These source exports visibly resemble ingots rather than the named motors.
# Keep the entire related family out until a game-to-export audit clears it.
QUARANTINED_MOTORS = frozenset((*range(32017, 32021), 32595, 32596, *range(32600, 32609)))


def icon_filenames(rows):
    used = set()
    names = []
    for row in rows:
        base = "".join("_" if char in ILLEGAL else char for char in row[4])
        name = base + ".png"
        number = 2
        while name.lower() in used:
            name = f"{base}_{number}.png"
            number += 1
        used.add(name.lower())
        names.append(name)
    return names


def compound_fields(text):
    """Read only top-level SNBT fields, leaving nested tag values opaque."""
    text = text.strip()
    if not text.startswith("{") or not text.endswith("}"):
        raise ValueError("invalid snapshot compound")
    fields = {}
    parts = []
    start = 1
    stack = []
    quote = None
    escaped = False
    for index in range(1, len(text) - 1):
        char = text[index]
        if quote:
            if escaped:
                escaped = False
            elif char == "\\":
                escaped = True
            elif char == quote:
                quote = None
        elif char in "\"'":
            quote = char
        elif char in "{[":
            stack.append(char)
        elif char in "}]":
            expected = "{" if char == "}" else "["
            if not stack or stack.pop() != expected:
                raise ValueError("unbalanced snapshot compound")
        elif char == "," and not stack:
            parts.append(text[start:index])
            start = index + 1
    if quote or stack:
        raise ValueError("unbalanced snapshot compound")
    parts.append(text[start:-1])
    for part in parts:
        key, separator, value = part.partition(":")
        key = key.strip().strip("\"'")
        if not separator or not key or key in fields:
            raise ValueError("invalid snapshot field")
        fields[key] = value.strip()
    return fields


def load_snapshot(source):
    csv_path = source / "itempanel.csv"
    snbt_path = source / "itempanel.json"
    with csv_path.open(encoding="utf-8-sig", newline="") as file:
        reader = csv.reader(file)
        if next(reader, None) != HEADER:
            raise ValueError("itempanel.csv must be the item-panel CSV, not item.csv")
        rows = list(reader)
    lines = snbt_path.read_text(encoding="utf-8-sig").splitlines()
    if len(rows) != len(lines) or not rows:
        raise ValueError("CSV and SNBT snapshot row counts differ or are empty")
    for number, (row, line) in enumerate(zip(rows, lines), start=1):
        if (len(row) != len(HEADER) or not REGISTRY.fullmatch(row[0])
                or not row[1].isdigit() or not re.fullmatch(r"-?\d+", row[2])
                or row[3] not in ("true", "false")):
            raise ValueError(f"invalid CSV snapshot row {number}")
        fields = compound_fields(line)
        numeric_id = SHORT.fullmatch(fields.get("id", ""))
        damage = SHORT.fullmatch(fields.get("Damage", ""))
        if (not numeric_id or not damage or int(numeric_id[1]) != int(row[1])
                or int(damage[1]) != int(row[2])
                or ("tag" in fields) != (row[3] == "true")):
            raise ValueError(f"CSV and SNBT snapshot identity differs at row {number}")
    registry_path = source / "item.csv"
    if registry_path.exists():
        with registry_path.open(encoding="utf-8-sig", newline="") as file:
            registry_rows = csv.DictReader(file)
            if not {"Name", "ID"}.issubset(registry_rows.fieldnames or []):
                raise ValueError("invalid item.csv registry snapshot")
            registry = {}
            for row in registry_rows:
                if row["ID"] in registry and registry[row["ID"]] != row["Name"]:
                    raise ValueError("ambiguous item.csv registry ID")
                registry[row["ID"]] = row["Name"]
        if any(registry.get(row[1]) != row[0] for row in rows):
            raise ValueError("item.csv registry does not agree with the item-panel snapshot")
    sources = {
        "itempanel_csv_sha256": hashlib.sha256(csv_path.read_bytes()).hexdigest(),
        "itempanel_snbt_sha256": hashlib.sha256(snbt_path.read_bytes()).hexdigest(),
    }
    if registry_path.exists():
        sources["item_registry_sha256"] = hashlib.sha256(registry_path.read_bytes()).hexdigest()
    return rows, sources


def png_visible(data):
    """Validate and unfilter the 8-bit RGB/RGBA PNGs emitted by Java ImageIO."""
    if len(data) > 2 * 1024 * 1024 or data[:8] != b"\x89PNG\r\n\x1a\n":
        raise ValueError("invalid PNG signature or size")
    position = 8
    header = None
    compressed = bytearray()
    ended = False
    while position < len(data):
        if position + 12 > len(data):
            raise ValueError("truncated PNG chunk")
        size = struct.unpack_from(">I", data, position)[0]
        end = position + size + 12
        if end > len(data):
            raise ValueError("truncated PNG chunk")
        kind = data[position + 4:position + 8]
        payload = data[position + 8:end - 4]
        checksum = struct.unpack_from(">I", data, end - 4)[0]
        if zlib.crc32(kind + payload) != checksum:
            raise ValueError("invalid PNG CRC")
        if header is None and kind != b"IHDR":
            raise ValueError("PNG header is not first")
        if kind == b"IHDR":
            if header is not None or size != 13:
                raise ValueError("invalid PNG header")
            header = struct.unpack(">IIBBBBB", payload)
        elif kind == b"IDAT":
            compressed.extend(payload)
        elif kind == b"IEND":
            if size != 0 or end != len(data):
                raise ValueError("invalid PNG ending")
            ended = True
            break
        elif kind[:1].isupper() and kind != b"PLTE":
            raise ValueError("unsupported critical PNG chunk")
        position = end
    if not ended or header is None or not compressed:
        raise ValueError("incomplete PNG")
    width, height, depth, color, compression, filtering, interlace = header
    if (not 1 <= width <= 256 or not 1 <= height <= 256 or depth != 8
            or color not in (2, 6) or compression or filtering or interlace):
        raise ValueError("unsupported PNG dimensions or format")
    channels = 4 if color == 6 else 3
    stride = width * channels
    expected = (stride + 1) * height
    decoder = zlib.decompressobj()
    try:
        raw = decoder.decompress(compressed, expected + 1)
    except zlib.error as error:
        raise ValueError("invalid PNG deflate stream") from error
    if (len(raw) != expected or not decoder.eof or decoder.unused_data
            or decoder.unconsumed_tail):
        raise ValueError("invalid PNG pixel length")
    previous = bytearray(stride)
    visible = color == 2
    offset = 0
    for _ in range(height):
        filter_type = raw[offset]
        row = bytearray(raw[offset + 1:offset + 1 + stride])
        offset += stride + 1
        if filter_type > 4:
            raise ValueError("invalid PNG scanline filter")
        if filter_type:
            for index in range(stride):
                left = row[index - channels] if index >= channels else 0
                up = previous[index]
                upper_left = previous[index - channels] if index >= channels else 0
                if filter_type == 1:
                    predictor = left
                elif filter_type == 2:
                    predictor = up
                elif filter_type == 3:
                    predictor = (left + up) // 2
                else:
                    value = left + up - upper_left
                    a, b, c = abs(value - left), abs(value - up), abs(value - upper_left)
                    predictor = left if a <= b and a <= c else up if b <= c else upper_left
                row[index] = (row[index] + predictor) & 255
        if color == 6 and any(row[3::4]):
            visible = True
        previous = row
    return visible, width, height


def write_atomic(path, content):
    with tempfile.NamedTemporaryFile(dir=path.parent, prefix=".nei-", delete=False) as file:
        temporary = pathlib.Path(file.name)
        try:
            file.write(content)
            file.flush()
        except BaseException:
            file.close()
            temporary.unlink(missing_ok=True)
            raise
    try:
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)


def import_icons(source, output):
    source, output = pathlib.Path(source).resolve(), pathlib.Path(output).resolve()
    if source == output or source in output.parents:
        raise ValueError("output must be outside the source dump directory")
    rows, source_hashes = load_snapshot(source)
    directory = source / "itempanel_icons"
    files = [path for path in directory.iterdir() if path.is_file() and path.suffix.lower() == ".png"]
    if len(files) != len(rows):
        raise ValueError("PNG and item-panel snapshot counts differ; preserve and verify the exports")
    paths = {}
    for path in files:
        folded = path.name.lower()
        if folded in paths:
            raise ValueError("ambiguous case-insensitive PNG filenames")
        paths[folded] = path
    names = icon_filenames(rows)
    counts = collections.Counter((row[0], int(row[2])) for row in rows)
    icons, assets, skipped = {}, {}, []
    dimensions = collections.Counter()
    checked = 0
    for number, (row, filename) in enumerate(zip(rows, names), start=1):
        registry, _, meta_text, tagged, _ = row
        meta = int(meta_text)
        reason = None
        if counts[registry, meta] != 1:
            reason = "ambiguous_visual_key"
        elif tagged == "true":
            reason = "nbt_variant"
        elif registry == "gregtech:gt.metaitem.01" and meta in QUARANTINED_MOTORS:
            reason = "quarantined_identity"
        elif filename.lower() not in paths:
            reason = "missing_image"
        if reason is None:
            path = paths[filename.lower()]
            data = path.read_bytes()
            checked += 1
            try:
                visible, width, height = png_visible(data)
            except ValueError:
                reason = "invalid_png"
            else:
                dimensions[f"{width}x{height}"] += 1
                if not visible:
                    reason = "transparent_image"
                else:
                    asset = hashlib.sha256(data).hexdigest() + ".png"
                    assets[asset] = data
                    icons[f"{registry}@{meta}"] = asset
        if reason:
            skipped.append({"row": number, "registry": registry, "meta": meta,
                            "source_file": filename, "reason": reason})
    expected_names = {name.lower() for name in names}
    unreferenced = sorted(path.name for key, path in paths.items() if key not in expected_names)
    stats = {
        "rows": len(rows), "mapped": len(icons), "skipped": len(skipped),
        "assets": len(assets), "bytes": sum(map(len, assets.values())),
        "png_checked": checked, "dimensions": dict(sorted(dimensions.items())),
        "skip_reasons": dict(sorted(collections.Counter(row["reason"] for row in skipped).items())),
    }
    manifest = {"version": 1, "icons": dict(sorted(icons.items())), "stats": stats}
    audit = {
        "version": 1, "source": source_hashes,
        "validation": "ordered_identity_and_decoded_png",
        "game_visual_audit_complete": False,
        "stats": stats, "skipped": skipped, "unreferenced_images": unreferenced,
    }
    output.mkdir(parents=True, exist_ok=True)
    for name, data in assets.items():
        path = output / name
        if not path.exists() or hashlib.sha256(path.read_bytes()).hexdigest() + ".png" != name:
            write_atomic(path, data)
    for name, value in (("audit.json", audit), ("manifest.json", manifest)):
        content = (json.dumps(value, ensure_ascii=True, sort_keys=True, separators=(",", ":")) + "\n").encode()
        write_atomic(output / name, content)
    return manifest, audit


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=pathlib.Path, help="NEI dumps directory (never modified)")
    parser.add_argument("--output", type=pathlib.Path, default=ROOT / "web/static/assets/nei")
    args = parser.parse_args()
    try:
        manifest, _ = import_icons(args.source, args.output)
    except (OSError, ValueError, csv.Error) as error:
        parser.exit(1, f"NEI import refused: {error}\n")
    print(json.dumps(manifest["stats"], sort_keys=True))


if __name__ == "__main__":
    main()
