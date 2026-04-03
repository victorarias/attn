import './RemoteSessionPlaceholder.css';

interface RemoteSessionPlaceholderProps {
  label: string;
  endpointName: string;
  endpointStatus?: string;
  directory: string;
  branch?: string;
}

export function RemoteSessionPlaceholder({
  label,
  endpointName,
  endpointStatus,
  directory,
  branch,
}: RemoteSessionPlaceholderProps) {
  return (
    <div className="remote-session-placeholder" data-remote-session-placeholder={label}>
      <div
        className="remote-session-placeholder__card"
        data-remote-session-card={label}
        data-remote-endpoint-name={endpointName}
        data-remote-endpoint-status={endpointStatus || 'connected'}
        data-remote-directory={directory}
      >
        <div className="remote-session-placeholder__eyebrow">Remote Session</div>
        <h2>{label}</h2>
        <div className="remote-session-placeholder__meta">
          <span className={`remote-session-placeholder__badge status-${endpointStatus || 'connected'}`}>
            {endpointName}
          </span>
          {branch && <span className="remote-session-placeholder__branch">{branch}</span>}
        </div>
        <p className="remote-session-placeholder__body">
          This endpoint is connected and its sessions are visible here, but terminal attach, git actions, and review controls are still local-only in this slice.
        </p>
        <dl className="remote-session-placeholder__details">
          <div>
            <dt>Directory</dt>
            <dd>{directory}</dd>
          </div>
          <div>
            <dt>Status</dt>
            <dd>{endpointStatus || 'connected'}</dd>
          </div>
        </dl>
      </div>
    </div>
  );
}
