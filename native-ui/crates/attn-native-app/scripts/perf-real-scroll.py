#!/usr/bin/env python3
"""
Drive REAL Cmd+scroll-wheel events into the spike5 canvas and measure
steady-state FPS afterwards. Mirrors the user's interactive workflow as
closely as the OS event API allows, so we can isolate "scroll-event
delivery is itself expensive" from "post-zoom steady state is
degraded".

Steps per run:
  1. Launch attn-spike5 with N synthetic panels, ATTN_AUTOMATION=1.
  2. Read window + canvas bounds so we know where to scroll.
  3. Activate the window (so scroll events land on the right app).
  4. Set FPS counter baseline at zoom=1.0 and record.
  5. Drive K real scroll-wheel events at the canvas center (Cmd held).
  6. Wait `settle_seconds` so the FPS counter window flushes any
     during-scroll samples and only post-scroll renders count.
  7. Reset the FPS counter (so old samples don't leak in), wait again,
     then read steady-state FPS.

Usage:
  python3 perf-real-scroll.py --launch ../target/release/attn-spike5 --panels 4

Requires Accessibility permission for whatever process invokes
`swift scroll-driver.swift`.
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


SCRIPT_DIR = Path(__file__).resolve().parent
SCROLL_DRIVER = SCRIPT_DIR / "scroll-driver.swift"
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
        rid = uuid.uuid4().hex[:8]
        msg = {"id": rid, "token": self.token, "action": action}
        if payload is not None:
            msg["payload"] = payload
        self.sock.sendall((json.dumps(msg) + "\n").encode())
        return self._read(rid)

    def _read(self, expected_id: str) -> dict:
        while b"\n" not in self.buf:
            chunk = self.sock.recv(8192)
            if not chunk:
                raise RuntimeError("automation socket closed")
            self.buf += chunk
        line, _, rest = self.buf.partition(b"\n")
        self.buf = rest
        resp = json.loads(line.decode())
        if resp.get("id") != expected_id:
            raise RuntimeError("id mismatch")
        if not resp.get("ok"):
            raise RuntimeError(f"failed: {resp.get('error')}")
        return resp.get("result") or {}

    def close(self):
        try:
            self.sock.close()
        except OSError:
            pass


def launch_spike(binary: Path, panels: int, tick_ms: int, bytes_per_tick: int) -> subprocess.Popen:
    if MANIFEST_PATH.exists():
        MANIFEST_PATH.unlink()
    env = os.environ.copy()
    env.update({
        "ATTN_AUTOMATION": "1",
        "ATTN_SPIKE5_SYNTHETIC_PANELS": str(panels),
        "ATTN_SPIKE5_SYNTHETIC_TICK_MS": str(tick_ms),
        "ATTN_SPIKE5_SYNTHETIC_BYTES": str(bytes_per_tick),
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


def activate_spike_window():
    subprocess.run(
        ["osascript", "-e",
         'tell application "System Events" to set frontmost of (first process whose name is "attn-spike5") to true'],
        check=False,
    )
    time.sleep(0.3)


def canvas_center_screen(client: Client) -> tuple[float, float]:
    geom = client.call("get_window_geometry")
    state = client.call("get_state")
    win = geom["globalBounds"]
    canvas = state["canvas"]["bounds"]
    cx = win["x"] + canvas["x"] + canvas["width"] / 2.0
    cy = win["y"] + canvas["y"] + canvas["height"] / 2.0
    return cx, cy


def drive_scroll(x: float, y: float, count: int, delta: int, step_us: int, no_cmd: bool = False) -> None:
    cmd = ["swift", str(SCROLL_DRIVER), str(x), str(y), str(count), str(delta), str(step_us)]
    if no_cmd:
        cmd.append("--no-cmd")
    subprocess.run(cmd, check=True)


def read_fps(client: Client) -> dict:
    state = client.call("get_state")
    return state.get("canvas", {}).get("fps", {})


def viewport_zoom(client: Client) -> float:
    state = client.call("get_state")
    return state.get("canvas", {}).get("viewport", {}).get("zoom", 0.0)


def viewport_full(client: Client) -> dict:
    state = client.call("get_state")
    return state.get("canvas", {}).get("viewport", {})


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--launch", type=Path, required=True)
    parser.add_argument("--panels", type=int, default=4)
    parser.add_argument("--tick-ms", type=int, default=16,
                        help="Synthetic ticker cadence. 0 = static panels.")
    parser.add_argument("--bytes-per-tick", type=int, default=80)
    parser.add_argument("--scroll-count", type=int, default=80,
                        help="Number of scroll events to post.")
    parser.add_argument("--delta-per-step", type=int, default=-10,
                        help="Pixels per scroll event (negative = zoom out).")
    parser.add_argument("--step-us", type=int, default=8000,
                        help="Microseconds between scroll events.")
    parser.add_argument("--settle", type=float, default=4.0,
                        help="Seconds to settle after scroll before measuring.")
    parser.add_argument("--no-cmd", action="store_true",
                        help="Drop the Cmd modifier — scroll events become pan instead of zoom.")
    args = parser.parse_args()

    proc = launch_spike(args.launch, args.panels, args.tick_ms, args.bytes_per_tick)
    time.sleep(2.5)
    try:
        manifest = read_manifest()
        client = Client(manifest["port"], manifest["token"])
        try:
            client.call("ping")
            client.call("set_zoom", {"zoom": 1.0, "reset": True})
            time.sleep(2.0)

            print(f"## n={args.panels}, tick_ms={args.tick_ms}, bytes={args.bytes_per_tick}")
            print(f"   scroll: {args.scroll_count}× delta={args.delta_per_step} every {args.step_us}us, settle={args.settle}s")
            print()

            base = read_fps(client)
            vp_before = viewport_full(client)
            print(f"  baseline    viewport={vp_before}: fps={base.get('fps')}, avg={base.get('avg_ms'):.2f}ms")

            geom = client.call("get_window_geometry")
            state = client.call("get_state")
            print(f"  geometry: window={geom['globalBounds']}")
            print(f"  geometry: canvas-bounds={state['canvas']['bounds']}")
            cx, cy = canvas_center_screen(client)
            print(f"  scroll target: ({cx:.0f}, {cy:.0f})")
            activate_spike_window()
            drive_scroll(cx, cy, args.scroll_count, args.delta_per_step, args.step_us, no_cmd=args.no_cmd)

            # Reactivate just in case the swift call lost focus, then
            # let renders settle. Reset the counter AFTER the settle so
            # the 1s window only sees post-settle frames.
            time.sleep(args.settle)
            vp_after = viewport_full(client)
            client.call("set_zoom", {"zoom": vp_after.get("zoom", 1.0), "reset": True})
            time.sleep(1.5)
            after = read_fps(client)
            print(f"  post-scroll viewport={vp_after}: fps={after.get('fps')}, avg={after.get('avg_ms'):.2f}ms, last={after.get('last_ms'):.2f}ms")
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
