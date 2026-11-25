// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

import { describe, expect, it } from 'vitest';
import {
  EMPTY_FILTER, EMPTY_SCHEDULE, NO_EVENT_FILTER, acceptedIds, allows, bulkBody, bulkHeadline, eventFilterActive, eventsPath, fetchAll,
  filterActive, labelKeys, machinesPath, mergeEvent, mergeHistory, scheduleBody, scheduleSummary, snapshotsPath, streamPath, summaryPath,
  validateSchedule,
} from './fleet';
import { readEventStream } from './sse';
import type { BulkResponse, CloudEvent, Machine } from '../types';

const machine = (over: Partial<Machine> = {}): Machine => ({
  id: 'm-1',
  project: 'default',
  provider: 'demo',
  state: 'running',
  spec: { name: 'web-01', image: 'x', compute: { cpu: 2, memoryMiB: 2048 }, disk: { sizeGiB: 20 } },
  createdAt: '2026-01-01T00:00:00Z',
  ...over,
});

describe('machine queries', () => {
  it('builds the list path with every server filter encoded', () => {
    expect(machinesPath('lab')).toBe('/api/v1/machines?project=lab&limit=500');
    expect(machinesPath('lab', { selector: ' env in (ci,qa) ', name: 'a&b', states: ['running', 'stopped'] })).toBe(
      '/api/v1/machines?project=lab&selector=env%20in%20(ci%2Cqa)&name=a%26b&state=running%2Cstopped&limit=500',
    );
    expect(filterActive(EMPTY_FILTER)).toBe(false);
    expect(filterActive({ ...EMPTY_FILTER, selector: '  ' })).toBe(false);
    expect(filterActive({ ...EMPTY_FILTER, states: ['failed'] })).toBe(true);
  });

  it('follows cursors until the last page and stops at the page cap', async () => {
    const seen: string[] = [];
    const items = await fetchAll<number>('/x?limit=2', async (p) => {
      seen.push(p);
      return seen.length === 1 ? { items: [1, 2], nextCursor: 'a b' } : { items: [3] };
    });
    expect(items).toEqual([1, 2, 3]);
    expect(seen).toEqual(['/x?limit=2', '/x?limit=2&cursor=a%20b']);
    let calls = 0;
    await fetchAll('/x', async () => ({ items: [++calls], nextCursor: 'again' }), 3);
    expect(calls).toBe(3);
  });
});

describe('allowed actions', () => {
  it('trusts the server list', () => {
    const m = machine({ allowedActions: ['stop'] });
    expect(allows(m, 'stop')).toBe(true);
    expect(allows(m, 'delete')).toBe(false);
    expect(allows(machine({ allowedActions: [] }), 'stop')).toBe(false);
  });
  it('falls back to state and never offers delete on a protected machine', () => {
    expect(allows(machine(), 'stop')).toBe(true);
    expect(allows(machine(), 'start')).toBe(false);
    expect(allows(machine({ state: 'stopped' }), 'start')).toBe(true);
    expect(allows(machine({ state: 'provisioning' }), 'snapshot')).toBe(false);
    expect(allows(machine({ state: 'running', protection: {} }), 'delete')).toBe(false);
    expect(allows(machine({ state: 'deleting' }), 'delete')).toBe(false);
    expect(allows(machine({ state: 'provisioning' }), 'delete')).toBe(true);
  });
});

describe('bulk actions', () => {
  it('confirms only a real delete', () => {
    expect(bulkBody('lab', 'delete', ['a'], { dryRun: true })).toEqual({ project: 'lab', action: 'delete', ids: ['a'], dryRun: true });
    expect(bulkBody('lab', 'delete', ['a'], { dryRun: false })).toEqual({ project: 'lab', action: 'delete', ids: ['a'], confirm: true });
    expect(bulkBody('lab', 'stop', ['a'], { dryRun: false, snapshotName: 'x' })).toEqual({ project: 'lab', action: 'stop', ids: ['a'] });
    expect(bulkBody('lab', 'snapshot', ['a'], { dryRun: false, snapshotName: ' pre ' })).toMatchObject({ snapshotName: 'pre' });
  });
  it('targets only what the preview accepted', () => {
    const r: BulkResponse = {
      project: 'lab', action: 'stop', dryRun: true, total: 2, succeeded: 1, failed: 1,
      results: [{ id: 'a', ok: true }, { id: 'b', ok: false }],
    };
    expect(acceptedIds(r)).toEqual(['a']);
    expect(bulkHeadline(r)).toBe('Preview: 1 of 2 machines can stop, 1 cannot.');
    expect(bulkHeadline({ ...r, dryRun: false, failed: 0 })).toBe('stop: 1 succeeded.');
  });
});

