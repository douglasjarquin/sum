#!/usr/bin/env python3
"""Build: render src/*.in into dist/ with a build stamp. The service serves dist/ only."""
import pathlib, time
root = pathlib.Path(__file__).resolve().parent
dist = root / "dist"; dist.mkdir(exist_ok=True)
for source in (root / "src").glob("*.in"):
    (dist / source.name[:-3]).write_text(source.read_text().replace("@BUILD@", str(int(time.time()))))
print("built", sorted(p.name for p in dist.iterdir()))
