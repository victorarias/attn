import {
  useEffect,
  Fragment,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type ReactNode,
} from 'react';
import { createPortal } from 'react-dom';
import { useEscapeStack } from '../hooks/useEscapeStack';
import { LABEL_COLOR_MAP, type QuickLabel } from './quickLabels';

export interface FloatingQuickLabelPickerProps {
  mode?: 'floating';
  className: string;
  groups: readonly (readonly QuickLabel[])[];
  anchorEl: HTMLElement;
  cursorHint?: { x: number; y: number } | null;
  onSelect: (label: QuickLabel) => void;
  onDismiss: () => void;
}

interface ChipQuickLabelPickerProps {
  mode: 'chips';
  className: string;
  groups: readonly (readonly QuickLabel[])[];
  isSelected: (label: QuickLabel) => boolean;
  onSelect: (label: QuickLabel) => void;
  onHint: (hint: string | null) => void;
  children?: ReactNode;
}

export type QuickLabelPickerProps = FloatingQuickLabelPickerProps | ChipQuickLabelPickerProps;

const PICKER_WIDTH = 192;
const GAP = 6;
const VIEWPORT_PADDING = 12;

function addDeferredPointerDownListener(listener: (event: PointerEvent) => void): () => void {
  const timer = window.setTimeout(() => {
    document.addEventListener('pointerdown', listener, true);
  }, 0);
  return () => {
    window.clearTimeout(timer);
    document.removeEventListener('pointerdown', listener, true);
  };
}

function computePosition(
  anchorEl: HTMLElement,
  cursorHint: { x: number; y: number } | null | undefined,
  height: number,
): { top: number; left: number } {
  const rect = anchorEl.getBoundingClientRect();
  const below = rect.bottom + GAP;
  const above = rect.top - GAP - height;
  const lowestTop = window.innerHeight - VIEWPORT_PADDING - height;
  let top = below;
  if (height > 0 && below > lowestTop) {
    top = above >= VIEWPORT_PADDING ? above : Math.max(VIEWPORT_PADDING, lowestTop);
  }

  let left = cursorHint ? cursorHint.x - 28 : rect.right - PICKER_WIDTH / 2;
  left = Math.max(
    VIEWPORT_PADDING,
    Math.min(left, window.innerWidth - PICKER_WIDTH - VIEWPORT_PADDING),
  );
  return { top, left };
}

function FloatingQuickLabelPicker({
  className,
  groups,
  anchorEl,
  cursorHint,
  onSelect,
  onDismiss,
}: FloatingQuickLabelPickerProps) {
  const [position, setPosition] = useState<{ top: number; left: number } | null>(null);
  const [height, setHeight] = useState(0);
  const ref = useRef<HTMLDivElement>(null);
  const { labels, indexedGroups } = useMemo(() => {
    const flattened = groups.flat();
    let nextIndex = 0;
    return {
      labels: flattened,
      indexedGroups: groups.map((group) =>
        group.map((label) => ({ label, index: nextIndex++ })),
      ),
    };
  }, [groups]);

  useEffect(() => {
    const update = () => setPosition(computePosition(anchorEl, cursorHint, height));
    update();
    window.addEventListener('scroll', update, true);
    window.addEventListener('resize', update);
    return () => {
      window.removeEventListener('scroll', update, true);
      window.removeEventListener('resize', update);
    };
  }, [anchorEl, cursorHint, height]);

  useLayoutEffect(() => {
    const measured = ref.current?.offsetHeight ?? 0;
    if (measured > 0) {
      setHeight((current) => (current === measured ? current : measured));
    }
  });

  useEscapeStack(onDismiss, true);

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      const isDigit = (event.code >= 'Digit1' && event.code <= 'Digit9') || event.code === 'Digit0';
      if (isDigit && !event.ctrlKey && !event.metaKey) {
        event.preventDefault();
        const digit = parseInt(event.code.slice(5), 10);
        const index = digit === 0 ? 9 : digit - 1;
        if (index < labels.length) {
          onSelect(labels[index]);
        }
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => {
      window.removeEventListener('keydown', handleKeyDown);
    };
  }, [labels, onSelect]);

  useEffect(() => {
    const handlePointerDown = (event: PointerEvent) => {
      if (ref.current && !ref.current.contains(event.target as Node)) {
        onDismiss();
      }
    };
    return addDeferredPointerDownListener(handlePointerDown);
  }, [onDismiss]);

  if (!position) {
    return null;
  }

  return createPortal(
    <div
      ref={ref}
      className={className}
      style={{ top: position.top, left: position.left, width: PICKER_WIDTH }}
      onMouseDown={(event) => event.stopPropagation()}
    >
      {indexedGroups.map((group, groupIndex) => (
        <Fragment key={group[0].label.id}>
          {groupIndex > 0 ? <hr className="md-quick-label-divider" /> : null}
          {group.map(({ label, index }) => {
            const color = LABEL_COLOR_MAP[label.color];
            return (
              <button
                key={label.id}
                type="button"
                className="md-quick-label-row"
                onClick={() => onSelect(label)}
                title={label.tip}
              >
                <span
                  className="md-ql-chip"
                  style={color
                    ? ({
                        background: color.bg,
                        '--md-ql-text': color.text,
                        '--md-ql-text-dark': color.darkText,
                      } as CSSProperties)
                    : undefined}
                >
                  {label.emoji}
                </span>
                <span className="md-quick-label-text">{label.text}</span>
                {index < 10 && <span className="md-quick-label-num">{(index + 1) % 10}</span>}
              </button>
            );
          })}
        </Fragment>
      ))}
    </div>,
    document.body,
  );
}

function ChipQuickLabelPicker({
  className,
  groups,
  isSelected,
  onSelect,
  onHint,
  children,
}: ChipQuickLabelPickerProps) {
  return (
    <div className={className}>
      {groups.map((group, groupIndex) => (
        <Fragment key={group[0].id}>
          {groupIndex > 0 ? <span className="anno-popup-divider" /> : null}
          {group.map((label) => (
            <button
              key={label.id}
              type="button"
              className={`anno-popup-label${isSelected(label) ? ' anno-popup-label--on' : ''}`}
              title={label.text}
              aria-label={label.text}
              onClick={() => onSelect(label)}
              onMouseEnter={() => onHint(label.text)}
              onMouseLeave={() => onHint(null)}
              onFocus={() => onHint(label.text)}
              onBlur={() => onHint(null)}
            >
              {label.emoji}
            </button>
          ))}
        </Fragment>
      ))}
      {children}
    </div>
  );
}

export function QuickLabelPicker(props: QuickLabelPickerProps) {
  if (props.mode === 'chips') {
    return <ChipQuickLabelPicker {...props} />;
  }
  return <FloatingQuickLabelPicker {...props} />;
}
