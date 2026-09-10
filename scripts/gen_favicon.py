#!/usr/bin/env python3
"""Generate favicon.ico + apple-touch-icon.png for the
MadHatter project (#61). Both assets are baked from the same
🎩 top-hat emoji, sourced from the system Apple Color Emoji
font. The PNGs are committed to internal/web/assets/icons/
and embedded via go:embed so the staticHandler serves them
under /static/.

Run from the repo root:

    python3 scripts/gen_favicon.py

The script is idempotent — overwriting an existing asset
produces the same bytes (PIL re-encodes with deterministic
settings).

Why emoji-based and not designed: the project is called
MadHatter (Alice-in-Wonderland themed), the birthday banner
already uses emoji (🎂, 💗) baked via canvas-confetti's
shapeFromText, and a 32x32 PNG of 🎩 is recognizably "hat"
even at small sizes. Designing a custom vector icon
would require a design tool + asset pipeline; emoji
generation is one Python file.
"""

from __future__ import annotations

import sys
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

# The emoji we render. 🎩 = top hat = the project mascot.
EMOJI = "🎩"

# Source font. Apple Color Emoji ships on every macOS
# install and is the canonical emoji rasterizer on Apple
# platforms. Linux deploys with a Noto Color Emoji
# fallback should still render the same glyph — the codepoint
# 🎩 (U+1F3A9) is in the base Unicode emoji block.
APPLE_EMOJI_FONT = "/System/Library/Fonts/Apple Color Emoji.ttc"

# Targets: standard favicon.ico carries 16+32+48 (the three
# sizes every browser + bookmark UI needs), apple-touch-icon
# is a single 180x180 PNG.
FAVICON_SIZES = (16, 32, 48)
APPLE_TOUCH_SIZE = 180

# Output paths. The favicons live at the top of the embedded
# assets/ tree so the URL /static/favicon.ico maps cleanly
# after the http.StripPrefix("/static/", ...) in staticHandler.
# Putting them under /icons/ would require /static/icons/...
# URLs, which is unconventional.
ASSETS_DIR = Path("internal/web/assets")
FAVICON_ICO = ASSETS_DIR / "favicon.ico"
APPLE_TOUCH_PNG = ASSETS_DIR / "apple-touch-icon.png"


# Apple Color Emoji only ships bitmap glyphs at a fixed set of
# pixel sizes; PIL raises "invalid pixel size" for any other
# size. The exact set depends on the OS version — on this macOS
# (Sonoma-era), the supported sizes are {20, 26, 32, 40, 48, 52,
# 64, 96, 160}. The script picks the nearest supported size
# via nearest_emoji_size and lets PIL's high-quality resampler
# scale the bitmap to the target. To find the supported sizes
# on your platform, run:
#   python3 -c "from PIL import ImageFont; \
#     [print(sz) for sz in range(8,256) if ImageFont.truetype('/System/Library/Fonts/Apple Color Emoji.ttc', size=sz) is not None]"
APPLE_EMOJI_BITMAP_SIZES = (20, 26, 32, 40, 48, 52, 64, 96, 160)


def nearest_emoji_size(target: int) -> int:
    """Pick the supported Apple Color Emoji bitmap size closest
    to target. Larger is preferred over smaller when the
    distance is even, because downscaling preserves more detail
    than upscaling."""
    return min(APPLE_EMOJI_BITMAP_SIZES, key=lambda s: (abs(s - target), s - target >= 0))


