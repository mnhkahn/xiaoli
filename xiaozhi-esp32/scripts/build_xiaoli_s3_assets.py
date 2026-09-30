"""Replace the S3's stock GIFs with Xiaoli face frames while keeping its speech model."""

import argparse
import json
from pathlib import Path
import struct


ENTRY = struct.Struct("<32sIIHH")
HEADER = struct.Struct("<III")


def unpack_assets(path: Path):
    blob = path.read_bytes()
    count, checksum, length = HEADER.unpack_from(blob)
    if length != len(blob) - HEADER.size or sum(blob[HEADER.size:]) & 0xFFFF != checksum:
        raise ValueError(f"Invalid source asset checksum: {path}")
    data_start = HEADER.size + count * ENTRY.size
    assets = {}
    for index in range(count):
        raw_name, size, offset, width, height = ENTRY.unpack_from(blob, HEADER.size + index * ENTRY.size)
        name = raw_name.split(b"\0", 1)[0].decode("utf-8")
        start = data_start + offset
        if not name or start + 2 + size > len(blob) or blob[start:start + 2] != b"ZZ":
            raise ValueError(f"Invalid source asset entry: {name}")
        assets[name] = (blob[start + 2:start + 2 + size], width, height)
    return assets


def pack_assets(assets, output: Path):
    data = bytearray()
    table = bytearray()
    for name, (content, width, height) in assets.items():
        encoded = name.encode("utf-8")
        if len(encoded) >= 32:
            raise ValueError(f"Asset name is too long: {name}")
        # GetAssetData skips ZZ; LVGL needs a four-byte-aligned pixel pointer.
        data.extend(b"\0" * ((2 - len(data)) % 4))
        table.extend(ENTRY.pack(encoded.ljust(32, b"\0"), len(content), len(data), width, height))
        data.extend(b"ZZ")
        data.extend(content)
    payload = table + data
    result = HEADER.pack(len(assets), sum(payload) & 0xFFFF, len(payload)) + payload
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_bytes(result)
    return len(result)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base", type=Path, required=True)
    parser.add_argument("--faces", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--max-size", type=lambda value: int(value, 0), required=True)
    args = parser.parse_args()

    original = unpack_assets(args.base)
    if "srmodels.bin" not in original or "index.json" not in original:
        raise ValueError("Source assets must include the speech model and index")
    assets = {name: item for name, item in original.items() if not name.endswith(".gif")}
    index = json.loads(assets["index.json"][0])
    index.pop("emoji_collection", None)
    index.setdefault("skin", {}).setdefault("dark", {})["background_color"] = "#041220"
    assets["index.json"] = (json.dumps(index, ensure_ascii=False).encode("utf-8"), 0, 0)
    faces = sorted(args.faces.glob("face_*.rgb565"))
    if len(faces) != 24:
        raise ValueError(f"Expected 24 face frames, found {len(faces)}")
    for face in faces:
        content = face.read_bytes()
        if len(content) != 168 * 200 * 2:
            raise ValueError(f"Unexpected face size: {face}")
        assets[face.name] = (content, 168, 200)
    size = pack_assets(assets, args.output)
    if size > args.max_size:
        args.output.unlink()
        raise ValueError(f"Assets exceed partition: {size} > {args.max_size}")
    print(f"Packed {len(assets)} assets ({len(faces)} faces): {size}/{args.max_size} bytes")


if __name__ == "__main__":
    main()