describe('summary and snapshots', () => {
  it('collects label keys', () => {
    const l = (labels: Record<string, string>) => machine({ spec: { ...machine().spec, labels } });
    expect(labelKeys([l({ team: 'a', env: 'ci' }), l({ env: 'p' }), machine()])).toEqual(['env', 'team']);
  });
  it('builds paths', () => {
    expect(summaryPath('lab', 'app/tier')).toBe('/api/v1/summary?project=lab&groupBy=app%2Ftier');
    expect(snapshotsPath('lab', '', ' ')).toBe('/api/v1/snapshots?project=lab&limit=200');
    expect(snapshotsPath('lab', '7d', 'env=ci')).toBe('/api/v1/snapshots?project=lab&olderThan=7d&selector=env%3Dci&limit=200');
  });
});

describe('events', () => {
  const now = Date.parse('2026-03-04T12:00:00.500Z');
  it('sends type, machine and since to the history, and no since to the stream', () => {
    const f = { type: 'io.kryton.machine.*', machineId: 'm-1', sinceHours: 1 };
    expect(eventsPath(f, now)).toBe('/api/v1/events?limit=100&type=io.kryton.machine.*&machineId=m-1&since=2026-03-04T11%3A00%3A00Z');
    expect(streamPath(f)).toBe('/api/v1/events/stream?type=io.kryton.machine.*&machineId=m-1');
    expect(streamPath(NO_EVENT_FILTER)).toBe('/api/v1/events/stream');
    expect(eventFilterActive(NO_EVENT_FILTER)).toBe(false);
  });
  it('merges without duplicates, newest first', () => {
    const e = (id: string, time: string): CloudEvent => ({ id, type: 't', time });
    const a = e('a', '2026-01-01T00:00:00Z');
    const b = e('b', '2026-01-02T00:00:00Z');
    expect(mergeEvent([a, b], a).map((x) => x.id)).toEqual(['a', 'b']);
    expect(mergeHistory([a], [b, a]).map((x) => x.id)).toEqual(['b', 'a']);
  });
  it('reads frames split across chunks and skips noise', async () => {
    const enc = new TextEncoder();
    const chunks = [': hello\n\nid: 1\nevent: cloudevent\nda', 'ta: {"id":"1","type":"x"}\n\nevent: cloudevent\ndata: {bad\n\n', 'event: cloudevent\ndata: {"id":"2","type":"y"}\n\n'];
    const body = new ReadableStream<Uint8Array>({
      start(c) {
        chunks.forEach((s) => c.enqueue(enc.encode(s)));
        c.close();
      },
    });
    const got: string[] = [];
    await readEventStream(body, (ev) => got.push(ev.id));
    expect(got).toEqual(['1', '2']);
  });
});

describe('schedules', () => {
  it('validates the form', () => {
    expect(validateSchedule(EMPTY_SCHEDULE)).toBe('');
    expect(validateSchedule({ ...EMPTY_SCHEDULE, cron: '@daily', retain: '', maxFailures: '' })).toBe('');
    expect(validateSchedule({ ...EMPTY_SCHEDULE, cron: '* *' })).toMatch(/five fields/);
    for (const bad of ['-1', '101', '1.5', 'x', '1e1']) {
      expect(validateSchedule({ ...EMPTY_SCHEDULE, retain: bad })).toMatch(/Keep/);
      expect(validateSchedule({ ...EMPTY_SCHEDULE, maxFailures: bad })).toMatch(/Disable/);
    }
    expect(validateSchedule({ ...EMPTY_SCHEDULE, action: 'stop', retain: 'x' })).toBe('');
  });
  it('sends retention only for snapshots', () => {
    expect(scheduleBody('lab', 'm', { ...EMPTY_SCHEDULE, snapshotName: 'n' })).toEqual({
      project: 'lab', machineId: 'm', action: 'snapshot', cron: '0 3 * * *', timezone: 'UTC', maxFailures: 3, retain: 7, snapshotName: 'n',
    });
    const stop = scheduleBody('lab', 'm', { ...EMPTY_SCHEDULE, action: 'stop', snapshotName: 'n' });
    expect(stop).not.toHaveProperty('retain');
    expect(stop).not.toHaveProperty('snapshotName');
  });
  it('summarises why a schedule is off', () => {
    const base = { id: 's', machineId: 'm', action: 'snapshot' as const, cron: '@daily', timezone: 'UTC', enabled: false };
    expect(scheduleSummary({ ...base, retain: 2, disabledReason: 'disabled after 3 consecutive failed runs' })).toBe(
      '@daily (UTC) · keeps 2 · disabled after 3 consecutive failed runs',
    );
    expect(scheduleSummary({ ...base, enabled: true, lastResult: 'error', lastError: 'boom' })).toBe('@daily (UTC) · last run error: boom');
  });
});
