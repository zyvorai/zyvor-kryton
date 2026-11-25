// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

// Pure helpers for the fleet API: list filters, bulk actions, allowed
// actions, event filters, snapshot inventory and schedules.

import type { BulkAction, BulkResponse, CloudEvent, List, Machine, Schedule } from '../types';

const q = encodeURIComponent;

function query(params: [string, string | undefined][]): string {
  const parts = params.filter(([, v]) => v).map(([k, v]) => `${k}=${q(v!)}`);
  return parts.length ? '?' + parts.join('&') : '';
}

/* —— Machine list —— */

export type ServerFilter = { selector: string; name: string; states: string[] };

export const EMPTY_FILTER: ServerFilter = { selector: '', name: '', states: [] };

export const MACHINE_STATES = ['running', 'stopped', 'provisioning', 'starting', 'stopping', 'pending', 'paused', 'migrating', 'failed', 'unknown'];

export const filterActive = (f: ServerFilter) => !!(f.selector.trim() || f.name.trim() || f.states.length);

export function machinesPath(project: string, f: ServerFilter = EMPTY_FILTER): string {
  return (
    '/api/v1/machines' +
    query([
      ['project', project],
      ['selector', f.selector.trim()],
      ['name', f.name.trim()],
      ['state', f.states.join(',')],
      ['limit', '500'],
    ])
  );
}

/** Follows nextCursor so projects with more than one page are listed in full. */
export async function fetchAll<T>(path: string, get: (p: string) => Promise<List<T>>, maxPages = 40): Promise<T[]> {
  const out: T[] = [];
  let cursor = '';
  for (let page = 0; page < maxPages; page++) {
    const res = await get(cursor ? `${path}${path.includes('?') ? '&' : '?'}cursor=${q(cursor)}` : path);
    out.push(...(res.items || []));
    cursor = res.nextCursor || '';
    if (!cursor) break;
  }
  return out;
}

/* —— Allowed actions —— */

export type Action = 'start' | 'stop' | 'restart' | 'snapshot' | 'delete';

/**
 * Whether the machine accepts an action now. The server's allowedActions is
 * authoritative; a server that omits it entirely (older krytond) falls back to
 * the state. An empty list from a current server means nothing is allowed.
 */
export function allows(m: Machine, action: Action): boolean {
  if (m.allowedActions) return m.allowedActions.includes(action);
  // Go omits an empty list, so this also covers transitional states.
  if (action === 'delete') return !m.protection && !['deleting', 'starting', 'stopping', 'migrating'].includes(m.state);
  if (action === 'start') return m.state === 'stopped' || m.state === 'failed';
  if (action === 'stop' || action === 'restart') return m.state === 'running';
  return m.state === 'running' || m.state === 'stopped';
}

/* —— Bulk actions —— */

export const MAX_BULK = 100;

export function bulkBody(project: string, action: BulkAction, ids: string[], opts: { dryRun: boolean; snapshotName?: string }) {
  const body: Record<string, unknown> = { project, action, ids };
  if (opts.dryRun) body.dryRun = true;
  else if (action === 'delete') body.confirm = true;
  if (action === 'snapshot' && opts.snapshotName?.trim()) body.snapshotName = opts.snapshotName.trim();
  return body;
}

/** The machines a real run should target: those the dry run accepted. */
export const acceptedIds = (r: BulkResponse) => r.results.filter((x) => x.ok).map((x) => x.id);

export function bulkHeadline(r: BulkResponse): string {
  const n = (k: number) => `${k} ${k === 1 ? 'machine' : 'machines'}`;
  if (r.dryRun) return `Preview: ${r.succeeded} of ${n(r.total)} can ${r.action}${r.failed ? `, ${r.failed} cannot` : ''}.`;
  return `${r.action}: ${r.succeeded} succeeded${r.failed ? `, ${r.failed} failed` : ''}.`;
}

/* —— Summary groups —— */

export function labelKeys(machines: Machine[]): string[] {
  const keys = new Set<string>();
  for (const m of machines) for (const k of Object.keys(m.spec.labels || {})) keys.add(k);
  return [...keys].sort();
}

export const summaryPath = (project: string, groupBy: string) => '/api/v1/summary' + query([['project', project], ['groupBy', groupBy.trim()]]);

/* —— Events —— */

export type EventFilter = { type: string; machineId: string; sinceHours: number };

