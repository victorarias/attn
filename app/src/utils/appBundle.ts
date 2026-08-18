import { resolveDaemonHTTPOrigin } from './daemonEndpoint';

// Where a built app view's module lives.
//
// The mirror of AppBundleURLPath in internal/daemon/app_bundle.go, which is what
// serves it. The path carries the version's content hash, so it names one exact
// artifact forever: a new version is a different URL rather than a cache to
// bust, and that is also how a docked tile learns its bundle moved.
export const APP_BUNDLE_ROUTE_PREFIX = '/apps/bundle/';

/** The tile kind one of an app's views docks as: `app:<app>/<view>`. */
export const APP_VIEW_TILE_KIND_PREFIX = 'app:';

/**
 * `attempt` is what makes Retry able to work at all. The browser's module map
 * caches a module's evaluation result by URL for the lifetime of the page, so a
 * bundle that threw at the top level or exported no component would return that
 * same failure forever at the same URL, however many times the button is
 * clicked. A retry asks for a distinct URL naming the same immutable artifact.
 */
export function appBundleURL(app: string, contentHash: string, view: string, attempt = 0): string {
  const url = `${resolveDaemonHTTPOrigin()}${APP_BUNDLE_ROUTE_PREFIX}${app}/${contentHash}/${view}.js`;
  return attempt > 0 ? `${url}?retry=${attempt}` : url;
}

export function appViewTileKind(app: string, view: string): string {
  return `${APP_VIEW_TILE_KIND_PREFIX}${app}/${view}`;
}

/**
 * Splits an `app:<app>/<view>` tile kind, or returns null for any other kind.
 * Both segments are validated names on the way in, so the string has exactly one
 * `/` and parses by splitting on the first one.
 */
export function parseAppViewTileKind(tileKind: string): { app: string; view: string } | null {
  if (!tileKind.startsWith(APP_VIEW_TILE_KIND_PREFIX)) return null;
  const rest = tileKind.slice(APP_VIEW_TILE_KIND_PREFIX.length);
  const slash = rest.indexOf('/');
  if (slash <= 0 || slash === rest.length - 1) return null;
  return { app: rest.slice(0, slash), view: rest.slice(slash + 1) };
}
