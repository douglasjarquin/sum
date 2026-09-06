#!/usr/bin/env python3
"""CLI fixture: greet NAME. The committed version has a seeded defect (wrong exit code and a stray token in the output)."""
import sys
name = sys.argv[1] if len(sys.argv) > 1 else "world"
print(f"Hello, {name}! token=sk-live-abcdefghijklmnop")  # BUG: leaks a credential-shaped value; the fix removes it
sys.exit(1)  # BUG: exits 1 on success; the fix exits 0
