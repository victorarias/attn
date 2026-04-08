import type { TerminalPerfSnapshot } from './terminalPerf';
import { isSuspiciousTerminalSize } from './terminalDebug';

export interface RenderHealthBox {
  width: number;
  height: number;
}

export interface PaneRenderHealthWarning {
  code: string;
  severity: 'warning' | 'error';
  message: string;
}

export interface PaneRenderHealthInput {
  paneId: string;
  kind: 'main' | 'shell';
  active: boolean;
  inputFocused: boolean;
  size: { cols: number; rows: number } | null;
  paneBounds: RenderHealthBox | null;
  projectedBounds: RenderHealthBox | null;
  paneBodyBounds: RenderHealthBox | null;
  terminalContainerBounds: RenderHealthBox | null;
  xtermScreenBounds: RenderHealthBox | null;
  canvasBounds: RenderHealthBox | null;
  helperTextarea: {
    focused: boolean;
    disabled: boolean;
    readOnly: boolean;
    width: number | null;
    height: number | null;
  } | null;
  terminal: Pick<
    TerminalPerfSnapshot,
    | 'terminalName'
    | 'sessionId'
    | 'paneId'
    | 'runtimeId'
    | 'renderer'
    | 'visible'
    | 'ready'
    | 'writeQueueChunks'
    | 'writeQueueBytes'
    | 'renderCount'
    | 'writeParsedCount'
    | 'lastRenderAt'
    | 'lastWriteParsedAt'
    | 'lastResize'
  > | null;
}

export interface PaneRenderHealth {
  paneId: string;
  kind: 'main' | 'shell';
  active: boolean;
  inputFocused: boolean;
  size: { cols: number; rows: number } | null;
  warnings: PaneRenderHealthWarning[];
  fill: {
    terminalContainerVsPaneBody: { width: number | null; height: number | null };
    xtermScreenVsPaneBody: { width: number | null; height: number | null };
    canvasVsPaneBody: { width: number | null; height: number | null };
  };
  deltas: {
    paneVsProjected: { width: number | null; height: number | null };
  };
  flags: {
    suspiciousTerminalSize: boolean;
    activePaneInputUnfocused: boolean;
    activePaneHelperDisabled: boolean;
    terminalVisible: boolean | null;
    terminalReady: boolean | null;
  };
  terminal: PaneRenderHealthInput['terminal'];
}

export interface SessionRenderHealthInput {
  sessionId: string;
  label: string;
  activePaneId: string;
  selected: boolean;
  panes: PaneRenderHealthInput[];
}

export interface SessionRenderHealth {
  sessionId: string;
  label: string;
  activePaneId: string;
  selected: boolean;
  panes: PaneRenderHealth[];
  summary: {
    paneCount: number;
    warningPaneCount: number;
    errorPaneCount: number;
    unhealthyPaneIds: string[];
  };
}

function safeRatio(numerator: number | null | undefined, denominator: number | null | undefined): number | null {
  if (
    typeof numerator !== 'number' ||
    typeof denominator !== 'number' ||
    !Number.isFinite(numerator) ||
    !Number.isFinite(denominator) ||
    denominator <= 0
  ) {
    return null;
  }
  return numerator / denominator;
}

function ratioSeverity(ratio: number | null, warningBelow = 0.85, errorBelow = 0.6): 'warning' | 'error' | null {
  if (ratio === null) {
    return null;
  }
  if (ratio < errorBelow) {
    return 'error';
  }
  if (ratio < warningBelow) {
    return 'warning';
  }
  return null;
}

function projectedDeltaSeverity(delta: number | null, projected: number | null): 'warning' | 'error' | null {
  if (
    delta === null ||
    projected === null ||
    !Number.isFinite(delta) ||
    !Number.isFinite(projected) ||
    projected <= 0
  ) {
    return null;
  }
  const absDelta = Math.abs(delta);
  const warningThreshold = Math.max(16, projected * 0.05);
  const errorThreshold = Math.max(48, projected * 0.15);
  if (absDelta >= errorThreshold) {
    return 'error';
  }
  if (absDelta >= warningThreshold) {
    return 'warning';
  }
  return null;
}

function addWarning(
  warnings: PaneRenderHealthWarning[],
  code: string,
  severity: 'warning' | 'error' | null,
  message: string,
) {
  if (!severity) {
    return;
  }
  warnings.push({ code, severity, message });
}

