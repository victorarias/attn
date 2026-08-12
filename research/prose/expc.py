"""Test 1 for design C: can a pairwise judge pick Victor's accepted revision
over the message he rejected?

Each of the 17 pure pairs is judged twice (positions swapped). C's shipping
rule is 'rewrite ships only if it wins both orders', so the headline number is
both-orders wins; flips and dense-wins are the failure modes."""
import json, re, subprocess
from concurrent.futures import ThreadPoolExecutor

SYSTEM = """You will see two versions of a message an AI coding agent wrote to the engineer it works for, labelled A and B.

The engineer is blunt. He wants plain language: no jargon, no hedging, no corporate filler. He also wants nothing lost — facts, numbers, commands, code, diagrams, and links must survive.

Which version would he rather receive?

Answer with the single letter A or B and nothing else."""

def ask(a, b):
    prompt = f"[VERSION A]\n{a}\n\n[VERSION B]\n{b}"
    out = subprocess.run(
        ["claude", "-p", "--strict-mcp-config",
         "--model", "claude-haiku-4-5-20251001", "--max-turns", "1",
         "--system-prompt", SYSTEM],
        input=prompt, capture_output=True, text=True, timeout=120)
    if out.returncode != 0:
        raise RuntimeError(out.stderr.strip()[:120])
    m = re.search(r"\b([AB])\b", out.stdout.strip())
    if not m:
        raise RuntimeError("no verdict in %r" % out.stdout[:60])
    return m.group(1)

cases = [json.loads(l) for l in open("corpus_pure.jsonl")]
print(f"{len(cases)} pairs x 2 orders", flush=True)

def judge(i_case):
    i, c = i_case
    # fwd: A=dense B=revision (revision should win as B)
    # rev: A=revision B=dense (revision should win as A)
    fwd = ask(c["dense"], c["revision"])
    rev = ask(c["revision"], c["dense"])
    rev_wins_fwd = fwd == "B"
    rev_wins_rev = rev == "A"
    return {"index": i, "fwd": rev_wins_fwd, "rev": rev_wins_rev}

with ThreadPoolExecutor(max_workers=8) as ex:
    results = list(ex.map(judge, enumerate(cases)))

both = sum(r["fwd"] and r["rev"] for r in results)
neither = sum(not r["fwd"] and not r["rev"] for r in results)
flip = len(results) - both - neither
per_call = sum(r["fwd"] + r["rev"] for r in results)
n = len(results)
print(f"\nrevision wins both orders (ships): {both}/{n}")
print(f"order-dependent flip (original ships): {flip}/{n}")
print(f"dense wins both orders (judge wrong): {neither}/{n}")
print(f"per-call accuracy: {per_call}/{2*n} = {100*per_call/(2*n):.0f}%")
print(f"consistency (same verdict both orders): {both+neither}/{n}")
for r in results:
    if not (r["fwd"] and r["rev"]):
        print(f"  case {r['index']}: fwd={'rev' if r['fwd'] else 'dense'} rev={'rev' if r['rev'] else 'dense'}")
