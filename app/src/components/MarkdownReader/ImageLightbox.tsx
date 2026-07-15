import { useEffect } from 'react';
import { createPortal } from 'react-dom';

export interface ImageLightboxProps {
  src: string;
  alt: string;
  onClose: () => void;
}

/**
 * Full-viewport image lightbox, ported from plannotator's Viewer. Portals to
 * document.body so it escapes tile overflow/stacking contexts. Closes on
 * Escape or backdrop click; clicking the image itself does not close. Mounts
 * and unmounts instantly (no enter/exit motion), which is trivially
 * reduced-motion-safe.
 *
 * The Escape listener runs in the capture phase and stops propagation so an
 * open lightbox consumes the key before tile/workspace shortcuts (closing the
 * lightbox must not also close the tile).
 */
export function ImageLightbox({ src, alt, onClose }: ImageLightboxProps) {
  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.stopPropagation();
        event.preventDefault();
        onClose();
      }
    };
    window.addEventListener('keydown', handleKeyDown, true);
    return () => window.removeEventListener('keydown', handleKeyDown, true);
  }, [onClose]);

  return createPortal(
    <div className="md-lightbox" role="dialog" aria-modal="true" onClick={onClose}>
      <img
        className="md-lightbox-img"
        src={src}
        alt={alt}
        onClick={(event) => event.stopPropagation()}
      />
      {alt && <div className="md-lightbox-caption">{alt}</div>}
    </div>,
    document.body,
  );
}
