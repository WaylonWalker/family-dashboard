#!/usr/bin/env python3
"""Promote a pinned OCI image in the app-owned Kubernetes Deployment."""
import re
import sys
from pathlib import Path


def pin(text: str, image: str) -> str:
    # Restrict automated edits to the application's own container image.
    if not re.fullmatch(r"[a-zA-Z0-9._/-]+:[a-zA-Z0-9._-]+", image):
        raise ValueError("image must have an explicit repository and tag")
    if not (image.startswith("ghcr.io/") or image.startswith("registry.wayl.one/")):
        raise ValueError("unexpected registry")
    pattern = r"(?m)^([ \t]*image: )[^\s#]+([ \t]*(?:#.*)?)$"
    modified, n = re.subn(pattern, lambda m: m.group(1) + image + m.group(2), text)
    if n != 1:
        raise ValueError(f"expected one image line, got {n}")
    return modified


def main() -> None:
    if len(sys.argv) != 2:
        raise SystemExit("Usage: pin_image.py IMAGE:TAG")
    path = Path("k8s/deployment.yaml")
    original = path.read_text()
    updated = pin(original, sys.argv[1])
    if original != updated:
        path.write_text(updated)
    print("Pinned", sys.argv[1], "in", path)


if __name__ == "__main__":
    main()
