#!/usr/bin/env python3
"""
Diagnose why scroll-wheel zoom drops FPS while a single `set_zoom` keeps
it pinned at vsync. Drives many small zoom steps over a window roughly
matching trackpad cadence (60 events / sec for ~1s) and reads the FPS
overlay while the sweep is in progress.

If this matches the observed scroll-wheel slowdown, the cause is in the
canvas's zoom-render pipeline (glyph cache invalidation, notify cascade
on TerminalView, etc.) — independent of the OS scroll-event source.

If this stays at vsync but real scrolling still drops, the cause is
specific to the scroll-event handler path (event coalescing, hit
testing, etc.).

Usage:

    python3 perf-scroll-zoom.py --launch ../target/release/attn-spike5 --panels 4
"""

from __future__ import annotations

import argparse
import json
import os
import socket
import subprocess
import sys
import time
import uuid
from pathlib import Path
from typing import Optional


MANIFEST_PATH = (
    Path.home()
    / "Library"
    / "Application Support"
    / "com.attn.native"
    / "debug"
    / "ui-automation.json"
)


def read_manifest(path: Path = MANIFEST_PATH) -> dict:
    if not path.exists():
        sys.exit(f"manifest not found at {path}")
    return json.loads(path.read_text())


class Client:
    def __init__(self, port: int, token: str):
        self.token = token
        self.sock = socket.create_connection(("127.0.0.1", port), timeout=10)
        self.sock.settimeout(10)
        self.buf = b""

    def call(self, action: str, payload: Optional[dict] = None) -> dict:
        request_id = uuid.uuid4().hex[:8]
        msg = {"id": request_id, "token": self.token, "action": action}
        if payload is not None:
            msg["payload"] = payload
        self.sock.sendall((json.dumps(msg) + "\n").encode())
        return self._read_one(request_id)

    def _read_one(self, expected_id: str) -> dict:
        while b"\n" not in self.buf:
            chunk = self.sock.recv(8192)
            if not chunk:
                raise RuntimeError("automation socket closed")
            self.buf += chunk
        line, _, rest = self.buf.partition(b"\n")
        self.buf = rest
        resp = json.loads(line.decode())
        if resp.get("id") != expected_id:
            raise RuntimeError(f"id mismatch")
        if not resp.get("ok"):
            raise RuntimeError(f"action failed: {resp.get('error')}")
        return resp.get("result") or {}

    def close(self):
        try:
            self.sock.close()
        except OSError:
            pass


