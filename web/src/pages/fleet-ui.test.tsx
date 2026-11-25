// @vitest-environment jsdom
// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

// Renders the real console against a fake krytond to check the fleet screens
// send the right requests.

import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import App from '../App';
import type { Machine } from '../types';

type Call = { method: string; url: string; body: any };

const machine = (id: string, name: string, over: Partial<Machine> = {}): Machine => ({
  id,
  project: 'default',
  provider: 'demo',
  state: 'running',
  spec: { name, image: 'x', compute: { cpu: 2, memoryMiB: 2048 }, disk: { sizeGiB: 20 }, labels: { team: 'core' } },
  createdAt: '2026-01-01T00:00:00Z',
  allowedActions: ['stop', 'snapshot', 'delete'],
  ...over,
});

let calls: Call[] = [];
let machines: Machine[] = [];

const json = (body: unknown, status = 200) => new Response(status === 204 ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

function server(url: string, method: string, body: any): Response {
  const u = new URL(url, 'http://k');
  const p = u.pathname;
  if (p === '/api/v1/me') return json({ name: 'op', role: 'operator' });
  if (p === '/api/v1/projects') return json({ items: ['default'] });
  if (p === '/api/v1/images') return json({ items: [] });
  if (p === '/api/v1/capabilities') return json({ provider: 'demo', snapshots: true });
  if (p === '/api/v1/summary')
    return json({
      project: 'default', provider: 'demo', machines: 2, running: 2, stopped: 0, attention: 0, cpu: 4, memoryMiB: 4096,
      ...(u.searchParams.get('groupBy')
        ? { groupBy: u.searchParams.get('groupBy'), groups: [{ value: 'core', machines: 2, running: 2, stopped: 0, attention: 0, cpu: 4, memoryMiB: 4096 }] }
        : {}),
    });
  if (p === '/api/v1/machines/actions') {
    const results = (body.ids as string[]).map((id) =>
      id === 'm2' && body.action === 'delete'
        ? { id, name: 'db', ok: false, error: { code: 'MACHINE_PROTECTED', message: 'machine is delete protected' } }
        : { id, name: id === 'm1' ? 'web' : 'db', ok: true },
    );
    const succeeded = results.filter((r) => r.ok).length;
    return json({ project: 'default', action: body.action, dryRun: body.dryRun, total: results.length, succeeded, failed: results.length - succeeded, results });
  }
  if (p === '/api/v1/machines') return json({ items: u.searchParams.get('selector') ? machines.slice(0, 1) : machines });
  const one = p.match(/^\/api\/v1\/machines\/([^/]+)$/);
  if (one) return json(machines.find((m) => m.id === one[1]));
  if (p.endsWith('/protection')) return json({ protected: body.protected });
  if (p === '/api/v1/snapshots')
    return json({ project: 'default', total: 1, machines: 2, items: [{ id: 's1', machineId: 'm1', machineName: 'web', name: 'nightly-1', state: 'ready', createdAt: '2026-01-01T00:00:00Z', scheduleId: 'sc' }] });
  if (p === '/api/v1/schedules' && method === 'GET')
    return json({ items: [{ id: 'sc', machineId: 'm1', action: 'snapshot', cron: '@daily', timezone: 'UTC', enabled: true, retain: 3 }] });
  if (p.startsWith('/api/v1/schedules')) return json({ id: 'sc' }, method === 'POST' && p === '/api/v1/schedules' ? 201 : 200);
  if (p === '/api/v1/events/stream') return new Response('', { status: 200 });
  if (p.endsWith('/snapshots') || p === '/api/v1/events' || p === '/api/v1/jobs' || p === '/api/v1/golden') return json({ items: [] });
  return json({ error: { message: 'not found', code: 'NOT_FOUND' } }, 404);
}

beforeEach(() => {
  calls = [];
  machines = [machine('m1', 'web'), machine('m2', 'db', { allowedActions: ['stop', 'snapshot'], protection: { reason: 'prod' } })];
  sessionStorage.setItem('kryton.token', 't');
  vi.stubGlobal('fetch', async (url: string, opts: RequestInit = {}) => {
    const method = opts.method || 'GET';
    const body = typeof opts.body === 'string' ? JSON.parse(opts.body) : undefined;
    calls.push({ method, url, body });
    return server(url, method, body);
  });
  window.scrollTo = () => {};
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.location.hash = '';
});

async function open(page: string) {
  window.location.hash = `/${page}`;
  render(<App />);
}

const posted = (path: string) => calls.filter((c) => c.method !== 'GET' && c.url.startsWith(path));
const click = async (el: HTMLElement) => {
  await act(async () => {
    fireEvent.click(el);
  });
};

describe('machines page', () => {
  it('previews a bulk delete, then deletes only what the preview accepted', async () => {
    await open('machines');
    await click(await screen.findByLabelText('Select all shown machines'));
    const bar = await screen.findByRole('region', { name: 'Bulk actions' });
    await click(within(bar).getByRole('button', { name: 'Delete…' }));
    await screen.findByText('Preview: 1 of 2 machines can delete, 1 cannot.');
    expect(posted('/api/v1/machines/actions').map((c) => c.body)).toEqual([{ project: 'default', action: 'delete', ids: ['m1', 'm2'], dryRun: true }]);
    await click(screen.getByRole('button', { name: 'Confirm delete of 1 machine' }));
    await waitFor(() => expect(posted('/api/v1/machines/actions')).toHaveLength(2));
    expect(posted('/api/v1/machines/actions')[1].body).toEqual({ project: 'default', action: 'delete', ids: ['m1'], confirm: true });
  });

  it('drops a preview when the selection changes', async () => {
    await open('machines');
    await click(await screen.findByLabelText('Select web'));
    await click(await screen.findByRole('button', { name: 'Stop…' }));
    await screen.findByRole('button', { name: 'Confirm stop of 1 machine' });
    await click(screen.getByLabelText('Select db'));
    expect(screen.queryByRole('button', { name: /^Confirm stop/ })).toBeNull();
    // Returning to the old selection does not bring the old preview back.
    await click(screen.getByLabelText('Select db'));
    expect(screen.queryByRole('button', { name: /^Confirm stop/ })).toBeNull();
    // Ticking a row does not open the detail drawer.
    expect(screen.queryByRole('dialog', { name: 'Machine details' })).toBeNull();
  });

  it('queries the server by selector and state', async () => {
    await open('machines');
    await screen.findByLabelText('Select web');
    await click(await screen.findByRole('button', { name: 'Query' }));
    const form = screen.getByRole('form', { name: 'Machine query' });
    fireEvent.change(within(form).getByLabelText('Label selector'), { target: { value: 'team=core' } });
    await click(within(form).getByLabelText('running'));
    await click(within(form).getByRole('button', { name: 'Apply query' }));
    await waitFor(() => expect(screen.queryByLabelText('Select db')).toBeNull());
    expect(calls.some((c) => c.url === '/api/v1/machines?project=default&selector=team%3Dcore&state=running&limit=500')).toBe(true);
    await click(within(form).getByRole('button', { name: 'Reset' }));
    await screen.findByLabelText('Select db');
  });
});

describe('machine detail', () => {
  it('disables delete for a protected machine and removes protection', async () => {
    await open('machines');
    await click((await screen.findByText('db')).closest('tr')!);
    const drawer = await screen.findByRole('dialog', { name: 'Machine details' });
    await within(drawer).findByRole('note');
    const del = drawer.querySelector<HTMLButtonElement>('.detail-actions button.danger')!;
    expect(del.disabled).toBe(true);
    expect(del.title).toMatch(/protected/i);
    await click(within(drawer).getByRole('button', { name: 'Remove protection' }));
    const confirm = await screen.findByRole('dialog', { name: 'Remove protection from db?' });
    await click(within(confirm).getByRole('button', { name: 'Remove protection' }));
    await waitFor(() => expect(posted('/api/v1/machines/m2/protection').map((c) => c.body)).toEqual([{ protected: false }]));
  });

  it('runs a schedule now and creates one with retention', async () => {
    await open('machines');
    await click((await screen.findByText('web')).closest('tr')!);
    const drawer = await screen.findByRole('dialog', { name: 'Machine details' });
    await click(await within(drawer).findByRole('button', { name: 'Run now' }));
    await waitFor(() => expect(posted('/api/v1/schedules/sc/run')).toHaveLength(1));
    await click(within(drawer).getByRole('button', { name: 'Add schedule' }));
    fireEvent.change(within(drawer).getByLabelText('Keep newest (0 = all)'), { target: { value: '101' } });
    expect((within(drawer).getByRole('button', { name: 'Create schedule' }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(within(drawer).getByLabelText('Keep newest (0 = all)'), { target: { value: '5' } });
    await click(within(drawer).getByRole('button', { name: 'Create schedule' }));
    await waitFor(() => expect(posted('/api/v1/schedules').filter((c) => c.url === '/api/v1/schedules')).toHaveLength(1));
    expect(posted('/api/v1/schedules').pop()!.body).toMatchObject({ machineId: 'm1', action: 'snapshot', retain: 5, maxFailures: 3 });
  });
});

describe('fleet and activity pages', () => {
  it('shows the label summary and filters the snapshot inventory', async () => {
    await open('fleet');
    await screen.findByRole('cell', { name: 'core' });
    expect(calls.some((c) => c.url === '/api/v1/summary?project=default&groupBy=team')).toBe(true);
    await screen.findByText('nightly-1');
    const form = screen.getByRole('form', { name: 'Snapshot filter' });
    fireEvent.change(within(form).getByLabelText('Age'), { target: { value: '7d' } });
    await click(within(form).getByRole('button', { name: 'Show' }));
    await waitFor(() => expect(calls.some((c) => c.url === '/api/v1/snapshots?project=default&olderThan=7d&limit=200')).toBe(true));
  });

  it('opens a filtered history and stream', async () => {
    await open('activity');
    const group = await screen.findByRole('group', { name: 'Event filters' });
    await act(async () => {
      fireEvent.change(within(group).getByLabelText('Machine'), { target: { value: 'm1' } });
    });
    await waitFor(() => {
      expect(calls.some((c) => c.url === '/api/v1/events/stream?machineId=m1')).toBe(true);
      expect(calls.some((c) => c.url === '/api/v1/events?limit=100&machineId=m1')).toBe(true);
    });
  });
});
