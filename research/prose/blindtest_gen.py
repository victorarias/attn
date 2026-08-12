"""Builds the blind A/B page for test 3: 20 shipped pairs from exp2, sides
randomized per pair, no labels. The page records picks and produces a JSON
block to paste back; the mapping lives only in this script's seed."""
import html, json, random

rows = [json.loads(l) for l in open("exp2.jsonl")]
shipped = [r for r in rows if r.get("ships")]
rng = random.Random(13)
rng.shuffle(shipped)
pairs = shipped[:20]

cards = []
for i, r in enumerate(pairs):
    flip = rng.random() < 0.5  # flip: A=rewrite; else A=original
    a, b = (r["rewrite"], r["text"]) if flip else (r["text"], r["rewrite"])
    amap = "rewrite" if flip else "original"
    cards.append(f"""
<section class="pair" data-idx="{i}" data-a="{amap}">
  <h2>Pair {i + 1} <span class="pick" id="pick{i}"></span></h2>
  <div class="cols">
    <div class="col"><h3>A</h3><pre>{html.escape(a)}</pre>
      <button onclick="choose({i},'A')">A reads better</button></div>
    <div class="col"><h3>B</h3><pre>{html.escape(b)}</pre>
      <button onclick="choose({i},'B')">B reads better</button></div>
  </div>
  <button class="tie" onclick="choose({i},'tie')">No preference</button>
</section>""")

page = f"""<meta charset="utf-8">
<title>Blind test: which would you rather receive?</title>
<style>
  body {{ font: 15px/1.5 -apple-system, sans-serif; margin: 2rem auto; max-width: 1200px; padding: 0 1rem; background:#fff; color:#111; }}
  .pair {{ border-top: 2px solid #ddd; margin-top: 2rem; padding-top: 1rem; }}
  .cols {{ display: flex; gap: 1rem; }}
  .col {{ flex: 1; min-width: 0; }}
  pre {{ white-space: pre-wrap; background: #f6f6f6; padding: .8rem; border-radius: 6px; max-height: 340px; overflow-y: auto; font: 13px/1.45 ui-monospace, monospace; }}
  button {{ padding: .4rem .9rem; cursor: pointer; }}
  .tie {{ margin-top: .5rem; }}
  .pick {{ color: #0a7; font-size: .8em; margin-left: .6rem; }}
  #done {{ margin: 2rem 0; }}
  textarea {{ width: 100%; height: 6rem; font: 12px ui-monospace, monospace; }}
</style>
<h1>Which message would you rather receive?</h1>
<p>Same content, two versions, order randomized. Judge only how it reads.
Pick one per pair (or no preference), then copy the results block at the bottom
and paste it back to the session.</p>
{''.join(cards)}
<div id="done"><h2>Results</h2><textarea id="out" readonly></textarea>
<button onclick="navigator.clipboard.writeText(document.getElementById('out').value)">Copy</button></div>
<script>
const picks = {{}};
function choose(i, v) {{
  picks[i] = v;
  document.getElementById('pick' + i).textContent = '✓ ' + v;
  document.getElementById('out').value = JSON.stringify(picks);
}}
</script>
"""
with open("blindtest.html", "w") as f:
    f.write(page)
key = {i: ("rewrite" if c.split('data-a="')[1][:7] == "rewrite" else "original")
       for i, c in enumerate(cards)}
with open("blindtest_key.json", "w") as f:
    json.dump({"key": key, "pairs": [{"file": p["file"], "line": p["line"]} for p in pairs]}, f)
print("20 pairs; A=rewrite on", sum(1 for v in key.values() if v == "rewrite"))
