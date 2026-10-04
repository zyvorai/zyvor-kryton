// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import type { ReactNode } from 'react';
import { stateTone } from '../lib/format';

export type HeroTint = 'green' | 'amber' | 'purple' | 'red';

export function PageHero({
  eyebrow,
  title,
  lede,
  tint,
  actions,
}: {
  eyebrow: string;
  title: ReactNode;
  lede?: ReactNode;
  tint?: HeroTint;
  actions?: ReactNode;
}) {
  return (
    <header className={tint ? `page-hero hero-tint-${tint}` : 'page-hero'}>
      <div className="page-hero-copy">
        <p className="eyebrow">{eyebrow}</p>
        <h1>{title}</h1>
        {lede && <p>{lede}</p>}
      </div>
      {actions && <div className="page-hero-actions">{actions}</div>}
    </header>
  );
}

export function StatePill({ state }: { state?: string }) {
  return <span className={`state-pill tone-${stateTone(state)}`}>{state || 'unknown'}</span>;
}

export function Chip({ tone, children }: { tone?: 'good' | 'warn' | 'bad' | 'info' | 'neutral'; children: ReactNode }) {
  return <span className={`chip chip-${tone || 'neutral'}`}>{children}</span>;
}

export function ListEmpty({ title, children, action }: { title: string; children?: ReactNode; action?: ReactNode }) {
  return (
    <div className="list-empty">
      <h3>{title}</h3>
      {children && <p>{children}</p>}
      {action}
    </div>
  );
}

export function Card({ eyebrow, title, lede, actions, children, className }: {
  eyebrow?: string;
  title?: ReactNode;
  lede?: ReactNode;
  actions?: ReactNode;
  children?: ReactNode;
  className?: string;
}) {
  return (
    <section className={`card${className ? ' ' + className : ''}`}>
      {(eyebrow || title || actions) && (
        <div className="card-head">
          <div>
            {eyebrow && <p className="eyebrow">{eyebrow}</p>}
            {title && <h2>{title}</h2>}
            {lede && <p className="card-lede">{lede}</p>}
          </div>
          {actions && <div className="card-actions">{actions}</div>}
        </div>
      )}
      {children}
    </section>
  );
}

export function DefList({ rows }: { rows: [string, ReactNode][] }) {
  return (
    <dl className="deflist">
      {rows.map(([k, v]) => (
        <div key={k}>
          <dt>{k}</dt>
          <dd>{v ?? '—'}</dd>
        </div>
      ))}
    </dl>
  );
}

export function Progress({ value }: { value: number }) {
  const v = Math.max(0, Math.min(100, value || 0));
  return (
    <div className="progress" role="progressbar" aria-valuenow={v} aria-valuemin={0} aria-valuemax={100}>
      <div className="progress-fill" style={{ width: `${v}%` }} />
    </div>
  );
}

export function Segmented<T extends string>({
  value,
  options,
  onChange,
  label,
}: {
  value: T;
  options: { value: T; label: string; count?: number }[];
  onChange: (v: T) => void;
  label: string;
}) {
  return (
    <div className="segmented" role="tablist" aria-label={label}>
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          role="tab"
          aria-selected={value === o.value}
          className={value === o.value ? 'active' : ''}
          onClick={() => onChange(o.value)}
        >
          {o.label}
          {o.count !== undefined && <span className="segmented-count">{o.count}</span>}
        </button>
      ))}
    </div>
  );
}

export function probeTone(status?: string): 'good' | 'warn' | 'bad' | 'neutral' {
  if (status === 'pass') return 'good';
  if (status === 'warn') return 'warn';
  if (status === 'fail') return 'bad';
  return 'neutral';
}

export function FindingRow({ status, title, message, hint }: { status?: string; title: string; message?: string; hint?: string }) {
  return (
    <div className="finding-row">
      <Chip tone={probeTone(status)}>{status || 'unknown'}</Chip>
      <div>
        <strong>{title}</strong>
        {message && <span>{message}</span>}
        {hint && <span className="finding-hint">{hint}</span>}
      </div>
    </div>
  );
}
