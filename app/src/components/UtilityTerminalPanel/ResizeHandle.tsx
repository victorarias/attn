// app/src/components/UtilityTerminalPanel/ResizeHandle.tsx
import { useRef, useCallback } from 'react';
import './ResizeHandle.css';

const MIN_HEIGHT = 100;
const MAX_HEIGHT_RATIO = 0.6;
const DEFAULT_HEIGHT = 200;

interface ResizeHandleProps {
  height: number;
  onHeightChange: (height: number) => void;
}

export function ResizeHandle({ height, onHeightChange }: ResizeHandleProps) {
  const startY = useRef(0);
  const startHeight = useRef(0);

  const handleMouseMove = useCallback(
    (e: MouseEvent) => {
      const delta = startY.current - e.clientY;
      const maxHeight = window.innerHeight * MAX_HEIGHT_RATIO;
      const newHeight = Math.min(maxHeight, Math.max(MIN_HEIGHT, startHeight.current + delta));
      onHeightChange(newHeight);
    },
    [onHeightChange]
  );

  const handleMouseUp = useCallback(() => {
    document.removeEventListener('mousemove', handleMouseMove);
    document.removeEventListener('mouseup', handleMouseUp);
    document.body.style.cursor = '';
    document.body.style.userSelect = '';
  }, [handleMouseMove]);

  const handleMouseDown = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      startY.current = e.clientY;
      startHeight.current = height;
      document.body.style.cursor = 'ns-resize';
      document.body.style.userSelect = 'none';
      document.addEventListener('mousemove', handleMouseMove);
      document.addEventListener('mouseup', handleMouseUp);
    },
    [height, handleMouseMove, handleMouseUp]
  );

  const handleDoubleClick = useCallback(() => {
    onHeightChange(DEFAULT_HEIGHT);
  }, [onHeightChange]);

  return (
    <div
      className="terminal-resize-handle"
      onMouseDown={handleMouseDown}
      onDoubleClick={handleDoubleClick}
    />
  );
}
