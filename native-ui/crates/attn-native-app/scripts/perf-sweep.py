#!/usr/bin/env python3
"""
Drive the canvas perf sweep against a running attn-spike5 process.

Reads the automation manifest, connects over TCP, then walks a matrix of
(panel_count, zoom) configurations. At each cell it sets the requested
zoom, sleeps to let the FPS counter's 1-second window populate with
post-change samples, and reads `canvas.fps` out of the automation
snapshot.

Panel count is fixed for one process (set via env var at launch), so
each panel-count row is a separate process. The script auto-launches
processes itself when given `--launch <release-binary>`.

Usage examples:

    # Drive a sweep against an already-running spike (you launched
    # `attn-spike5` yourself with the env vars set).
    python3 perf-sweep.py --panels 4

    # Have the script launch the binary with N panels and tear it down
    # at the end.
    python3 perf-sweep.py --panels 4 --launch ../target/release/attn-spike5

The script always prints results to stdout as a small markdown-friendly
table so they can be pasted into the plan-doc Findings section.
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

DEFAULT_ZOOMS = (1.0, 0.5, 0.25)
SETTLE_SECONDS = 2.5
SAMPLE_SECONDS = 1.0  # extra settle time after the FPS counter window fills


def read_manifest(path: Path = MANIFEST_PATH) -> dict:
    if not path.exists():
        sys.exit(f"manifest not found at {path} — is attn-spike5 running with ATTN_AUTOMATION=1?")
    return json.loads(path.read_text())


class Client:
    def __init__(self, port: int, token: str):
        self.token = token
        self.sock = socket.create_connection(("127.0.0.1", port), timeout=10)
        self.sock.settimeout(10)
        self.buf = b""

    def call(self, action: str, payload: Optional[dict] = None) -> dict:
        request_id = uuid.uuid4().hex[:8]
        msg = {
            "id": request_id,
            "token": self.token,
            "action": action,
        }
        if payload is not None:
            msg["payload"] = payload
        line = (json.dumps(msg) + "\n").encode()
        self.sock.sendall(line)
        return self._read_one(request_id)

    def _read_one(self, expected_id: str) -> dict:
        # Newline-delimited; loop reading until we have one full line.
        while b"\n" not in self.buf:
            chunk = self.sock.recv(8192)
            if not chunk:
                raise RuntimeError("automation socket closed before response")
            self.buf += chunk
        line, _, rest = self.buf.partition(b"\n")
        self.buf = rest
        resp = json.loads(line.decode())
        if resp.get("id") != expected_id:
            raise RuntimeError(f"id mismatch: expected {expected_id}, got {resp.get('id')}")
        if not resp.get("ok"):
            raise RuntimeError(f"action failed: {resp.get('error')}")
        return resp.get("result") or {}

    def close(self):
        try:
            self.sock.close()
        except OSError:
            pass


def measure_at_zoom(client: Client, zoom: float) -> dict:
    client.call("set_zoom", {"zoom": zoom})
    # Reset clears the sample window; we want at least 1s of post-reset
    # samples in there for `avg_ms` to reflect the new state, plus a
    # little extra for parsing/scheduling jitter.
    time.sleep(SETTLE_SECONDS + SAMPLE_SECONDS)
    state = client.call("get_state")
    fps = state.get("canvas", {}).get("fps", {})
    viewport = state.get("canvas", {}).get("viewport", {})
    return {
        "zoom_target": zoom,
        "zoom_actual": viewport.get("zoom"),
        "fps": fps.get("fps"),
        "avg_ms": fps.get("avg_ms"),
        "last_ms": fps.get("last_ms"),
    }


def launch_spike(binary: Path, panels: int, env_extra: dict) -> subprocess.Popen:
    env = os.environ.copy()
    env.update({
        "ATTN_AUTOMATION": "1",
        "ATTN_SPIKE5_SYNTHETIC_PANELS": str(panels),
    })
    env.update(env_extra)
    proc = subprocess.Popen(
        [str(binary)],
        env=env,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    # Wait for manifest to appear.
    deadline = time.time() + 15
    while time.time() < deadline:
        if MANIFEST_PATH.exists():
            return proc
        if proc.poll() is not None:
            sys.exit(f"spike5 exited before manifest appeared (rc={proc.returncode})")
        time.sleep(0.1)
    proc.terminate()
    sys.exit("timed out waiting for automation manifest")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--panels", type=int, required=True,
                        help="Panel count for this run (informational only — must match the running spike).")
    parser.add_argument("--launch", type=Path,
                        help="Path to spike5 release binary. If set, the script launches and tears down the process.")
    parser.add_argument("--zooms", type=float, nargs="+", default=list(DEFAULT_ZOOMS),
                        help="Zoom levels to sweep (default: 1.0 0.5 0.25).")
    parser.add_argument("--bytes-per-tick", type=int, default=80,
                        help="ATTN_SPIKE5_SYNTHETIC_BYTES for the launched process.")
    parser.add_argument("--tick-ms", type=int, default=16,
                        help="ATTN_SPIKE5_SYNTHETIC_TICK_MS for the launched process.")
    args = parser.parse_args()

    proc = None
    if args.launch:
        # Always start from a fresh manifest so we don't hit a stale port.
        if MANIFEST_PATH.exists():
            MANIFEST_PATH.unlink()
        env_extra = {
            "ATTN_SPIKE5_SYNTHETIC_BYTES": str(args.bytes_per_tick),
            "ATTN_SPIKE5_SYNTHETIC_TICK_MS": str(args.tick_ms),
        }
        proc = launch_spike(args.launch, args.panels, env_extra)
        # Give GPUI a beat to spin up the window + first synthetic ticks.
        time.sleep(2.5)

    try:
        manifest = read_manifest()
        client = Client(manifest["port"], manifest["token"])
        try:
            client.call("ping")
            results = [measure_at_zoom(client, z) for z in args.zooms]
        finally:
            client.close()
    finally:
        if proc is not None:
            proc.terminate()
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()

    # Render results.
    print()
    print(f"## n={args.panels} panels  (bytes/tick={args.bytes_per_tick}, tick={args.tick_ms}ms)")
    print()
    print(f"| zoom | fps   | avg ms | last ms |")
    print(f"|------|-------|--------|---------|")
    for r in results:
        z = r["zoom_target"]
        fps = r["fps"]
        avg = r["avg_ms"]
        last = r["last_ms"]
        print(f"| {z:>4.2f} | {fps:>5.1f} | {avg:>6.2f} | {last:>7.2f} |")
    print()


if __name__ == "__main__":
    main()
