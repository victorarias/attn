export function toDisplayPath(path: string, homePath: string): string {
  if (!path) {
    return '';
  }
  if (homePath && path.startsWith(homePath + '/')) {
    return '~' + path.slice(homePath.length);
  }
  if (homePath && path === homePath) {
    return '~';
  }
  return path;
}

export function expandDisplayPath(path: string, homePath: string): string {
  if (!path || !homePath || !path.startsWith('~')) {
    return path;
  }
  if (path === '~') {
    return homePath;
  }
  if (path.startsWith('~/')) {
    return homePath + path.slice(1);
  }
  return homePath + '/' + path.slice(1);
}

export function normalizePickerPath(path: string, homePath: string): string {
  const expanded = expandDisplayPath(path.trim(), homePath);
  if (expanded === '/' || expanded === homePath) {
    return expanded;
  }
  return expanded.replace(/\/+$/, '');
}

export function buildInitialPickerInput(path: string | undefined, homePath: string): string {
  if (!path) {
    return '';
  }
  const displayPath = homePath ? toDisplayPath(path, homePath) : path;
  return displayPath.endsWith('/') ? displayPath : `${displayPath}/`;
}
