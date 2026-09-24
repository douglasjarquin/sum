#!/usr/bin/env python3
"""A strict fake for the one ps call sum makes: `ps -o lstart= -p PID` under LC_ALL=C TZ=UTC.

Fake Herdr pids are not real processes, so their start times are scenario data: FAKE_PS_STARTS is a JSON object of
pid -> RFC 3339 UTC start time, FAKE_PS_GONE a comma-separated list of pids that are not running, and every other pid
started at FAKE_PS_DEFAULT_START (default 2025-01-01, before any fixture record).
"""
import json, os, sys
from datetime import datetime
args = sys.argv[1:]
if len(args) != 4 or args[:3] != ["-o", "lstart=", "-p"]:
    print("fake ps: unsupported arguments %r" % args, file=sys.stderr); sys.exit(2)
pid = args[3]
if pid in os.environ.get("FAKE_PS_GONE", "").split(","):
    sys.exit(1)
starts = json.loads(os.environ.get("FAKE_PS_STARTS") or "{}")
started = starts.get(pid, os.environ.get("FAKE_PS_DEFAULT_START", "2025-01-01T00:00:00Z"))
print(datetime.strptime(started, "%Y-%m-%dT%H:%M:%SZ").strftime("%a %b %e %H:%M:%S %Y"))
