// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import { useEffect, useRef, type ReactNode } from 'react';
import Icon from './Icon';

/** Modal sheet (centered) or drawer (right edge). Escape and backdrop click close it. */
export default function Overlay({
  kind,
  label,
  onClose,
  children,
}: {
  kind: 'sheet' | 'drawer' | 'dialog';
  label: string;
  onClose: () => void;
  children: ReactNode;
}) {
  const panel = useRef<HTMLDivElement | null>(null);
  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') closeRef.current();
    };
    document.addEventListener('keydown', onKey);
    document.body.classList.add('overlay-open');
    const first = panel.current?.querySelector<HTMLElement>('[data-autofocus], input, select, textarea, button:not(.overlay-close)');
    first?.focus();
    return () => {
      document.removeEventListener('keydown', onKey);
      document.body.classList.remove('overlay-open');
      prev?.focus?.();
    };
  }, []);
  return (
    <div
      className={`overlay overlay-${kind}`}
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className={`overlay-panel overlay-panel-${kind}`} role="dialog" aria-modal="true" aria-label={label} ref={panel}>
        <button type="button" className="overlay-close" onClick={onClose} aria-label="Close">
          <Icon name="close" size={14} />
        </button>
        {children}
      </div>
    </div>
  );
}
