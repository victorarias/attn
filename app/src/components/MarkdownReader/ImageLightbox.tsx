import { createPortal } from 'react-dom';
import { useEscapeStack } from '../../hooks/useEscapeStack';

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
 * Escape goes through useEscapeStack (the app-wide LIFO dismiss stack): the
 * lightbox only mounts while open, so it sits on top of the stack for its
 * whole lifetime and one Escape press closes ONLY the lightbox — never the
 * overlay/tile underneath. (A hand-rolled window listener cannot do this:
 * stopPropagation does not shield other listeners on the same node, so it
 * would dismiss the stack-top overlay simultaneously.)
 */
export function ImageLightbox({ src, alt, onClose }: ImageLightboxProps) {
  useEscapeStack(onClose, true);

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
