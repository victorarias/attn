"""Experiment A: a judge that sees what the reader saw.

Same population and hand labels as expb; haiku scores each message 0-10 for
"will he push back on how this is written", but now with the visible
conversation prefix in the prompt. Writes judge rows to expa.jsonl."""
import json, random, re, subprocess, sys
from concurrent.futures import ThreadPoolExecutor

SYSTEM = """You will see a conversation between an engineer and the AI coding agent working for him, exactly as the engineer saw it, followed by one NEW message from the agent.

The engineer is blunt and dislikes dense, jargon-heavy writing — and above all he dislikes messages that assume knowledge he does not have. Sometimes he replies asking for plain language or says he does not understand; usually he just gets on with the work.

Rate from 0 to 10 how likely he is to push back on how the NEW message is WRITTEN — because it is dense, verbose, jargon-heavy, or leans on terms and facts he has not seen in this conversation. Not whether he agrees with it, and not whether the work is good.

0 means he will not comment on the writing at all. 10 means he will certainly ask for it in plainer words or say he does not understand.

Answer with a single integer and nothing else."""

def ask(prompt_text):
    out = subprocess.run(
        ["claude", "-p", "--strict-mcp-config",
         "--model", "claude-haiku-4-5-20251001", "--max-turns", "1",
         "--system-prompt", SYSTEM],
        input=prompt_text, capture_output=True, text=True, timeout=120)
    if out.returncode != 0:
        raise RuntimeError(out.stderr.strip()[:120] or "exit %d" % out.returncode)
    m = re.search(r"\d+", out.stdout)
    if not m:
        raise RuntimeError("no digit in %r" % out.stdout[:60])
    return int(m.group())

labels = {}
with open("silent.jsonl.handlabel") as f:
    for line in f:
        r = json.loads(line)
        labels[(r["file"], r["line"])] = r["label"]

rows = []
with open("silentctx.jsonl") as f:
    for line in f:
        r = json.loads(line)
        lab = labels.get((r["file"], r["line"]))
        if lab is None or len(r["context"]) < 2:
            continue
        if len(re.findall(r"[a-zA-Z][a-zA-Z'-]*", re.sub(r"```.*?```", " ", r["text"], flags=re.S))) < 150:
            continue
        rows.append((lab, r))

complaints = [x for x in rows if x[0] == "complaint"]
silent = [x for x in rows if x[0] == "silent"]
random.Random(7).shuffle(silent)
silent = silent[:350]
sample = complaints + silent
print(f"judging {len(sample)} ({len(complaints)} complaint, {len(silent)} silent)", flush=True)

def build_prompt(r):
    turns = []
    budget = 8000
    for t in reversed(r["context"]):
        text = t["text"][:3000]
        if budget - len(text) < 0:
            break
        budget -= len(text)
        who = "ENGINEER" if t["role"] == "user" else "AGENT"
        turns.append(f"[{who}]\n{text}")
    body = "\n\n".join(reversed(turns))
    return f"{body}\n\n[NEW MESSAGE FROM AGENT]\n{r['text']}"

out = open("expa.jsonl", "w")
def judge(item):
    lab, r = item
    row = {"label": lab, "file": r["file"], "line": r["line"]}
    try:
        row["score"] = ask(build_prompt(r))
    except Exception as e:
        row["error"] = str(e)[:120]
    out.write(json.dumps(row) + "\n")
    out.flush()
    return row

with ThreadPoolExecutor(max_workers=8) as ex:
    results = list(ex.map(judge, sample))
out.close()

def auc(pos, neg):
    wins = sum((p > n) + 0.5 * (p == n) for p in pos for n in neg)
    return wins / (len(pos) * len(neg)) if pos and neg else float("nan")

pos = [r["score"] for r in results if r["label"] == "complaint" and "score" in r]
neg = [r["score"] for r in results if r["label"] == "silent" and "score" in r]
failed = sum(1 for r in results if "error" in r)
print(f"haiku+ctx AUC {auc(pos, neg):.3f}  mean complaint {sum(pos)/len(pos):.2f}  "
      f"mean silent {sum(neg)/len(neg):.2f}  (n={len(pos)}/{len(neg)}, {failed} failed)")
for cut in (5, 6, 7, 8):
    fp = 100 * sum(s >= cut for s in neg) / len(neg)
    tp = 100 * sum(s >= cut for s in pos) / len(pos)
    print(f"  >={cut}  fires on {fp:5.1f}% of silent, {tp:5.1f}% of complaints")
