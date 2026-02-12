const SESSION_EMOJIS = [
  '🛠️', '🚀', '🧪', '🔭', '🧠', '🛰️', '🧩', '🧵', '🔧', '🗺️',
  '📡', '🧭', '⚙️', '📦', '🧬', '🪄', '🦾', '🧱', '🧯', '🪛',
  '🪜', '🪐', '🌈', '🔥', '🌊', '🌪️', '☄️', '🌟', '🍀', '🌻',
  '🦊', '🦉', '🦄', '🐙', '🐝', '🦖', '🐢', '🦜', '🦩', '🐬',
  '🎯', '🎲', '🎨', '🎸', '🎹', '🎮', '🎬', '📚', '📝', '🗞️',
  '🧶', '🪡', '🪢', '💡', '🔮', '🧿', '🗿', '🪁', '🏁', '📍',
  '🕹️', '⌛', '⏱️', '🌋', '🗻', '🏝️', '🏔️', '🛶', '🚲', '🛼',
];

function fnv1aHash(input: string): number {
  let hash = 0x811c9dc5;
  for (let i = 0; i < input.length; i += 1) {
    hash ^= input.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return hash >>> 0;
}

export function pickSessionEmoji(seed: string): string {
  if (!seed) {
    return SESSION_EMOJIS[0];
  }
  const idx = fnv1aHash(seed) % SESSION_EMOJIS.length;
  return SESSION_EMOJIS[idx];
}

