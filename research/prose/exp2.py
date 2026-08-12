"""Test 2 for design C: the safety property, on data no prompt was tuned on.

100 silently-accepted messages -> opus rewrite under the /bro nudge ->
sonnet/low pairwise both orders (rewrite ships only on a double win) ->
real Structure.Lost via the repo's Go code -> fact-retention question on
every shipped rewrite. The claim under test: shipped >= original, always."""
import json, random, re, subprocess, time
from concurrent.futures import ThreadPoolExecutor

NUDGE = ("Restate your last message. Stop using jargon and speak coherently. "
         "State it more simply and concisely, like one human talking to another. "
         "Keep what carries information the prose cannot — diagrams, tables, "
         "code, commands, links. Improve them if you can; just don't drop them.")

REWRITE_SYSTEM = ("You are an AI coding agent. You just sent the engineer you work for "
                  "the message the user shows you. The engineer replied:\n\n"
                  f"\"{NUDGE}\"\n\n"
                  "Output only the restated message, nothing else.")

JUDGE_SYSTEM = """You will see two versions of a message an AI coding agent wrote to the engineer it works for, labelled A and B. Both say roughly the same thing.

The engineer just asked for plain language. Pick the version that explains it more plainly: simple words, one human talking to another, no jargon, no corporate filler, no hedging.

Judge only how it is written. A version is NOT better because it packs in more detail, more precise terminology, or more caveats — that is what he is complaining about. It IS worse if it drops a command, a number, a link, or a code block the other version carries.

Which version would he rather read?

Answer with the single letter A or B and nothing else."""

FACT_SYSTEM = """You will see an ORIGINAL message and a REWRITE of it.

Name one concrete thing stated in ORIGINAL that is missing from REWRITE: a fact, number, file path, command, link, code block, or decision. Quote it briefly.

If nothing material is missing, answer exactly NONE."""

def claude(model, effort, system, text, timeout=300):
    cmd = ["claude", "-p", "--strict-mcp-config", "--model", model,
           "--max-turns", "1", "--system-prompt", system]
    if effort:
        cmd += ["--effort", effort]
    out = subprocess.run(cmd, input=text, capture_output=True, text=True, timeout=timeout)
    if out.returncode != 0:
        raise RuntimeError(out.stderr.strip()[:120])
    return out.stdout.strip()

def verdict(a, b):
    r = claude("claude-sonnet-5", "low", JUDGE_SYSTEM, f"[VERSION A]\n{a}\n\n[VERSION B]\n{b}")
    m = re.search(r"\b([AB])\b", r)
    if not m:
        raise RuntimeError("no verdict in %r" % r[:60])
    return m.group(1)

rows = []
with open("silent.jsonl.handlabel") as f:
    for line in f:
        r = json.loads(line)
        if r["label"] != "silent":
            continue
        if len(re.findall(r"[a-zA-Z][a-zA-Z'-]*",
                          re.sub(r"```.*?```", " ", r["text"], flags=re.S))) < 150:
            continue
        rows.append(r)
random.Random(11).shuffle(rows)
sample = rows[:100]
print(f"pipeline over {len(sample)} silently-accepted messages", flush=True)

out = open("exp2.jsonl", "w")
def run(r):
    rec = {"file": r["file"], "line": r["line"]}
    try:
        t0 = time.time()
        rec["rewrite"] = claude("claude-opus-5", None, REWRITE_SYSTEM, r["text"])
        rec["rewrite_s"] = round(time.time() - t0, 1)
        t0 = time.time()
        fwd = verdict(r["text"], rec["rewrite"]) == "B"   # rewrite wins as B
        rev = verdict(rec["rewrite"], r["text"]) == "A"   # rewrite wins as A
        rec["verify_s"] = round(time.time() - t0, 1)
        rec["ships"] = fwd and rev
        if rec["ships"]:
            f = claude("claude-sonnet-5", "low", FACT_SYSTEM,
                       f"[ORIGINAL]\n{r['text']}\n\n[REWRITE]\n{rec['rewrite']}")
            rec["fact_loss"] = "" if f.strip().upper().startswith("NONE") else f[:200]
    except Exception as e:
        rec["error"] = str(e)[:120]
    rec["text"] = r["text"]
    out.write(json.dumps(rec) + "\n")
    out.flush()
    return rec

with ThreadPoolExecutor(max_workers=8) as ex:
    results = list(ex.map(run, sample))
out.close()

ok = [r for r in results if "error" not in r]
ships = [r for r in ok if r["ships"]]
losses = [r for r in ships if r.get("fact_loss")]
lat_r = sorted(r["rewrite_s"] for r in ok)
lat_v = sorted(r["verify_s"] for r in ok)
print(f"\ncompleted {len(ok)}/{len(results)}  ({len(results)-len(ok)} errors)")
print(f"rewrite ships: {len(ships)}/{len(ok)} ({100*len(ships)/max(len(ok),1):.0f}%)")
print(f"fact loss flagged on shipped: {len(losses)}/{len(ships)}")
print(f"rewrite p50 {lat_r[len(lat_r)//2]:.0f}s max {lat_r[-1]:.0f}s   "
      f"verify p50 {lat_v[len(lat_v)//2]:.0f}s max {lat_v[-1]:.0f}s")
