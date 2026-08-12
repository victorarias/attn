"""Experiment B: does the message leaning on terms the reader never saw
predict a writing complaint?

Joins the context mine with the hand-labelled corpus by (file, line), then
scores each message by how much of it is "novel" relative to the visible
conversation before it. AUC per feature, complaints vs silent."""
import json, re, sys
from collections import Counter

word_re = re.compile(r"[a-zA-Z][a-zA-Z'-]*")
tick_re = re.compile(r"`([^`\n]+)`")
fence_re = re.compile(r"```.*?```", re.S)

def words(s):
    return [w.lower() for w in word_re.findall(s)]

def prose(s):
    return fence_re.sub(" ", s)

def auc(pos, neg):
    if not pos or not neg:
        return float("nan")
    wins = ties = 0
    sneg = sorted(neg)
    import bisect
    for p in pos:
        lo = bisect.bisect_left(sneg, p)
        hi = bisect.bisect_right(sneg, p)
        wins += lo
        ties += hi - lo
    return (wins + ties / 2) / (len(pos) * len(neg))

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
        if lab is None:
            continue  # row not in the cleaned population
        ctx_text = "\n".join(t["text"] for t in r["context"])
        ctx_words = set(words(ctx_text))
        n_ctx_turns = len(r["context"])
        msg = r["text"]
        mw = words(prose(msg))
        if len(mw) < 150:
            continue  # same abstention floor as every previous experiment
        uniq = set(mw)
        content = {w for w in uniq if len(w) >= 5}
        long8 = {w for w in uniq if len(w) >= 8}
        ticks = {t.strip().lower() for t in tick_re.findall(prose(msg))}
        ctx_ticks = {t.strip().lower() for t in tick_re.findall(ctx_text)}

        def frac(new, base):
            return len(new - base) / len(new) if new else 0.0

        rows.append({
            "label": lab,
            "ctx_turns": n_ctx_turns,
            "f": {
                "novel_content_frac": frac(content, ctx_words),
                "novel_long8_frac": frac(long8, ctx_words),
                "novel_content_per100w": 100 * len(content - ctx_words) / len(mw),
                "novel_ticks_frac": frac(ticks, ctx_ticks | ctx_words),
                "novel_ticks_count": len(ticks - ctx_ticks - ctx_words),
                "ctx_turns": float(n_ctx_turns),
            },
        })

# Records with no visible context score novel=1.0 vacuously; require some.
judged = [r for r in rows if r["ctx_turns"] >= 2]
pos = [r for r in judged if r["label"] == "complaint"]
neg = [r for r in judged if r["label"] == "silent"]
print(f"joined={len(rows)} with-context={len(judged)} complaint={len(pos)} silent={len(neg)}\n")
print(f"{'feature':24}{'AUC':>7}{'mean cmpl':>11}{'mean silent':>13}")
for name in judged[0]["f"]:
    p = [r["f"][name] for r in pos]
    n = [r["f"][name] for r in neg]
    print(f"{name:24}{auc(p, n):7.3f}{sum(p)/len(p):11.3f}{sum(n)/len(n):13.3f}")