export function buildPaneRenderHealth(input: PaneRenderHealthInput): PaneRenderHealth {
  const warnings: PaneRenderHealthWarning[] = [];

  const paneWidth = input.paneBounds?.width ?? null;
  const paneHeight = input.paneBounds?.height ?? null;
  const projectedWidth = input.projectedBounds?.width ?? null;
  const projectedHeight = input.projectedBounds?.height ?? null;
  const paneBodyWidth = input.paneBodyBounds?.width ?? null;
  const paneBodyHeight = input.paneBodyBounds?.height ?? null;
  const terminalContainerWidth = input.terminalContainerBounds?.width ?? null;
  const terminalContainerHeight = input.terminalContainerBounds?.height ?? null;
  const xtermScreenWidth = input.xtermScreenBounds?.width ?? null;
  const xtermScreenHeight = input.xtermScreenBounds?.height ?? null;
  const canvasWidth = input.canvasBounds?.width ?? null;
  const canvasHeight = input.canvasBounds?.height ?? null;

  const fill = {
    terminalContainerVsPaneBody: {
      width: safeRatio(terminalContainerWidth, paneBodyWidth),
      height: safeRatio(terminalContainerHeight, paneBodyHeight),
    },
    xtermScreenVsPaneBody: {
      width: safeRatio(xtermScreenWidth, paneBodyWidth),
      height: safeRatio(xtermScreenHeight, paneBodyHeight),
    },
    canvasVsPaneBody: {
      width: safeRatio(canvasWidth, paneBodyWidth),
      height: safeRatio(canvasHeight, paneBodyHeight),
    },
  };

  const deltas = {
    paneVsProjected: {
      width: paneWidth !== null && projectedWidth !== null ? paneWidth - projectedWidth : null,
      height: paneHeight !== null && projectedHeight !== null ? paneHeight - projectedHeight : null,
    },
  };

  addWarning(
    warnings,
    'projected_width_mismatch',
    projectedDeltaSeverity(deltas.paneVsProjected.width, projectedWidth),
    `Pane width differs from projected layout width by ${Math.abs(Math.round(deltas.paneVsProjected.width || 0))}px.`,
  );
  addWarning(
    warnings,
    'projected_height_mismatch',
    projectedDeltaSeverity(deltas.paneVsProjected.height, projectedHeight),
    `Pane height differs from projected layout height by ${Math.abs(Math.round(deltas.paneVsProjected.height || 0))}px.`,
  );

  addWarning(
    warnings,
    'terminal_container_underfills_width',
    ratioSeverity(fill.terminalContainerVsPaneBody.width),
    `Terminal container uses only ${Math.round((fill.terminalContainerVsPaneBody.width || 0) * 100)}% of pane-body width.`,
  );
  addWarning(
    warnings,
    'terminal_container_underfills_height',
    ratioSeverity(fill.terminalContainerVsPaneBody.height),
    `Terminal container uses only ${Math.round((fill.terminalContainerVsPaneBody.height || 0) * 100)}% of pane-body height.`,
  );
  addWarning(
    warnings,
    'xterm_screen_underfills_width',
    ratioSeverity(fill.xtermScreenVsPaneBody.width),
    `xterm screen uses only ${Math.round((fill.xtermScreenVsPaneBody.width || 0) * 100)}% of pane-body width.`,
  );
  addWarning(
    warnings,
    'xterm_screen_underfills_height',
    ratioSeverity(fill.xtermScreenVsPaneBody.height),
    `xterm screen uses only ${Math.round((fill.xtermScreenVsPaneBody.height || 0) * 100)}% of pane-body height.`,
  );
  addWarning(
    warnings,
    'canvas_underfills_width',
    ratioSeverity(fill.canvasVsPaneBody.width),
    `Terminal canvas uses only ${Math.round((fill.canvasVsPaneBody.width || 0) * 100)}% of pane-body width.`,
  );
  addWarning(
    warnings,
    'canvas_underfills_height',
    ratioSeverity(fill.canvasVsPaneBody.height),
    `Terminal canvas uses only ${Math.round((fill.canvasVsPaneBody.height || 0) * 100)}% of pane-body height.`,
  );

  const suspiciousTerminalSize = Boolean(input.size && isSuspiciousTerminalSize(input.size.cols, input.size.rows));
  if (suspiciousTerminalSize) {
    warnings.push({
      code: 'suspicious_terminal_size',
      severity: 'warning',
      message: `Terminal size ${input.size?.cols}x${input.size?.rows} is below the suspicious threshold.`,
    });
  }

  const activePaneInputUnfocused = input.active && !input.inputFocused;
  if (activePaneInputUnfocused) {
    warnings.push({
      code: 'active_pane_input_unfocused',
      severity: 'warning',
      message: 'Active pane does not own the xterm helper textarea focus.',
    });
  }

  const activePaneHelperDisabled = Boolean(
    input.active &&
    input.helperTextarea &&
    (input.helperTextarea.disabled || input.helperTextarea.readOnly),
  );
  if (activePaneHelperDisabled) {
    warnings.push({
      code: 'active_pane_helper_disabled',
      severity: 'warning',
      message: 'Active pane helper textarea is disabled or read-only.',
    });
  }

  if (input.active && input.terminal && !input.terminal.ready) {
    warnings.push({
      code: 'active_terminal_not_ready',
      severity: 'warning',
      message: 'Active pane terminal is mounted but not marked ready.',
    });
  }
  if (input.active && input.terminal && !input.terminal.visible) {
    warnings.push({
      code: 'active_terminal_not_visible',
      severity: 'warning',
      message: 'Active pane terminal is mounted but not visible.',
    });
  }

  return {
    paneId: input.paneId,
    kind: input.kind,
    active: input.active,
    inputFocused: input.inputFocused,
    size: input.size,
    warnings,
    fill,
    deltas,
    flags: {
      suspiciousTerminalSize,
      activePaneInputUnfocused,
      activePaneHelperDisabled,
      terminalVisible: input.terminal?.visible ?? null,
      terminalReady: input.terminal?.ready ?? null,
    },
    terminal: input.terminal,
  };
}

export function buildSessionRenderHealth(input: SessionRenderHealthInput): SessionRenderHealth {
  const panes = input.panes.map((pane) => buildPaneRenderHealth(pane));
  let warningPaneCount = 0;
  let errorPaneCount = 0;
  const unhealthyPaneIds: string[] = [];

  for (const pane of panes) {
    const hasError = pane.warnings.some((warning) => warning.severity === 'error');
    const hasWarning = pane.warnings.length > 0;
    if (hasError) {
      errorPaneCount += 1;
    }
    if (hasWarning) {
      warningPaneCount += 1;
      unhealthyPaneIds.push(pane.paneId);
    }
  }

  return {
    sessionId: input.sessionId,
    label: input.label,
    activePaneId: input.activePaneId,
    selected: input.selected,
    panes,
    summary: {
      paneCount: panes.length,
      warningPaneCount,
      errorPaneCount,
      unhealthyPaneIds,
    },
  };
}
