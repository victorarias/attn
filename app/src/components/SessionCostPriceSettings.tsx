import { useMemo, useState } from 'react';

export const SESSION_COST_PRICE_PREFIX = 'session_cost.price.';

const RATE_FIELDS = [
  { key: 'input_usd_per_mtok', label: 'Input' },
  { key: 'output_usd_per_mtok', label: 'Output' },
  { key: 'cache_read_usd_per_mtok', label: 'Cache read' },
  { key: 'cache_write_5m_usd_per_mtok', label: 'Cache write · 5m' },
  { key: 'cache_write_1h_usd_per_mtok', label: 'Cache write · 1h' },
] as const;

type RateField = (typeof RATE_FIELDS)[number]['key'];
type PriceDraft = Record<RateField, string>;
type PriceCard = Record<RateField, number>;

const blankDraft = (): PriceDraft => ({
  input_usd_per_mtok: '',
  output_usd_per_mtok: '',
  cache_read_usd_per_mtok: '',
  cache_write_5m_usd_per_mtok: '',
  cache_write_1h_usd_per_mtok: '',
});

function parsePriceCard(raw: string): PriceCard | null {
  try {
    const value = JSON.parse(raw) as Partial<Record<RateField, unknown>>;
    if (
      typeof value !== 'object'
      || value === null
      || Array.isArray(value)
      || Object.keys(value).some((key) => !RATE_FIELDS.some((field) => field.key === key))
    ) return null;
    const card = {} as PriceCard;
    for (const { key } of RATE_FIELDS) {
      const rate = value?.[key];
      if (typeof rate !== 'number' || !Number.isFinite(rate) || rate < 0) return null;
      card[key] = rate;
    }
    return card;
  } catch {
    return null;
  }
}

function draftFromRaw(raw: string): PriceDraft {
  const card = parsePriceCard(raw);
  if (!card) return blankDraft();
  return Object.fromEntries(RATE_FIELDS.map(({ key }) => [key, String(card[key])])) as PriceDraft;
}

function priceCardFromDraft(draft: PriceDraft): PriceCard | null {
  const card = {} as PriceCard;
  for (const { key } of RATE_FIELDS) {
    if (draft[key].trim() === '') return null;
    const rate = Number(draft[key]);
    if (!Number.isFinite(rate) || rate < 0) return null;
    card[key] = rate;
  }
  return card;
}

function serializePriceCard(card: PriceCard): string {
  return JSON.stringify(Object.fromEntries(RATE_FIELDS.map(({ key }) => [key, card[key]])));
}

interface RateInputsProps {
  draft: PriceDraft;
  idPrefix: string;
  onChange: (key: RateField, value: string) => void;
}

function RateInputs({ draft, idPrefix, onChange }: RateInputsProps) {
  return (
    <div className="settings-price-grid">
      {RATE_FIELDS.map(({ key, label }) => {
        const inputId = `${idPrefix}-${key}`;
        return (
          <div className="settings-field" key={key}>
            <label className="settings-label" htmlFor={inputId}>{label} · USD/MTok</label>
            <input
              id={inputId}
              data-testid={inputId}
              type="number"
              min="0"
              step="any"
              inputMode="decimal"
              className="settings-input settings-price-input"
              value={draft[key]}
              onChange={(event) => onChange(key, event.target.value)}
              placeholder="0"
            />
          </div>
        );
      })}
    </div>
  );
}

interface ExistingPriceOverrideProps {
  modelId: string;
  raw: string;
  onSetSetting: (key: string, value: string) => void;
}

