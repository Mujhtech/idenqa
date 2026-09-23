#!/usr/bin/env python3
"""Prepare the same digest-pinned evaluation model as Capture Web, offline-capable."""
import argparse
import hashlib
from pathlib import Path
import urllib.request

URL = "https://storage.googleapis.com/mediapipe-models/face_landmarker/face_landmarker/float16/1/face_landmarker.task"
DIGEST = "64184e229b263107bc2b804c6625db1341ff2bb731874b0bcc2fe6544e0bc9ff"

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--model", type=Path, help="Use a local model instead of downloading")
args = parser.parse_args()
if args.model:
    data = args.model.read_bytes()
else:
    with urllib.request.urlopen(URL, timeout=60) as response:
        data = response.read(8 * 1024 * 1024 + 1)
if len(data) > 8 * 1024 * 1024 or hashlib.sha256(data).hexdigest() != DIGEST:
    raise SystemExit("Face Landmarker model digest mismatch")
output = Path(__file__).resolve().parents[1] / "idenqa/src/main/assets/idenqa/face_landmarker.task"
output.parent.mkdir(parents=True, exist_ok=True)
temporary = output.with_suffix(".tmp")
temporary.write_bytes(data)
temporary.replace(output)
print(f"Prepared pinned evaluation model: {output}")
