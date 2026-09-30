"""Build the C3's flash-mapped RGB565 face frames from the three artwork sheets."""

from pathlib import Path

from PIL import Image


ROOT = Path(__file__).resolve().parent
OUTPUT = ROOT.parent / "assets"
WIDTH, HEIGHT = 168, 200
SHEETS = (
    ("expressions-a.png", 3, ("neutral", "happy", "laughing", "funny", "surprised", "shocked", "thinking", "listening", "speaking")),
    ("expressions-b.png", 3, ("sad", "angry", "crying", "loving", "embarrassed", "winking", "cool", "relaxed", "delicious")),
    ("expressions-c.png", 2, ("kissy", "confident", "sleepy", "silly", "confused", "blink")),
)


def encode_rgb565(image: Image.Image) -> bytes:
    result = bytearray()
    for red, green, blue in image.convert("RGB").getdata():
        color = ((red & 0xF8) << 8) | ((green & 0xFC) << 3) | (blue >> 3)
        result.extend(color.to_bytes(2, "little"))
    return bytes(result)


def main() -> None:
    OUTPUT.mkdir(parents=True, exist_ok=True)
    for filename, rows, names in SHEETS:
        with Image.open(ROOT / filename) as sheet:
            cell_width = sheet.width // 3
            cell_height = sheet.height // rows
            for index, name in enumerate(names):
                column, row = index % 3, index // 3
                left, top = column * cell_width, row * cell_height
                if rows == 2:
                    crop = (left + 6, top + 100, left + cell_width - 6, top + 565)
                else:
                    crop = (left + 6, top + 6, left + cell_width - 6, top + 460)
                face = sheet.crop(crop).resize((WIDTH, HEIGHT), Image.Resampling.LANCZOS)
                (OUTPUT / f"face_{name}.rgb565").write_bytes(encode_rgb565(face))
    print(f"Built {sum(len(names) for _, _, names in SHEETS)} faces at {WIDTH}x{HEIGHT}")


if __name__ == "__main__":
    main()
