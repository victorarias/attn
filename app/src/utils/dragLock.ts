// Locks text selection across the whole document for the duration of a pointer
// drag. WebKit (the engine Tauri uses on macOS) does not honor preventDefault on
// pointerdown for selection purposes, so a divider/header drag still paints a
// text selection across whatever it passes over. Disabling user-select on <body>
// and swallowing `selectstart` is the reliable cross-engine fix.
//
// Returns a release() that restores the prior state. Call it on pointerup.
type BodyStyle = CSSStyleDeclaration & { webkitUserSelect?: string };

export function lockTextSelection(cursor?: string): () => void {
  const body = document.body;
  const style = body.style as BodyStyle;
  const prevUserSelect = style.userSelect;
  const prevWebkitUserSelect = style.webkitUserSelect ?? '';
  const prevCursor = style.cursor;

  style.userSelect = 'none';
  style.webkitUserSelect = 'none';
  if (cursor) {
    style.cursor = cursor;
  }

  const swallow = (event: Event) => event.preventDefault();
  document.addEventListener('selectstart', swallow);

  return () => {
    document.removeEventListener('selectstart', swallow);
    style.userSelect = prevUserSelect;
    style.webkitUserSelect = prevWebkitUserSelect;
    style.cursor = prevCursor;
  };
}
