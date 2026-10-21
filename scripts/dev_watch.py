#!/usr/bin/env python3
"""Restart the Go app when source or embedded web assets change."""

from __future__ import annotations

import os
from pathlib import Path
import signal
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parent.parent
POLL_SECONDS = 0.5
stopping = False


def watch_state() -> tuple[tuple[str, int, int], ...]:
    paths = [*ROOT.glob("*.go"), ROOT / "go.mod", ROOT / "go.sum", * (ROOT / "web").rglob("*")]
    state = []
    for path in paths:
        if path.is_file():
            stat = path.stat()
            state.append((str(path.relative_to(ROOT)), stat.st_mtime_ns, stat.st_size))
    return tuple(sorted(state))


def stop_child(child: subprocess.Popen[bytes] | None) -> None:
    if child is None or child.poll() is not None:
        return
    try:
        os.killpg(child.pid, signal.SIGTERM)
        child.wait(timeout=4)
    except subprocess.TimeoutExpired:
        os.killpg(child.pid, signal.SIGKILL)
        child.wait()
    except ProcessLookupError:
        pass


def request_stop(_signum: int, _frame: object) -> None:
    global stopping
    stopping = True


def main() -> int:
    global stopping
    signal.signal(signal.SIGINT, request_stop)
    signal.signal(signal.SIGTERM, request_stop)

    env = os.environ.copy()
    env["FAMILY_DASHBOARD_DEV_MODE"] = "1"
    env["FAMILY_AUTH_MODE"] = "off"
    if not env.get("LISTEN_ADDR"):
        env["LISTEN_ADDR"] = "127.0.0.1:8080"
    if not env.get("TASKS_FILE"):
        env["TASKS_FILE"] = "./data/tasks.json"
    (ROOT / "data").mkdir(exist_ok=True)

    state = watch_state()
    child: subprocess.Popen[bytes] | None = None

    def start() -> subprocess.Popen[bytes]:
        print("Starting Family HQ (watching Go files and web assets)…", flush=True)
        return subprocess.Popen(["go", "run", "."], cwd=ROOT, env=env, start_new_session=True)

    try:
        child = start()
        while not stopping:
            time.sleep(POLL_SECONDS)
            current = watch_state()
            if current != state:
                state = current
                print("Source changed; restarting Family HQ…", flush=True)
                stop_child(child)
                child = start()
            elif child.poll() is not None:
                print(f"Family HQ exited with status {child.returncode}; waiting for a source change…", flush=True)
                child = None
                while not stopping and watch_state() == state:
                    time.sleep(POLL_SECONDS)
                if not stopping:
                    state = watch_state()
                    child = start()
    except FileNotFoundError as error:
        print(f"Cannot start development server: {error}", file=sys.stderr)
        return 127
    finally:
        stop_child(child)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
