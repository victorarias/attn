// Generated names for new worktrees.
//
// The new-session flow has no task description to derive a meaningful branch
// name from, so a name has to be invented to make zero-input creation possible.
// Adjective-noun pairs are used instead of an opaque code because these names
// end up in `git branch`, in sibling worktree directories, and on pull request
// branches, where something pronounceable is easier to recognize and talk about
// than a timestamp or hash.

const ADJECTIVES = [
  'amber', 'bashful', 'brisk', 'chaotic', 'chipper', 'cosmic', 'cranky',
  'crispy', 'dapper', 'dizzy', 'drowsy', 'feral', 'fluffy', 'frosty', 'fussy',
  'gloomy', 'grumpy', 'hasty', 'jaunty', 'jolly', 'lanky', 'lucky', 'moody',
  'nimble', 'peppy', 'plucky', 'prickly', 'quirky', 'rowdy', 'rugged', 'rustic',
  'salty', 'scruffy', 'sleepy', 'smug', 'snappy', 'sneaky', 'soggy', 'spicy',
  'sturdy', 'sulky', 'sunny', 'surly', 'tipsy', 'unruly', 'velvet', 'wary',
  'whimsy', 'wily', 'zesty',
];

const NOUNS = [
  'alpaca', 'axolotl', 'badger', 'beetle', 'bison', 'capybara', 'chinchilla',
  'dingo', 'ferret', 'gecko', 'gerbil', 'gopher', 'hedgehog', 'heron', 'ibex',
  'iguana', 'kestrel', 'lemur', 'llama', 'magpie', 'manatee', 'marmot',
  'meerkat', 'mongoose', 'moose', 'narwhal', 'newt', 'ocelot', 'opossum',
  'otter', 'pangolin', 'pelican', 'pigeon', 'platypus', 'puffin', 'quokka',
  'raccoon', 'seal', 'tapir', 'toucan', 'walrus', 'weasel', 'wombat', 'yak',
];

const pick = <T,>(items: readonly T[], random: () => number): T =>
  items[Math.floor(random() * items.length) % items.length];

/**
 * Returns an unused adjective-noun name such as `grumpy-otter`.
 *
 * `taken` should carry every name that would collide — existing worktree
 * branches and the repo's current branch — so a generated name never lands on
 * a create that git will reject. When the random draws keep colliding, a
 * numeric suffix guarantees termination rather than looping until a free pair
 * happens to come up.
 */
export function generateWorktreeName(
  taken: Iterable<string> = [],
  random: () => number = Math.random,
): string {
  const used = new Set(taken);

  for (let attempt = 0; attempt < 50; attempt++) {
    const name = `${pick(ADJECTIVES, random)}-${pick(NOUNS, random)}`;
    if (!used.has(name)) {
      return name;
    }
  }

  const base = `${pick(ADJECTIVES, random)}-${pick(NOUNS, random)}`;
  for (let suffix = 2; ; suffix++) {
    const name = `${base}-${suffix}`;
    if (!used.has(name)) {
      return name;
    }
  }
}
