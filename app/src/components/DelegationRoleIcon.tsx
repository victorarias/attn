export const DELEGATION_ICONS = ['search', 'diamond', 'code', 'arrow', 'list', 'bug', 'spark', 'circle'] as const;

export function DelegationRoleIcon({ icon, name }: { icon: string; name: string }) {
  const paths: Record<string, React.ReactNode> = {
    search: <><circle cx="7" cy="7" r="4" /><path d="m10 10 4 4" /></>,
    diamond: <path d="m8 1 7 7-7 7-7-7Z" />,
    code: <><path d="m5 4-4 4 4 4m6-8 4 4-4 4M9 2 7 14" /></>,
    arrow: <path d="M3 13 13 3M4 3h9v9" />,
    list: <path d="M6 4h8M6 8h8M6 12h8M2 4h1M2 8h1M2 12h1" />,
    bug: <><rect x="4" y="5" width="8" height="9" rx="4" /><path d="m5 2 2 3m4-3-2 3M1 7h3m8 0h3M1 11h3m8 0h3M8 6v7" /></>,
    spark: <path d="m8 1 2 5 5 2-5 2-2 5-2-5-5-2 5-2Z" />,
    circle: <circle cx="8" cy="8" r="6" />,
  };
  if (!paths[icon]) return <span aria-hidden="true">{name.trim().slice(0, 1).toUpperCase() || '○'}</span>;
  return <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{paths[icon]}</svg>;
}
