# The calibration corpus

Two labelled sets of the same authors writing about the same code, which is
what makes them comparable.

- `dense/` — comment prose as it stood before PR #818, the sweep that measured
  hand-written Go comments going from 6% to 17% of the diff and cut them back.
  Victor judged this prose too dense and deleted most of it.
- `swept/` — the same twelve files after that sweep. Victor wrote and kept
  these.

The third set is not here because it is already in the repository: the plan and
vision docs under `docs/plans` and `docs/vision`, the healthy set the
thresholds are set past.

## Regenerating

The twelve files are the ones the sweep cut hardest, taken from the two sweep
commits — `7e490418` ("compress comment prose to constraints and receipts",
#818) and `ea7113f4` ("sweep the frontend and the Go long tail", #820). For
each file the sweep touched, the comment groups of the parent and of the
commit were extracted with `go/parser` and written as paragraphs, dropping
groups under twelve words and anything holding code.

The sample is a sample: 36,623 dense words and 15,417 swept words out of
98,187 and 41,695 across all 82 touched files.