def launch_spike(binary: Path, panels: int) -> subprocess.Popen:
    if MANIFEST_PATH.exists():
        MANIFEST_PATH.unlink()
    env = os.environ.copy()
    env.update({
        "ATTN_AUTOMATION": "1",
        "ATTN_SPIKE5_SYNTHETIC_PANELS": str(panels),
    })
    proc = subprocess.Popen(
        [str(binary)],
        env=env,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    deadline = time.time() + 15
    while time.time() < deadline:
        if MANIFEST_PATH.exists():
            return proc
        if proc.poll() is not None:
            sys.exit("spike5 exited early")
        time.sleep(0.1)
    proc.terminate()
    sys.exit("timed out waiting for manifest")


def measure_steady(client: Client, zoom: float) -> dict:
    # Single set_zoom + settle, for the baseline.
    client.call("set_zoom", {"zoom": zoom, "reset": True})
    time.sleep(2.5)
    state = client.call("get_state")
    return state.get("canvas", {}).get("fps", {})


def measure_sweep(client: Client, start: float, end: float, steps: int, step_ms: int) -> dict:
    """Drive `steps` rapid set_zoom calls between start and end, then read
    the FPS counter. Counter is reset only on the first step so the final
    readout reflects the sweep's own steady-state cost."""
    for i in range(steps):
        t = i / max(steps - 1, 1)
        zoom = start * (end / start) ** t
        client.call("set_zoom", {"zoom": zoom, "reset": i == 0})
        time.sleep(step_ms / 1000.0)
    state = client.call("get_state")
    return state.get("canvas", {}).get("fps", {})


def measure_post_sweep(client: Client, start: float, end: float, steps: int, step_ms: int,
                       settle_seconds: float = 3.0) -> dict:
    """Drive a sweep, then SIT at the final zoom for `settle_seconds` with
    the FPS counter reset, and read steady-state. This is the right way
    to ask 'after I scrolled, what's the FPS at this new zoom?'"""
    for i in range(steps):
        t = i / max(steps - 1, 1)
        zoom = start * (end / start) ** t
        # Don't reset during the sweep so we don't perturb the canvas's
        # observed behavior. We reset AFTER the sweep below.
        client.call("set_zoom", {"zoom": zoom, "reset": False})
        time.sleep(step_ms / 1000.0)
    # Now reset the counter and let it accumulate fresh samples at the
    # post-scroll zoom for `settle_seconds`. This isolates "lingering
    # cost from the recent scroll" from "steady-state cost at this
    # zoom".
    final_zoom_now = client.call("get_state").get("canvas", {}).get("viewport", {}).get("zoom")
    client.call("set_zoom", {"zoom": final_zoom_now, "reset": True})
    time.sleep(settle_seconds)
    state = client.call("get_state")
    return state.get("canvas", {}).get("fps", {})


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--panels", type=int, required=True)
    parser.add_argument("--launch", type=Path, required=True)
    parser.add_argument("--steps", type=int, default=60,
                        help="Number of zoom steps in the synthetic sweep (default 60).")
    parser.add_argument("--step-ms", type=int, default=16,
                        help="Sleep between steps in ms (default 16, ~60Hz).")
    args = parser.parse_args()

    proc = launch_spike(args.launch, args.panels)
    time.sleep(2.5)
    try:
        manifest = read_manifest()
        client = Client(manifest["port"], manifest["token"])
        try:
            client.call("ping")
            print(f"## n={args.panels} panels  scroll-zoom diagnosis")
            print()

            # Baselines: clean steady state at each zoom level.
            print("Baseline (single set_zoom, settled):")
            for z in (1.0, 0.25):
                fps = measure_steady(client, z)
                print(f"  zoom={z}: fps={fps.get('fps'):>4.1f}, avg={fps.get('avg_ms'):>5.2f}ms, last={fps.get('last_ms'):>5.2f}ms")

            print()
            print(f"Synthetic sweep DURING (steps={args.steps}, step_ms={args.step_ms}, ~{args.steps * args.step_ms / 1000.0:.2f}s):")
            for direction in ("zoom_out", "zoom_in"):
                if direction == "zoom_out":
                    fps = measure_sweep(client, 1.0, 0.25, args.steps, args.step_ms)
                else:
                    client.call("set_zoom", {"zoom": 1.0, "reset": True})
                    time.sleep(0.5)
                    fps = measure_sweep(client, 0.25, 1.0, args.steps, args.step_ms)
                print(f"  {direction}: fps={fps.get('fps'):>4.1f}, avg={fps.get('avg_ms'):>5.2f}ms, last={fps.get('last_ms'):>5.2f}ms")

            print()
            print(f"Steady state AFTER sweep (settle 3s with counter reset):")
            print(f"  Clean endpoints (1.0↔0.25):")
            for direction in ("zoom_out", "zoom_in"):
                if direction == "zoom_out":
                    client.call("set_zoom", {"zoom": 1.0, "reset": True})
                    time.sleep(0.5)
                    fps = measure_post_sweep(client, 1.0, 0.25, args.steps, args.step_ms)
                else:
                    client.call("set_zoom", {"zoom": 0.25, "reset": True})
                    time.sleep(0.5)
                    fps = measure_post_sweep(client, 0.25, 1.0, args.steps, args.step_ms)
                print(f"    {direction}: fps={fps.get('fps'):>4.1f}, avg={fps.get('avg_ms'):>5.2f}ms, last={fps.get('last_ms'):>5.2f}ms")

            # Real scroll lands at sub-pixel zoom values. Sweep to a
            # non-clean endpoint (close to 0.25 but not exactly) and
            # see if the steady-state cost differs.
            print(f"  Non-clean endpoint (1.0→0.2683):")
            client.call("set_zoom", {"zoom": 1.0, "reset": True})
            time.sleep(0.5)
            fps = measure_post_sweep(client, 1.0, 0.2683, args.steps, args.step_ms)
            print(f"    zoom_out: fps={fps.get('fps'):>4.1f}, avg={fps.get('avg_ms'):>5.2f}ms, last={fps.get('last_ms'):>5.2f}ms")

            # Hold-at-clean baseline (no sweep, just set_zoom and sit).
            print(f"  Hold-at baseline (no preceding sweep):")
            for z in (0.25, 0.2683):
                client.call("set_zoom", {"zoom": z, "reset": True})
                time.sleep(3.0)
                state = client.call("get_state")
                fps = state.get("canvas", {}).get("fps", {})
                print(f"    zoom={z}: fps={fps.get('fps'):>4.1f}, avg={fps.get('avg_ms'):>5.2f}ms, last={fps.get('last_ms'):>5.2f}ms")
        finally:
            client.close()
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill()


if __name__ == "__main__":
    main()
