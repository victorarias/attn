"""Mines real ticket descriptions and comments — the input for exp4 and exp5.

Ticket text is the hard case for a plainness rewrite: short, dense with
identifiers, and full of receipts a rewrite must not drop. It is why exp4 found
37% receipt loss where chat text found 10%.

This reads a *copy* of the attn database. Never point it at ~/.attn: production
is read-only to agents, and a live daemon is writing to it. Copy first:

    cp ~/.attn/attn.db /tmp/attn-copy.db
    python3 mine_tickets.py /tmp/attn-copy.db > tickets.jsonl

Writes one JSON object per line: {"kind": "description"|"comment", "text": ...}.
exp4.py and exp5.py expect it at ../ticketreg/tickets.jsonl by default.
"""
import json
import re
import sqlite3
import sys

# The same floor the experiments use. Below it the per-100-word rates a
# plainness measure reports are one sentence wide.
MIN_WORDS = 100

QUERIES = (
    ("description", "SELECT description FROM tickets WHERE description != ''"),
    ("comment", "SELECT comment FROM ticket_events "
                "WHERE kind = 'commented' AND comment != ''"),
)


def words(s):
    return len(re.findall(r"[a-zA-Z][a-zA-Z'-]*", re.sub(r"```.*?```", " ", s, flags=re.S)))


def main():
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    path = sys.argv[1]
    if path.rstrip("/").endswith(".attn/attn.db"):
        sys.exit("refusing to read the production database; copy it first")

    db = sqlite3.connect(f"file:{path}?mode=ro", uri=True)
    kept = seen = 0
    for kind, sql in QUERIES:
        for (text,) in db.execute(sql):
            seen += 1
            if words(text) >= MIN_WORDS:
                kept += 1
                print(json.dumps({"kind": kind, "text": text}))
    print(f"{seen} ticket texts, {kept} at or above {MIN_WORDS} words",
          file=sys.stderr)


if __name__ == "__main__":
    main()
