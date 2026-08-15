/**
 * How a crew member's id is written wherever a person reads it: Trellis, Keel,
 * Alder. Display capitalizes, identity does not — the id stays lowercase in
 * paths, CLI arguments, protocol fields and test ids, and this is the one place
 * the two part ways.
 *
 * The daemon keeps the same rule in `internal/crew.DisplayName`; a member woken
 * by the daemon already arrives with a capitalized session label, and this is
 * what names a member the app draws without one — a sleeping row.
 *
 * The first character only. Ids may carry `-`, but no real member is two words,
 * and title-casing one would invent a name nobody chose.
 */
export function crewDisplayName(id: string): string {
  const trimmed = id.trim();
  if (!trimmed) return '';
  const [first] = trimmed;
  return first.toUpperCase() + trimmed.slice(first.length);
}

/**
 * How something's holder is written when it can be either a member or a bare
 * session: the member reads as the name it is, and a session id is left exactly
 * as it is rather than dressed up as one. The daemon keeps the same rule in
 * `internal/crew.HolderName`.
 */
export function crewHolderName(member: string | undefined, session: string | undefined): string {
  return member?.trim() ? crewDisplayName(member) : session?.trim() ?? '';
}
