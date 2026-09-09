#!/usr/bin/env python3
"""Tiny CLI: `hello.py NAME` prints a greeting; `--shout` upper-cases it."""
import argparse, sys

def greet(name, shout=False):
    text = f"CI fault seed, {name}!"
    return text.upper() if shout else text

def main(argv=None):
    parser = argparse.ArgumentParser()
    parser.add_argument("name")
    parser.add_argument("--shout", action="store_true")
    args = parser.parse_args(argv)
    print(greet(args.name, args.shout))
    return 0

if __name__ == "__main__":
    sys.exit(main())
