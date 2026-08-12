"""Joins exp5 rewrites with the Go structure verdicts and reports the matrix."""
import json

rows = [json.loads(l) for l in open("exp5.jsonl")]
scored = [r for r in rows if "rewrite" in r]
lost = [json.loads(l)["lost"] for l in open("struct_out5.jsonl")]
assert len(lost) == len(scored), f"{len(lost)} verdicts vs {len(scored)} rewrites"
for r, l in zip(scored, lost):
    r["structure_lost"] = l

by = {}
for r in rows:
    by.setdefault(r["model"], []).append(r)

print(f"{'model':11}{'ships':>8}{'plainer':>9}{'fact ok':>9}{'struct ok':>11}"
      f"{'p50':>7}{'max':>7}{'err':>5}")
for name, rs in by.items():
    n = len(rs)
    ok = [r for r in rs if "rewrite" in r]
    errs = n - len(ok)
    plainer = sum(r.get("plainer", False) for r in ok)
    factok = sum(not r.get("fact_loss") for r in ok)
    structok = sum(not r.get("structure_lost") for r in ok)
    ships = sum(bool(r.get("plainer")) and not r.get("fact_loss") and not r.get("structure_lost")
                for r in ok)
    lat = sorted(r["rewrite_s"] for r in ok)
    p50 = lat[len(lat) // 2] if lat else 0
    print(f"{name:11}{ships:>4}/{n:<3}{plainer:>6}/{len(ok):<2}{factok:>6}/{len(ok):<2}"
          f"{structok:>8}/{len(ok):<2}{p50:>6.0f}s{lat[-1] if lat else 0:>6.0f}s{errs:>5}")

print("\nerrors:")
seen = set()
for r in rows:
    if "error" in r and (r["model"], r["error"][:40]) not in seen:
        seen.add((r["model"], r["error"][:40]))
        print(f"  {r['model']}: {r['error'][:110]}")
