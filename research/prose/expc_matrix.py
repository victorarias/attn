"""Judge-model matrix for design C's pairwise verdict layer.

Same 17 (rejected, accepted-revision) pairs, both orders, prompt #2.
Configs: sonnet at three effort levels, gpt-5.6-terra at three reasoning
levels. Reports ships / flips / wrong-ships, per-call accuracy, and latency
— the effort knob is a practicality question as much as an accuracy one."""
import json, os, re, subprocess, time
from concurrent.futures import ThreadPoolExecutor

SYSTEM = """You will see two versions of a message an AI coding agent wrote to the engineer it works for, labelled A and B. Both say roughly the same thing.

The engineer just asked for plain language. Pick the version that explains it more plainly: simple words, one human talking to another, no jargon, no corporate filler, no hedging.

Judge only how it is written. A version is NOT better because it packs in more detail, more precise terminology, or more caveats — that is what he is complaining about. It IS worse if it drops a command, a number, a link, or a code block the other version carries.

Which version would he rather read?

Answer with the single letter A or B and nothing else."""

def ask_claude(effort):
    def ask(a, b):
        out = subprocess.run(
            ["claude", "-p", "--strict-mcp-config", "--model", "claude-sonnet-5",
             "--effort", effort, "--max-turns", "1", "--system-prompt", SYSTEM],
            input=f"[VERSION A]\n{a}\n\n[VERSION B]\n{b}",
            capture_output=True, text=True, timeout=300)
        if out.returncode != 0:
            raise RuntimeError(out.stderr.strip()[:120])
        m = re.search(r"\b([AB])\b", out.stdout.strip())
        if not m:
            raise RuntimeError("no verdict in %r" % out.stdout[:60])
        return m.group(1)
    return ask

def ask_terra(reasoning):
    def ask(a, b):
        out = subprocess.run(
            ["codex", "exec", "-m", "gpt-5.6-terra",
             "-c", f'model_reasoning_effort="{reasoning}"',
             "--skip-git-repo-check", "-"],
            input=f"{SYSTEM}\n\n[VERSION A]\n{a}\n\n[VERSION B]\n{b}",
            capture_output=True, text=True, timeout=300, cwd="/tmp")
        if out.returncode != 0:
            raise RuntimeError(out.stderr.strip()[:120])
        for line in reversed(out.stdout.strip().splitlines()):
            s = line.strip()
            if s:
                m = re.search(r"\b([AB])\b", s)
                if not m:
                    raise RuntimeError("no verdict in %r" % s[:60])
                return m.group(1)
        raise RuntimeError("no output")
    return ask

CONFIGS = [
    ("sonnet/low", ask_claude("low")),
    ("sonnet/medium", ask_claude("medium")),
    ("sonnet/high", ask_claude("high")),
    ("terra/low", ask_terra("low")),
    ("terra/medium", ask_terra("medium")),
    ("terra/high", ask_terra("high")),
]

cases = [json.loads(l) for l in open("corpus_pure.jsonl")]

def run_config(name, ask):
    lat = []
    def timed(a, b):
        t0 = time.time()
        v = ask(a, b)
        lat.append(time.time() - t0)
        return v

    def judge(c):
        try:
            fwd = timed(c["dense"], c["revision"])   # revision should win as B
            rev = timed(c["revision"], c["dense"])   # revision should win as A
            return {"fwd": fwd == "B", "rev": rev == "A"}
        except Exception as e:
            return {"error": str(e)[:120]}

    with ThreadPoolExecutor(max_workers=8) as ex:
        rs = list(ex.map(judge, cases))
    ok = [r for r in rs if "error" not in r]
    errs = len(rs) - len(ok)
    both = sum(r["fwd"] and r["rev"] for r in ok)
    neither = sum(not r["fwd"] and not r["rev"] for r in ok)
    flip = len(ok) - both - neither
    acc = sum(r["fwd"] + r["rev"] for r in ok)
    lat.sort()
    p50 = lat[len(lat) // 2] if lat else 0
    print(f"{name:14} ships {both:2}/17  flip {flip:2}  WRONG {neither:2}  "
          f"acc {100*acc/max(2*len(ok),1):3.0f}%  p50 {p50:5.1f}s  max {lat[-1] if lat else 0:5.1f}s"
          f"{'  errors ' + str(errs) if errs else ''}", flush=True)

print(f"{len(cases)} pairs x 2 orders per config")
for name, ask in CONFIGS:
    run_config(name, ask)