function ExistingPriceOverride({ modelId, raw, onSetSetting }: ExistingPriceOverrideProps) {
  const parsed = useMemo(() => parsePriceCard(raw), [raw]);
  const [draft, setDraft] = useState(() => draftFromRaw(raw));
  const card = priceCardFromDraft(draft);
  const serialized = card ? serializePriceCard(card) : null;
  const changed = serialized !== (parsed ? serializePriceCard(parsed) : null);
  const settingKey = `${SESSION_COST_PRICE_PREFIX}${modelId}`;
  const idPrefix = `settings-price-${modelId}`;

  return (
    <div className="settings-price-card">
      <div className="settings-price-card-head">
        <code className="settings-price-model">{modelId}</code>
        <div className="settings-price-actions">
          <button
            type="button"
            className="settings-action"
            data-testid={`${idPrefix}-save`}
            disabled={!card || !changed}
            onClick={() => card && onSetSetting(settingKey, serializePriceCard(card))}
          >
            Save
          </button>
          <button
            type="button"
            className="settings-action danger"
            data-testid={`${idPrefix}-remove`}
            onClick={() => onSetSetting(settingKey, '')}
          >
            Remove
          </button>
        </div>
      </div>
      {!parsed && (
        <div className="settings-warning">
          This saved override is invalid. Complete every rate to repair it, or remove it.
        </div>
      )}
      <RateInputs
        draft={draft}
        idPrefix={idPrefix}
        onChange={(key, value) => setDraft((current) => ({ ...current, [key]: value }))}
      />
    </div>
  );
}

interface SessionCostPriceSettingsProps {
  settings: Record<string, string>;
  onSetSetting: (key: string, value: string) => void;
}

export function SessionCostPriceSettings({ settings, onSetSetting }: SessionCostPriceSettingsProps) {
  const overrides = useMemo(() => {
    const result: Array<{ modelId: string; raw: string }> = [];
    for (const [key, raw] of Object.entries(settings)) {
      if (!key.startsWith(SESSION_COST_PRICE_PREFIX) || raw.trim() === '') continue;
      const modelId = key.slice(SESSION_COST_PRICE_PREFIX.length);
      if (modelId.trim() !== '') result.push({ modelId, raw });
    }
    return result.sort((left, right) => left.modelId.localeCompare(right.modelId));
  }, [settings]);
  const existingModelIds = useMemo(() => new Set(overrides.map(({ modelId }) => modelId)), [overrides]);
  const [modelId, setModelId] = useState('');
  const [draft, setDraft] = useState<PriceDraft>(blankDraft);
  const card = priceCardFromDraft(draft);
  const normalizedModelId = modelId.trim();
  const canAdd = Boolean(card && normalizedModelId && !existingModelIds.has(normalizedModelId));

  const addOverride = () => {
    if (!card || !canAdd) return;
    onSetSetting(`${SESSION_COST_PRICE_PREFIX}${normalizedModelId}`, serializePriceCard(card));
    setModelId('');
    setDraft(blankDraft());
  };

  return (
    <section className="settings-block" data-testid="settings-session-cost-prices">
      <div className="settings-block-intro">
        <div className="settings-kicker">Agents</div>
        <h3>Model pricing</h3>
        <p className="settings-description">
          Add exact model IDs that are missing from the built-in price table, or correct stale
          rates. Every field is USD per million tokens; cache reads and writes keep their own rates.
        </p>
      </div>
      <div className="settings-block-body">
        {overrides.length > 0 && (
          <div className="settings-price-list">
            {overrides.map(({ modelId: overrideModelId, raw }) => (
              <ExistingPriceOverride
                key={`${overrideModelId}:${raw}`}
                modelId={overrideModelId}
                raw={raw}
                onSetSetting={onSetSetting}
              />
            ))}
          </div>
        )}

        <div className="settings-price-card settings-price-card--new">
          <div className="settings-field">
            <label className="settings-label" htmlFor="settings-price-new-model">Exact model ID</label>
            <input
              id="settings-price-new-model"
              data-testid="settings-price-new-model"
              type="text"
              className="settings-input"
              value={modelId}
              onChange={(event) => setModelId(event.target.value)}
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
              placeholder="provider-model-id"
            />
          </div>
          <RateInputs
            draft={draft}
            idPrefix="settings-price-new"
            onChange={(key, value) => setDraft((current) => ({ ...current, [key]: value }))}
          />
          {normalizedModelId && existingModelIds.has(normalizedModelId) && (
            <div className="settings-warning">That model already has an override above.</div>
          )}
          <div className="settings-price-actions">
            <button
              type="button"
              className="settings-action"
              data-testid="settings-price-add"
              disabled={!canAdd}
              onClick={addOverride}
            >
              Add price override
            </button>
          </div>
        </div>
      </div>
    </section>
  );
}
