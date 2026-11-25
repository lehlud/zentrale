"""Render the checked-in Zentrale PNG icons with librsvg (rsvg-convert)."""

from io import BytesIO
from pathlib import Path
from subprocess import run
from xml.etree import ElementTree as ET


def render(artwork: bytes, size: int, target: Path) -> None:
    run(
        ["rsvg-convert", "-w", str(size), "-h", str(size), "-o", str(target)],
        input=artwork,
        check=True,
    )


def main() -> None:
    icons = Path(__file__).resolve().parents[1] / "public" / "icons"
    artwork = (icons / "zentrale.svg").read_bytes()
    for name, size in (
        ("favicon-32.png", 32),
        ("pwa-192.png", 192),
        ("pwa-512.png", 512),
    ):
        render(artwork, size, icons / name)

    # Use a full-bleed tile and keep the mark within the maskable safe circle.
    namespace = "http://www.w3.org/2000/svg"
    ET.register_namespace("", namespace)
    maskable = ET.parse(BytesIO(artwork)).getroot()
    tile = maskable.find(".//svg:rect[@id='tile-shape']", {"svg": namespace})
    mark = maskable.find(".//svg:g[@id='mark']", {"svg": namespace})
    if tile is None or mark is None:
        raise ValueError("expected tile-shape and mark in zentrale.svg")

    tile.set("rx", "0")
    render(ET.tostring(maskable), 180, icons / "apple-touch-icon.png")

    mark.set("transform", "translate(23.04 23.04) scale(.82)")
    render(ET.tostring(maskable), 512, icons / "pwa-maskable-512.png")


if __name__ == "__main__":
    main()