export const NO_EVENT_FILTER: EventFilter = { type: '', machineId: '', sinceHours: 0 };

export const EVENT_TYPES: { value: string; label: string }[] = [
  { value: '', label: 'All events' },
  { value: 'io.kryton.machine.*', label: 'Machines' },
  { value: 'io.kryton.snapshot.*', label: 'Snapshots' },
  { value: 'io.kryton.schedule.*', label: 'Schedules' },
  { value: 'io.kryton.machine.job.*', label: 'Guest jobs' },
  { value: 'io.kryton.machine.deleted,io.kryton.machine.expired', label: 'Removals' },
];

export const eventFilterActive = (f: EventFilter) => !!(f.type || f.machineId || f.sinceHours);

export function eventsPath(f: EventFilter, now = Date.now()): string {
  const since = f.sinceHours > 0 ? new Date(now - f.sinceHours * 3600_000).toISOString().replace(/\.\d{3}Z$/, 'Z') : '';
  return '/api/v1/events' + query([['limit', '100'], ['type', f.type], ['machineId', f.machineId], ['since', since]]);
}

/** The stream accepts type and machineId only; new events are always recent. */
export const streamPath = (f: EventFilter) => '/api/v1/events/stream' + query([['type', f.type], ['machineId', f.machineId]]);

export const mergeEvent = (cur: CloudEvent[], e: CloudEvent) => [e, ...cur.filter((x) => x.id !== e.id)].slice(0, 100);

/** Merges a polled history with what the stream already delivered, newest first. */
export function mergeHistory(cur: CloudEvent[], history: CloudEvent[]): CloudEvent[] {
  const seen = new Set(history.map((e) => e.id));
  const at = (e: CloudEvent) => Date.parse(e.time || '') || 0;
  return [...cur.filter((e) => !seen.has(e.id)), ...history].sort((a, b) => at(b) - at(a)).slice(0, 100);
}

/* —— Snapshot inventory —— */

export function snapshotsPath(project: string, olderThan: string, selector: string): string {
  return '/api/v1/snapshots' + query([['project', project], ['olderThan', olderThan], ['selector', selector.trim()], ['limit', '200']]);
}

/* —— Schedules —— */

export type ScheduleForm = {
  action: Schedule['action'];
  cron: string;
  timezone: string;
  snapshotName: string;
  retain: string;
  maxFailures: string;
};

export const EMPTY_SCHEDULE: ScheduleForm = { action: 'snapshot', cron: '0 3 * * *', timezone: 'UTC', snapshotName: '', retain: '7', maxFailures: '3' };

function count(v: string): number | null {
  const s = v.trim();
  if (!s) return 0;
  const n = Number(s);
  return /^\d+$/.test(s) && n <= 100 ? n : null;
}

export function validateSchedule(f: ScheduleForm): string {
  if (f.cron.trim().split(/\s+/).length !== 5 && !/^@(hourly|daily|weekly|monthly|yearly)$/.test(f.cron.trim()))
    return 'Cron needs five fields (minute hour day month weekday) or @daily.';
  if (f.action === 'snapshot' && count(f.retain) === null) return 'Keep must be a whole number from 0 to 100.';
  if (count(f.maxFailures) === null) return 'Disable after must be a whole number from 0 to 100.';
  if (f.snapshotName.trim().length > 63) return 'Snapshot name is at most 63 characters.';
  return '';
}

export function scheduleBody(project: string, machineId: string, f: ScheduleForm) {
  const body: Record<string, unknown> = {
    project,
    machineId,
    action: f.action,
    cron: f.cron.trim(),
    timezone: f.timezone.trim() || 'UTC',
    maxFailures: count(f.maxFailures) || 0,
  };
  if (f.action === 'snapshot') {
    body.retain = count(f.retain) || 0;
    if (f.snapshotName.trim()) body.snapshotName = f.snapshotName.trim();
  }
  return body;
}

export function scheduleSummary(s: Schedule): string {
  const parts = [`${s.cron} (${s.timezone})`];
  if (s.retain) parts.push(`keeps ${s.retain}`);
  if (s.maxFailures) parts.push(`disables after ${s.maxFailures} failures`);
  if (s.disabledReason) parts.push(s.disabledReason);
  else if (!s.enabled) parts.push('disabled');
  if (s.lastResult) parts.push(`last run ${s.lastResult}${s.lastError ? `: ${s.lastError}` : ''}`);
  return parts.join(' · ');
}
