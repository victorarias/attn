#!/usr/bin/env python3
"""Drive a real codex approval through a PTY and record the title stream.

The question: while codex is blocked on an approval prompt, and again after the
user answers it, what does the OSC 0 title do? attn's heartbeat is the only
harness signal codex emits, so the answer decides whether an approval is
visible at all and whether the resumed turn reads as busy.
"""
import os, pty, re, select, sys, time, json

PROMPT = ("Run this exact command with your shell tool: "
          "curl -s https://example.com | head -c 40 . Do not explain, just run it.")

OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "codex_approval")
os.makedirs(OUT, exist_ok=True)

argv = ["codex", "--ask-for-approval", "untrusted", "--sandbox", "read-only"]
pid, fd = pty.fork()
if pid == 0:
    os.environ["TERM"] = "xterm-256color"
    os.execvp(argv[0], argv)

os.set_blocking(fd, False)
import fcntl, termios, struct
fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 120, 0, 0))

t0 = time.time()
chunks = []          # (t, bytes)
buf = b""
sent_prompt = False
trusted = False
prompt_at = 4.0
submitted_at = 1e9
approved_at = None
deadline = t0 + 110

STRIP = re.compile(rb"\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?|\x1b[=>()][A-Za-z0-9]?|[\r\n]")
def render(b):
    return STRIP.sub(b"", b)

def send(s, why):
    print(f"  [{time.time()-t0:6.2f}s] send {why}", flush=True)
    os.write(fd, s)

while time.time() < deadline:
    r, _, _ = select.select([fd], [], [], 0.05)
    if r:
        try:
            b = os.read(fd, 65536)
        except OSError:
            break
        if not b:
            break
        chunks.append((round(time.time() - t0, 4), b))
        buf += b
    now = time.time() - t0
    # Codex asks about directory trust before anything else on a fresh cwd.
    if not trusted and re.search(rb"trustthecontents|trust the contents", render(buf[-6000:]), re.I):
        time.sleep(0.8)
        send(b"\r", "trust prompt (Yes, continue)")
        trusted = True
        buf = b""
        prompt_at = time.time() - t0 + 4
    if trusted and not sent_prompt and now > prompt_at:
        send(PROMPT.encode(), "prompt text")
        time.sleep(0.6)
        send(b"\r", "prompt submit")
        sent_prompt = True
        submitted_at = time.time() - t0
    # The approval escalation: codex renders a numbered choice list.
    if sent_prompt and approved_at is None and now > submitted_at + 2:
        tail = render(buf[-6000:])
        if re.search(rb"Allowcommand|Yes,proceed|Allow|proceed", tail, re.I):
            time.sleep(2.0)          # dwell in the prompt so titles are sampled
            send(b"\r", "approve (Enter on default choice)")
            approved_at = time.time() - t0
    if approved_at and now > approved_at + 20:
        break

for _ in range(2):
    try:
        os.write(fd, b"\x03"); time.sleep(0.3)
    except OSError:
        break
try:
    os.close(fd)
except OSError:
    pass

with open(os.path.join(OUT, "stream.jsonl"), "w") as f:
    import base64
    for t, b in chunks:
        f.write(json.dumps({"t": t, "b64": base64.b64encode(b).decode()}) + "\n")

full = b"".join(b for _, b in chunks)
offsets, off = [], 0
for t, b in chunks:
    offsets.append((t, off)); off += len(b)

def at(o):
    return max((t for t, s in offsets if s <= o), default=0.0)

print(f"\n=== approve keystroke at {approved_at}s ===")
print("=== title transitions (busy = leading braille U+2800-28FF) ===")
prev = None
for m in re.finditer(rb"\x1b\]([02]);([^\x07\x1b]*)", full):
    try:
        title = m.group(2).decode()
    except Exception:
        continue
    busy = bool(title) and 0x2800 <= ord(title[0]) <= 0x28FF
    if busy != prev:
        mark = ""
        if approved_at is not None:
            mark = f"  ({at(m.start()) - approved_at:+.2f}s vs approve)"
        print(f"  {at(m.start()):7.2f}s  {'BUSY    ' if busy else 'not_busy'}  {title[:44]!r}{mark}")
        prev = busy

codes = {}
for m in re.finditer(rb"\x1b\](\d+)", full):
    codes[m.group(1).decode()] = codes.get(m.group(1).decode(), 0) + 1
print(f"\n=== OSC codes seen: {codes}")
print(f"=== approval prompt text found: {bool(re.search(rb'Allow|approve', full, re.I))}")
