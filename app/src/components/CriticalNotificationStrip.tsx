// app/src/components/CriticalNotificationStrip.tsx
//
// The ambient half of notification severity: while at least one critical
// notification is unread, a strip sits under the sidebar header naming the
// newest one, and the notifications bell escalates to match (see
// `.sidebar-tool-btn.has-critical` in this component's stylesheet, applied by
// Sidebar from the same count).
//
// It renders nothing at all when the count is zero, so "cleared" is an unmount
// rather than a hidden element — there is no state in which an emptied surface
// still occupies the sidebar.
//
// Its input comes from the daemon on both the notification_list result and the
// notifications_updated broadcast, never derived from a fetched list: the panel
// only lists while it is open, and a user who has never opened it must still be
// unable to miss something critical.
import './CriticalNotificationStrip.css';

interface CriticalNotificationStripProps {
  // How many critical notifications are unread. Zero renders nothing.
  count: number;
  // Title of the newest unread critical notification.
  title: string;
  // Opens the notifications panel.
  onOpen: () => void;
}

export function CriticalNotificationStrip({ count, title, onOpen }: CriticalNotificationStripProps) {
  if (count <= 0) return null;

  // The count only earns its place once it says something the title does not.
  const label = title || 'Critical notification';
  const ariaLabel =
    count === 1
      ? `1 unread critical notification: ${label}. Open notifications.`
      : `${count} unread critical notifications, newest: ${label}. Open notifications.`;

  return (
    <button type="button" className="critical-strip" onClick={onOpen} aria-label={ariaLabel}>
      <span className="critical-strip-mark" aria-hidden="true" />
      <span className="critical-strip-text">{label}</span>
      {count > 1 && (
        <span className="critical-strip-count" aria-hidden="true">
          {count}
        </span>
      )}
    </button>
  );
}
