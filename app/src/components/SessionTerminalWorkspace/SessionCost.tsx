interface HeaderSessionCostProps {
  costUsd?: number;
  unknown?: boolean;
}

const usdFormatter = new Intl.NumberFormat('en-US', {
  style: 'currency',
  currency: 'USD',
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
});

function formatSessionCostUSD(costUsd: number): string {
  if (costUsd > 0 && costUsd < 0.01) {
    return '<$0.01';
  }
  return usdFormatter.format(costUsd);
}

export function HeaderSessionCost({ costUsd, unknown }: HeaderSessionCostProps) {
  if (unknown) {
    return (
      <span
        className="workspace-pane-cost workspace-pane-cost--unknown"
        aria-label="Session cost unknown"
        title="Session cost unknown"
      >
        unknown
      </span>
    );
  }

  if (costUsd === undefined) {
    return null;
  }

  const display = formatSessionCostUSD(costUsd);
  return (
    <span
      className="workspace-pane-cost"
      aria-label={`Session cost ${display} USD`}
      title={`Session cost ${display} USD`}
    >
      {display}
    </span>
  );
}
