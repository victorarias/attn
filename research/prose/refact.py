import json, subprocess
from concurrent.futures import ThreadPoolExecutor
FACT_SYSTEM = """You will see an ORIGINAL message and a REWRITE of it.

Does the REWRITE drop a concrete fact the ORIGINAL states — a number, measurement, file path, line reference, command, link, code block, decision, or stated caveat?

Answer with a single line and nothing else: NONE if nothing material is missing, otherwise a short quote of one missing item. No reasoning, no hedging, no "actually"."""
def check(r):
    out = subprocess.run(["claude","-p","--strict-mcp-config","--model","claude-sonnet-5",
        "--effort","low","--max-turns","1","--system-prompt",FACT_SYSTEM],
        input=f"[ORIGINAL]\n{r['text']}\n\n[REWRITE]\n{r['rewrite']}",
        capture_output=True, text=True, timeout=300)
    v = out.stdout.strip()
    return "" if v.upper().startswith("NONE") else v[:200]
rows = [json.loads(l) for l in open("exp2.jsonl") if json.loads(l).get("ships")]
with ThreadPoolExecutor(max_workers=8) as ex:
    flags = list(ex.map(check, rows))
losses = [f for f in flags if f]
print(f"old nudge, tight checker: {len(losses)}/{len(rows)} flagged")
json.dump(flags, open("refact_old.json","w"))
