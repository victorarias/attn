import type { ComponentType } from 'react';

/** What a view is given. Mirrors ViewProps in sdk/attn-app/src/index.ts. */
export interface AppViewProps {
  workspaceId: string;
  sessionId: string | null;
  tileId: string;
  params: string;
}

export type AppViewComponent = ComponentType<AppViewProps>;

// How long a bundle fetch may hang before the tile says so.
//
// The tripwire is the fetch, not the network: the daemon serving it is on this
// machine, and every artifact it serves is a file it already has on disk. 10s is
// borrowed from A4's appRuntimeConnectWait — the same shape of wait against the
// same daemon — and is far past anything a local read does. A view that trips it
// is a daemon that is gone or wedged, which is what the message says.
export const APP_VIEW_LOAD_TIMEOUT_MS = 10_000;

export class AppViewLoadError extends Error {
  readonly detail: string;
  constructor(message: string, detail: string) {
    super(message);
    this.name = 'AppViewLoadError';
    this.detail = detail;
  }
}

/**
 * Imports a built view and returns its component.
 *
 * A plain dynamic import of the daemon's URL, on purpose. The module is an ES
 * module whose SDK imports are bare specifiers, and only a real import resolves
 * those against index.html's import map — which is the whole one-React design.
 *
 * The import cannot be cancelled, so the timeout races it rather than aborting
 * it: a late resolution is dropped, and the tile has already said what happened.
 */
export async function loadAppView(url: string, timeoutMs = APP_VIEW_LOAD_TIMEOUT_MS): Promise<AppViewComponent> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const module = await Promise.race([
    import(/* @vite-ignore */ url).catch((error: unknown) => {
      throw new AppViewLoadError(
        'This view could not be loaded.',
        `Importing ${url} failed: ${errorText(error)}`,
      );
    }),
    new Promise<never>((_, reject) => {
      timer = setTimeout(() => {
        reject(new AppViewLoadError(
          'This view did not load.',
          `${url} did not answer within ${timeoutMs / 1000}s. The daemon serving it may be down — check \`attn daemon status\`.`,
        ));
      }, timeoutMs);
    }),
  ]).finally(() => {
    if (timer) clearTimeout(timer);
  });

  const component = (module as { default?: unknown }).default;
  if (typeof component !== 'function') {
    // The binding, by name: an author who exported the component under its own
    // name rather than as the default gets told exactly that, and the list of
    // what the module did export is the shortest way to see it.
    const exported = Object.keys(module as object).filter((k) => k !== 'default');
    throw new AppViewLoadError(
      'This view exports no component.',
      `${url} must export a React component as its default export. `
      + (exported.length > 0 ? `It exports: ${exported.join(', ')}.` : 'It exports nothing.'),
    );
  }
  return component as AppViewComponent;
}

export function errorText(error: unknown): string {
  if (error instanceof Error) return error.stack || `${error.name}: ${error.message}`;
  return String(error);
}
