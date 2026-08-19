import { openUrl } from '@tauri-apps/plugin-opener';
import type { MouseEvent } from 'react';
import type { AutomationProvenance as AutomationProvenanceValue } from '../types/generated';
import './AutomationProvenance.css';

function shortDefinitionName(name: string): string {
  return name.replace(/^requested pr review\s*[-—:]\s*/i, '').trim() || name;
}

function shortRepository(repository: string): string {
  const parts = repository.split('/').filter(Boolean);
  return parts[parts.length - 1] || repository;
}

export function automationProvenanceDescription(provenance: AutomationProvenanceValue): string {
  const parts = [`Automation: ${provenance.definition_name}`];
  const pr = provenance.pull_request;
  if (pr) {
    parts.push(`${pr.repository}#${pr.number}`);
    if (pr.title) parts.push(pr.title);
  }
  return parts.join(' · ');
}

export function AutomationProvenance({
  provenance,
  density = 'line',
  interactive = false,
}: {
  provenance?: AutomationProvenanceValue;
  density?: 'badge' | 'compact' | 'line' | 'detail';
  interactive?: boolean;
}) {
  if (!provenance) return null;

  const pr = provenance.pull_request;
  const description = automationProvenanceDescription(provenance);
  const target = pr ? `${shortRepository(pr.repository)}#${pr.number}` : null;
  const openPR = interactive && pr
    ? (event: MouseEvent<HTMLButtonElement>) => {
        event.stopPropagation();
        openUrl(pr.url).catch((error) => {
          console.error('[AutomationProvenance] Failed to open PR URL:', error);
        });
      }
    : null;

  if (density === 'badge') {
    return (
      <span className="automation-provenance automation-provenance--badge" title={description} aria-label={description}>
        <span aria-hidden="true">⚡</span>
      </span>
    );
  }

  if (density === 'compact') {
    return (
      <span className="automation-provenance automation-provenance--compact" title={description} aria-label={description}>
        <span className="automation-provenance__kind" aria-hidden="true">⚡</span>
        <span className="automation-provenance__definition">{shortDefinitionName(provenance.definition_name)}</span>
        {pr && <span className="automation-provenance__target">#{pr.number}</span>}
      </span>
    );
  }

  return (
    <span className={`automation-provenance automation-provenance--${density}`} title={description}>
      <span className="automation-provenance__kind">
        <span aria-hidden="true">⚡</span>
        Automation
      </span>
      <span className="automation-provenance__definition">{shortDefinitionName(provenance.definition_name)}</span>
      {target && (
        openPR ? (
          <button
            type="button"
            className="automation-provenance__target"
            onPointerDown={(event) => event.stopPropagation()}
            onClick={openPR}
          >
            {target} ↗
          </button>
        ) : (
          <span className="automation-provenance__target">{target}</span>
        )
      )}
      {pr?.title && <span className="automation-provenance__title">{pr.title}</span>}
    </span>
  );
}