def render_emoji(size: int) -> Image.Image:
    """Rasterize EMOJI into a square RGBA PNG of the given size.

    Renders at the nearest supported bitmap size and rescales
    via Lanczos so 16x16 favicons and 180x180 apple-touch-icons
    both look correct — the source bitmap is the highest-
    quality master, then PIL downscales (or upscales) to the
    target."""
    native = nearest_emoji_size(size)
    canvas = Image.new("RGBA", (native, native), (0, 0, 0, 0))
    draw = ImageDraw.Draw(canvas)

    # Render at 2x the bitmap size (capped at the largest
    # supported bitmap, 128px) so PIL has more pixels to
    # downscale from; produces a sharper 16x16 favicon than
    # rendering directly at 16x16.
    render_px = min(native * 2, APPLE_EMOJI_BITMAP_SIZES[-1])
    if render_px not in APPLE_EMOJI_BITMAP_SIZES:
        render_px = nearest_emoji_size(render_px)
    font = ImageFont.truetype(APPLE_EMOJI_FONT, size=render_px)

    if hasattr(font, "getbbox"):
        bbox = draw.textbbox((0, 0), EMOJI, font=font)
        text_w = bbox[2] - bbox[0]
        text_h = bbox[3] - bbox[1]
        offset_x = bbox[0]
        offset_y = bbox[1]
    else:
        text_w, text_h = font.getsize(EMOJI)
        offset_x, offset_y = 0, 0

    x = (native - text_w) // 2 - offset_x
    y = (native - text_h) // 2 - offset_y
    draw.text((x, y), EMOJI, font=font, embedded_color=True)

    # Now scale down to the target size. LANCZOS preserves
    # edges better than the default bicubic for tiny targets.
    return canvas.resize((size, size), Image.Resampling.LANCZOS)


def write_favicon_ico(out: Path, sizes: tuple[int, ...]) -> None:
    """Write a multi-resolution .ico file. Pillow's ICO writer
    drops everything except the first image when called with
    append_images, so we hand-roll the ICO format. It's a tiny
    header + a directory entry per image + the image data
    itself (PNG-encoded for each size)."""
    import io
    import struct

    # Render each size once; we'll encode each as PNG and
    # concatenate the bytes behind a directory.
    rendered = []
    for s in sizes:
        img = render_emoji(s)
        buf = io.BytesIO()
        img.save(buf, format="PNG", optimize=True)
        rendered.append((s, buf.getvalue()))

    # ICO header (6 bytes):
    #   uint16le reserved (0)
    #   uint16le type (1 = ICO)
    #   uint16le count
    header = struct.pack("<HHH", 0, 1, len(rendered))

    # Each directory entry (16 bytes):
    #   uint8  width (0 = 256)
    #   uint8  height (0 = 256)
    #   uint8  color_count (0 = >=256)
    #   uint8  reserved (0)
    #   uint16le planes
    #   uint16le bpp
    #   uint32le image_size
    #   uint32le offset
    #
    # The offset is the byte offset of the PNG data within the
    # final .ico file. Compute it as: header + directory + sum
    # of preceding image bytes.
    header_and_dir_size = 6 + 16 * len(rendered)
    offsets: list[int] = []
    cursor = header_and_dir_size
    for _, png_bytes in rendered:
        offsets.append(cursor)
        cursor += len(png_bytes)

    directory = b""
    for (s, png_bytes), off in zip(rendered, offsets):
        # 0 means 256 in the ICO format. Our sizes are all
        # < 256 so the literal value fits.
        width = 0 if s >= 256 else s
        height = 0 if s >= 256 else s
        # bpp: 32 for RGBA. The directory entry's bpp is
        # informational; PNG-encoded images ignore it.
        entry = struct.pack(
            "<BBBBHHII",
            width, height,
            0,  # color_count
            0,  # reserved
            1,  # planes
            32,  # bpp
            len(png_bytes),
            off,
        )
        directory += entry

    with open(out, "wb") as f:
        f.write(header)
        f.write(directory)
        for _, png_bytes in rendered:
            f.write(png_bytes)


def write_apple_touch(out: Path, size: int) -> None:
    """Write a single-resolution 180x180 PNG. Apple rounds the
    corners itself, so the source PNG should fill the entire
    180x180 canvas (no transparent corners)."""
    render_emoji(size).save(out, format="PNG", optimize=True)


def main() -> int:
    if not Path(APPLE_EMOJI_FONT).exists():
        print(f"fatal: {APPLE_EMOJI_FONT} not found; run on macOS or swap the font path",
              file=sys.stderr)
        return 1

    ASSETS_DIR.mkdir(parents=True, exist_ok=True)

    write_favicon_ico(FAVICON_ICO, FAVICON_SIZES)
    print(f"wrote {FAVICON_ICO} ({FAVICON_SIZES} resolutions)")

    write_apple_touch(APPLE_TOUCH_PNG, APPLE_TOUCH_SIZE)
    print(f"wrote {APPLE_TOUCH_PNG} ({APPLE_TOUCH_SIZE}x{APPLE_TOUCH_SIZE})")

    return 0


if __name__ == "__main__":
    sys.exit(main())
