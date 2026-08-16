# Pixel-diffs two screenshots: prints the count of differing pixels and their
# bounding box, or "identical". Used by the verify skill's hand-over matrix.
#
#   uv run --with pillow dope/scripts/imgdiff.py before.png after.png
import sys

from PIL import Image, ImageChops


def main(a_path: str, b_path: str) -> int:
    a = Image.open(a_path).convert("RGB")
    b = Image.open(b_path).convert("RGB")
    if a.size != b.size:
        print(f"size differs: {a.size} vs {b.size}")
        return 1
    diff = ImageChops.difference(a, b)
    box = diff.getbbox()
    if box is None:
        print("identical")
        return 0
    changed = sum(1 for px in diff.convert("L").getdata() if px)
    print(f"{changed} px differ, bbox {box}")
    return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1], sys.argv[2]))
