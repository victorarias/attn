"""Which model should do the rewriting?

40 real ticket descriptions/comments x 6 rewriters, each scored by the gates
already validated: pairwise plainness (sonnet/low, both orders), fact
retention (sonnet/low), structure (checked afterwards by the repo's Go code).
Every model runs at its CLI default effort — effort is a separate axis, and
unexplored for rewriting.

The headline is the pass rate: how often a model's rewrite is good enough to
replace the original. A miss costs nothing but the call; the original ships."""
import json, os, re, subprocess, tempfile, time
from concurrent.futures import ThreadPoolExecutor

NUDGE = ("Restate your last message. Stop using jargon and speak coherently. "
         "State it more simply and concisely, like one human talking to another. "
         "Keep what carries information the prose cannot — diagrams, tables, "
         "code, commands, links. Improve them if you can; just don't drop them. "
         "Keep every number, measurement, and file or line reference, and keep "
         "stated uncertainty stated — a receipt or a caveat is information, not clutter.")

REWRITE_SYSTEM = ("You are an AI coding agent. You just sent the engineer you work for "
                  "the message the user shows you. The engineer replied:\n\n"
                  f"\"{NUDGE}\"\n\nOutput only the restated message, nothing else.")

JUDGE_SYSTEM = """You will see two versions of a message an AI coding agent wrote to the engineer it works for, labelled A and B. Both say roughly the same thing.

The engineer just asked for plain language. Pick the version that explains it more plainly: simple words, one human talking to another, no jargon, no corporate filler, no hedging.

Judge only how it is written. A version is NOT better because it packs in more detail, more precise terminology, or more caveats — that is what he is complaining about. It IS worse if it drops a command, a number, a link, or a code block the other version carries.

Which version would he rather read?

Answer with the single letter A or B and nothing else."""

FACT_SYSTEM = """You will see an ORIGINAL message and a REWRITE of it.

Does the REWRITE drop a concrete fact the ORIGINAL states — a number, measurement, file path, line reference, command, link, code block, decision, or stated caveat?

Answer with a single line and nothing else: NONE if nothing material is missing, otherwise a short quote of one missing item. No reasoning, no hedging, no "actually"."""

def claude(model, effort, system, text, timeout=300):
    cmd = ["claude", "-p", "--strict-mcp-config", "--model", model,
           "--max-turns", "1", "--system-prompt", system]
    if effort:
        cmd += ["--effort", effort]
    out = subprocess.run(cmd, input=text, capture_output=True, text=True, timeout=timeout)
    if out.returncode != 0:
        raise RuntimeError(out.stderr.strip()[:120])
    return out.stdout.strip()

def codex(model, system, text, timeout=600):
    fd, path = tempfile.mkstemp()
    os.close(fd)
    try:
        out = subprocess.run(
            ["codex", "exec", "-m", model, "--skip-git-repo-check", "-o", path, "-"],
            input=f"{system}\n\n---\n\n{text}", capture_output=True, text=True,
            timeout=timeout, cwd="/tmp")
        if out.returncode != 0:
            raise RuntimeError(out.stderr.strip()[:120] or f"exit {out.returncode}")
        with open(path) as f:
            return f.read().strip()
    finally:
        os.unlink(path)

REWRITERS = {
    "opus-5":   lambda t: claude("claude-opus-5", None, REWRITE_SYSTEM, t),
    "sonnet-5": lambda t: claude("claude-sonnet-5", None, REWRITE_SYSTEM, t),
    "haiku-4.5": lambda t: claude("claude-haiku-4-5-20251001", None, REWRITE_SYSTEM, t),
    "luna":     lambda t: codex("gpt-5.6-luna", REWRITE_SYSTEM, t),
    "terra":    lambda t: codex("gpt-5.6-terra", REWRITE_SYSTEM, t),
    "sol":      lambda t: codex("gpt-5.6-sol", REWRITE_SYSTEM, t),
}

def verdict(a, b):
    r = claude("claude-sonnet-5", "low", JUDGE_SYSTEM, f"[VERSION A]\n{a}\n\n[VERSION B]\n{b}")
    m = re.search(r"\b([AB])\b", r)
    if not m:
        raise RuntimeError("no verdict in %r" % r[:60])
    return m.group(1)

texts = [json.loads(l)["text"] for l in open("../ticketreg/tickets.jsonl")][:40]
jobs = [(name, i, t) for name in REWRITERS for i, t in enumerate(texts)]
print(f"{len(texts)} ticket texts x {len(REWRITERS)} rewriters = {len(jobs)} jobs", flush=True)

out = open("exp5.jsonl", "w")
lock = __import__("threading").Lock()

def run(job):
    name, i, text = job
    rec = {"model": name, "idx": i}
    try:
        t0 = time.time()
        rw = REWRITERS[name](text)
        rec["rewrite_s"] = round(time.time() - t0, 1)
        if len(rw.split()) < 20:
            raise RuntimeError("degenerate rewrite: %r" % rw[:60])
        rec["rewrite"] = rw
        t0 = time.time()
        fwd = verdict(text, rw) == "B"
        rev = verdict(rw, text) == "A"
        rec["verify_s"] = round(time.time() - t0, 1)
        rec["plainer"] = fwd and rev
        f = claude("claude-sonnet-5", "low", FACT_SYSTEM,
                   f"[ORIGINAL]\n{text}\n\n[REWRITE]\n{rw}")
        rec["fact_loss"] = "" if f.strip().upper().startswith("NONE") else f[:200]
    except Exception as e:
        rec["error"] = str(e)[:150]
    rec["text"] = text
    with lock:
        out.write(json.dumps(rec) + "\n")
        out.flush()
    return rec

with ThreadPoolExecutor(max_workers=8) as ex:
    results = list(ex.map(run, jobs))
out.close()
print("done — run the Go structure check, then exp5_report.py", flush=True)
