import { useCallback } from 'react';
import { useDaemonStore } from '../store/daemonSessions';

/**
 * Resolves an app view's declared title, for every surface that titles a tile.
 *
 * It lives here rather than inside `deriveTileTitle` because that module is pure
 * and is called from tests that hold no store — and it is one hook rather than a
 * lookup each caller writes because the sidebar and the tile header showing
 * different names for the same tile is exactly what a second copy produces.
 */
export function useAppViewTitleResolver(): (app: string, view: string) => string | undefined {
  const apps = useDaemonStore((state) => state.apps);
  return useCallback(
    (app: string, view: string) =>
      apps.find((a) => a.name === app)?.views?.find((v) => v.name === view)?.title,
    [apps],
  );
}
