#!/usr/bin/env python3
import argparse
import json
import os
from pathlib import Path
import sys
import threading

parser = argparse.ArgumentParser()
parser.add_argument("--control", required=True)
parser.add_argument("--receipts", required=True)
args = parser.parse_args()
lock = threading.Lock()


def status(state):
    with lock:
        os.write(1, ("\x1b]7501;state=" + state + ":app=sum-fixture\x1b\\").encode())


def control():
    while True:
        with open(args.control) as stream:
            for line in stream:
                value = line.strip()
                if value == "exit":
                    os._exit(0)
                status(value)


threading.Thread(target=control, daemon=True).start()
status("idle")
for line in sys.stdin:
    text = line.rstrip("\r\n")
    status("working")
    with Path(args.receipts).open("a") as stream:
        stream.write(json.dumps({"text": text}) + "\n")
    print("APPLIED:" + text, flush=True)
    status("done")
