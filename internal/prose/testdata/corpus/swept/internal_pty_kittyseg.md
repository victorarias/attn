The worker's PTY feed segmenter — the ONE place that decides where a kitty
APC or OSC 133 marker begins and ends; meaning is read elsewhere ("observe,
never interpret", docs/plans/2026-08-02-terminal-kitty-images.md).

Invariant: for any input, split into any chunks, the emissions concatenate
back to the input byte for byte, in order — minus only a tail held for the
next Feed. Extraction removes bytes from one side, so it is safe only from
GROUND with a reached terminator: outside ground the sequence's leading ESC
also exits the open sequence, and taking it away silently diverges the two
grids. Everything else replays to both sides as plain.

Every transition here was MEASURED against the native terminal, not read off
the VT spec — ghostty's sets differ from the spec's — and
TestKittySegmenterGroundMatchesGhostty holds this machine's idea of ground
equal to ghostty's. Change a rule only with a measurement to point at.

kittyAPCIntroducer is ESC _ G — APC plus kitty graphics' protocol byte.
Ghostty identifies kitty on that G alone (src/terminal/apc.zig).

kittySegMaxPendingBytes bounds what one unterminated APC can buffer — a
tripwire, not a correctness cliff: past it the held bytes replay as plain to
both sides. It must sit ABOVE what ghostty accepts: ghostty's kitty APC cap
is 65 MiB (Protocol.defaultMaxBytes(.kitty), src/terminal/apc.zig at native
pin ab0b9da), over the post-`;` payload; 72 MiB clears that plus framing, so
tripping it means a payload ghostty already refused.

osc133MarkerMaxPendingBytes is the same tripwire for a held OSC 133 marker.
Receipt: the largest legitimate marker is a C marker carrying a
percent-encoded command line; ARG_MAX measured 1 MiB here (`getconf
ARG_MAX` = 1048576), encoding can triple a byte, so 3 MiB is the ceiling for
a runnable command — attn's own emitter was observed at 61 bytes. 16 MiB
clears it five times over.

kittySegMode is where ghostty's parser stands after everything fed so far.
Bytes are held only in the modes holding() names.

kittySegEscapeIntermediate: an escape that has taken an intermediate byte.
Measured: once one lands, the string introducers stop introducing.

kittySegOSC: inside ESC ] …, which ends on its own byte set.

kittySegOSC133Prefix: inside an OSC opened in GROUND that still matches
osc133Prefix. Holds only the undecided bytes; the first divergent byte
drops the hold, so a title write never stalls the feed.

kittySegOpaque: inside a DCS, SOS, PM or APC string — including a kitty
APC this segmenter has decided it cannot extract.

c1Executed reports the C1 bytes ghostty executes as controls, returning to
ground from escape, CSI, and every string state. Measured: exactly 80-8f,
91-97, 99-9a and 9c; the holes are the C1 introducers.

kittySegAborts reports the single bytes that end a string sequence short of
its terminator. Measured: CAN and SUB abort everywhere, plus every
c1Executed byte. BEL is deliberately absent — it ends only an OSC.

kittySegOpensInsideString reports the sequence a raw C1 introducer opens from
inside an open DCS, PM, APC or kitty string. Measured, and asymmetric from
the escape state: 90/9b/9d cut the string short and introduce their own;
98/9e/9f are payload. An OSC honours none of the six — hence its own mode.

kittySegOpensC1 reports the sequence a raw C1 introducer opens from escape or
CSI state, where all six introduce (measured). From GROUND they open
nothing: the stream is UTF-8, so they decode to U+FFFD and print.

kittySegOpens7Bit reports the sequence a byte opens from escape state — the
only mode where the 7-bit forms introduce anything.

feedSegKittyAPC: one complete kitty APC, introducer through terminator.
Terminal only; the wire carries synthesized layout bytes instead.

feedSegOSC133: one complete OSC 133 marker. Wire only; the block table
takes the parsed marker. OSC 133 produces no cells, so the grids agree.

feedSegment is one emission. Bytes is never empty and is valid only for the
duration of its callback — it aliases a buffer the next call reuses.

Marker is what a feedSegOSC133 emission means — nil for a subtype no
block event is defined for (bytes still consumed).

feedSegmenter splits the PTY byte stream into the three dispositions above,
carrying a partial sequence and the parser mode across Feed calls.

resume is how far into pending the scan has already looked. Meaningful
only in holding modes.

holding reports whether the mode buffers its bytes rather than emitting them.

abandoned is where the parser stands once a held sequence is given up on at
the tripwire: both ends are still inside it.

Fast path: an ESC-free chunk in ground with nothing pending passes the input
slice through — no copy, no allocation. Ground is part of the condition
because it is the only mode an ESC-free chunk cannot move (measured).

Cost is amortized O(len(chunk)) even while a sequence stays open across many
calls: only new bytes are scanned, or the walk to the 72 MiB tripwire goes
quadratic.

carried says whether buffer aliases s.pending, which decides whether
holding bytes back at the end costs a copy.

holdStart is where the held extractable sequence began, or -1 for none.

Deciding what this ESC introduces needs one byte after it, two
for a kitty APC. Hold when they have not arrived: no prefix of a
removed sequence may reach the far side ahead of the removal.

Measured: ESC ESC restarts the escape and drops collected
intermediates — not ground.

A final byte. Measured: from a bare escape, 30-4f, 51-57,
59-5a, 5c and 60-7e return to ground; after an intermediate
all of 30-7e does.

A final byte. Measured: CSI returns to ground on all of
40-7e — the 7-bit letters open nothing here.

Measured: an OSC ends on BEL, CAN and SUB and on NOTHING
else — C1 ST does not end one, and a raw C1 introducer
inside is payload. The opposite of the opaque strings.

Not a marker: drop the hold so the bytes stay in the plain run,
and read this byte again as OSC payload.

Measured: a stray ESC makes ghostty DISPATCH the marker, so
cutting would be framing-safe — but the client's parser
(terminalOsc133.ts) knows only BEL and ST, and stripping a
marker it would not recognise splits the two block tables.

Measured: CAN and SUB also DISPATCH the marker and leave
ground. Same disposal, same reason.

The ESC ends the APC for ghostty and opens a new escape.
Extracting would take that exit off the wire, so the whole
abandoned APC replays to both sides as plain.

Measured: C1 ST terminates a kitty APC exactly as ESC \ does.

The aborting byte has its own grid effect (IND scrolls) that
synthesis cannot observe; replay as plain so both parsers
cut in the same place.

emitMarker reports one complete OSC 133 marker: raw is its FULL bytes,
introducer through terminator. Only the two extracting terminators may reach
here — BEL, or two-byte ST — a third added without widening this would index
backwards past the introducer.

hold keeps buffer[from:] for the next Feed, resumeAt being the absolute
index the body scan continues from (pass from when the bytes are not an open
sequence). Keeping one already at the front of its own growing buffer costs
nothing — that is what makes a long transmission linear.

Everything else held here is small; copying into a fresh slice releases
whatever capacity a just-finished APC had grown to.

release drops the buffer instead of keeping its capacity: a finished APC may
have grown to megabytes, held otherwise for the session's whole life. The
parser mode is untouched.
