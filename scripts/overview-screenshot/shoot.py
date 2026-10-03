"""Photograph the Overview of a QSP server running on this machine.

    python3 shoot.py http://127.0.0.1:18090/ ../../docs/images/overview.png

Not signed in, so the picture is what any visitor sees and carries no
addresses. Cropped above the Health panel: a demo server has most subsystems
switched off, and a table of "unavailable" says nothing about QSP.
"""
import io
import sys

from PIL import Image
from playwright.sync_api import sync_playwright

url, out = sys.argv[1], sys.argv[2]
with sync_playwright() as p:
    browser = p.chromium.launch()
    page = browser.new_page(viewport={"width": 1440, "height": 900})
    errors = []
    page.on("pageerror", lambda e: errors.append(str(e)))
    # The peer table, the calls and the map tiles arrive after the page does.
    with page.expect_response(lambda r: "/api/peers" in r.url):
        page.goto(url, wait_until="load")
    page.wait_for_timeout(5000)
    bottom = page.locator("h2", has_text="Health").bounding_box()["y"] - 24
    shot = page.screenshot(full_page=True, clip={"x": 0, "y": 0, "width": 1440, "height": bottom})
    browser.close()

if errors:
    sys.exit("the page reported errors: " + "; ".join(errors))

# 256 colours is plenty for a flat interface and a third of the size.
Image.open(io.BytesIO(shot)).convert("RGB").quantize(colors=256, method=Image.Quantize.MEDIANCUT, dither=Image.Dither.NONE).save(out, optimize=True)
print("wrote", out)
