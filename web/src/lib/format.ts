// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import type { CloudEvent } from '../types';

export function fmtMemory(mib: number): string {
  if (!Number.isFinite(mib)) return '—';
  if (mib >= 1024) return `${Math.round(mib / 102.4) / 10} GB`;
  return `${mib} MiB`;
}

export function ago(value?: string, now = Date.now()): string {
  if (!value) return 'now';
  const t = new Date(value).getTime();
  if (Number.isNaN(t)) return '—';
  const s = Math.max(1, Math.floor((now - t) / 1000));
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

export type StateTone = 'running' | 'stopped' | 'provisioning' | 'failed' | 'neutral';

export function stateTone(v?: string): StateTone {
  if (!v) return 'neutral';
  if (['failed', 'unknown', 'error'].includes(v)) return 'failed';
  if (['provisioning', 'starting', 'stopping', 'pending', 'restoring', 'deleting'].includes(v)) return 'provisioning';
  if (['running', 'ready', 'done', 'succeeded'].includes(v)) return 'running';
  if (v === 'stopped') return 'stopped';
  return 'neutral';
}

export function eventTitle(type: string): string {
  return String(type)
    .split('.')
    .slice(-2)
    .join(' ')
    .replace(/\b\w/g, (c) => c.toUpperCase());
}

export function eventDescription(e: CloudEvent): string {
  const d = e.data || {};
  const name = d.name || d.machineId || e.subject || 'resource';
  return `${name}${d.project ? ` · ${d.project}` : ''}`;
}

export function healthPercent(machines: number, running: number): number {
  if (!machines) return 100;
  return Math.max(0, Math.min(100, Math.round((running / machines) * 100)));
}

export function plural(n: number, one: string, many = one + 's'): string {
  return `${n} ${n === 1 ? one : many}`;
}

export function fmtTime(t?: string): string {
  if (!t) return '';
  const d = new Date(t);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleTimeString();
}

export function fmtDateTime(t?: string): string {
  if (!t) return '—';
  const d = new Date(t);
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString();
}
