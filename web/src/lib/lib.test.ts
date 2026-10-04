// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest';
import { ago, eventDescription, eventTitle, fmtMemory, healthPercent, stateTone } from './format';
import { availability, availabilityLabel, defaultImageId, pickableImages, sortImages, storageFoot, supportsProvider } from './images';
import {
  consoleTicketEndpoint,
  goldenStepIndex,
  jobConsoleEmbed,
  machineConsole,
  sshCommand,
  withConsoleHTML,
  withConsoleTicket,
} from './console';
import { applyImageMinimums, buildCreateBody, parseKeys, validateCreate, type CreateForm } from './create';
import { matchesFilter } from './machines';
import type { Image, Machine } from '../types';

const img = (over: Partial<Image>): Image => ({
  id: 'x',
  name: 'X',
  version: '1',
  family: 'linux',
  description: '',
  minCpu: 1,
  minMemoryMiB: 1024,
  defaultDiskGiB: 10,
  ready: false,
  ...over,
});

const machine = (over: Partial<Machine> = {}): Machine => ({
  id: 'm-1',
  project: 'default',
  provider: 'libvirt',
  state: 'running',
  spec: { name: 'web-01', image: 'ubuntu-24.04', compute: { cpu: 2, memoryMiB: 2048 }, disk: { sizeGiB: 20 } },
  createdAt: '2026-01-01T00:00:00Z',
  ...over,
});

describe('format', () => {
  it('formats memory', () => {
    expect(fmtMemory(512)).toBe('512 MiB');
    expect(fmtMemory(8192)).toBe('8 GB');
    expect(fmtMemory(1536)).toBe('1.5 GB');
  });
  it('formats relative time', () => {
    const now = Date.parse('2026-01-01T01:00:00Z');
    expect(ago(undefined)).toBe('now');
    expect(ago('2026-01-01T00:59:30Z', now)).toBe('30s ago');
    expect(ago('2026-01-01T00:00:00Z', now)).toBe('1h ago');
    expect(ago('not a date', now)).toBe('—');
  });
  it('maps machine states to tones', () => {
    expect(stateTone('running')).toBe('running');
    expect(stateTone('starting')).toBe('provisioning');
    expect(stateTone('unknown')).toBe('failed');
    expect(stateTone('stopped')).toBe('stopped');
    expect(stateTone(undefined)).toBe('neutral');
  });
  it('titles events', () => {
    expect(eventTitle('dev.zyvor.kryton.machine.created')).toBe('Machine Created');
    expect(eventDescription({ id: '1', type: 't', data: { name: 'a', project: 'p' } })).toBe('a · p');
    expect(eventDescription({ id: '1', type: 't', subject: 's' })).toBe('s');
  });
  it('computes health', () => {
    expect(healthPercent(0, 0)).toBe(100);
    expect(healthPercent(4, 3)).toBe(75);
  });
});

describe('images', () => {
  const ubuntu = img({ id: 'ubuntu-24.04', name: 'Ubuntu', os: 'linux', providers: ['libvirt', 'kubevirt'], ready: true, availability: 'stored' });
  const debian = img({ id: 'debian-12', name: 'Debian', os: 'linux', providers: ['libvirt', 'kubevirt'] });
  const win = img({ id: 'windows-11', name: 'Windows 11', family: 'windows-desktop', minMemoryMiB: 8192 });

  it('classifies availability', () => {
    expect(availability(img({ availability: 'on-demand' }))).toBe('on-demand');
    expect(availability(img({ availability: 'weird' }))).toBe('catalog');
    expect(availabilityLabel(debian)).toBe('Fetch required');
    expect(availabilityLabel(win)).toBe('Build required');
  });
  it('mirrors server provider support', () => {
    expect(supportsProvider(win, 'libvirt')).toBe(false);
    expect(supportsProvider(win, 'kubevirt')).toBe(true);
    expect(supportsProvider(win, 'demo')).toBe(true);
    expect(supportsProvider(ubuntu, 'libvirt')).toBe(true);
    expect(supportsProvider(ubuntu, 'dockur')).toBe(false);
  });
  it('sorts stored first then by name', () => {
    expect(sortImages([win, debian, ubuntu]).map((i) => i.id)).toEqual(['ubuntu-24.04', 'debian-12', 'windows-11']);
  });
  it('offers only ready, compatible images when any are ready', () => {
    const r = pickableImages([win, debian, ubuntu], 'libvirt');
    expect(r.anyReady).toBe(true);
    expect(r.items.map((i) => i.id)).toEqual(['ubuntu-24.04']);
    expect(pickableImages([win, debian], 'libvirt').items.map((i) => i.id)).toEqual(['debian-12']);
    expect(defaultImageId([win, debian, ubuntu], 'libvirt', 'windows-11')).toBe('ubuntu-24.04');
  });
  it('describes storage', () => {
    expect(storageFoot(img({ storagePath: '/var/lib/kryton/images/a.qcow2' }))).toBe('/var/lib/kryton/images/a.qcow2');
    expect(storageFoot(debian)).toContain('kryton-image fetch -image debian-12');
  });
});

describe('console', () => {
  it('adds format=html once', () => {
    expect(withConsoleHTML('/c')).toBe('/c?format=html');
    expect(withConsoleHTML('/c?project=a')).toBe('/c?project=a&format=html');
    expect(withConsoleHTML('/c?format=raw')).toBe('/c?format=raw');
  });
  it('builds machine console urls', () => {
    expect(machineConsole(machine(), 'default')).toBe('');
    expect(machineConsole(machine({ consoleUrl: 'http://10.0.0.1:8006' }), 'default')).toBe('/api/v1/machines/m-1/console/?project=default&format=html');
    expect(jobConsoleEmbed({ id: 'machine:m-1', kind: 'machine', name: 'n', state: 'running', consoleUrl: 'http://x' }, 'p')).toBe(
      '/api/v1/machines/m-1/console/?project=p&format=html',
    );
  });
  it('mints tickets only for in-app console urls', () => {
    expect(consoleTicketEndpoint('/api/v1/machines/m-1/console/?project=p&format=html')).toBe('/api/v1/machines/m-1/console-ticket?project=p');
    expect(consoleTicketEndpoint('/api/v1/machines/m-1/vnc')).toBe('/api/v1/machines/m-1/console-ticket?project=default');
    expect(consoleTicketEndpoint('/api/v1/machines/m-1/snapshots')).toBe('');
    expect(consoleTicketEndpoint('http://10.0.0.1:8006')).toBe('');
    expect(withConsoleTicket('/c?project=p', 'a.b')).toBe('/c?project=p&console_ticket=a.b');
    expect(withConsoleTicket('/c', '')).toBe('/c');
  });
  it('maps golden phases to steps', () => {
    expect(goldenStepIndex(null)).toBe(0);
    expect(goldenStepIndex({ id: '1', state: 'starting' })).toBe(1);
    expect(goldenStepIndex({ id: '1', state: 'installing' })).toBe(3);
    expect(goldenStepIndex({ id: '1', state: 'ready' })).toBe(6);
  });
  it('builds ssh commands', () => {
    expect(sshCommand(machine({ ipAddresses: ['192.168.122.5'] }), 'ubuntu')).toBe('ssh ubuntu@192.168.122.5');
    const custom = machine({ ipAddresses: ['10.0.0.2'] });
    custom.spec.initialization = { username: 'ops' };
    expect(sshCommand(custom, 'ubuntu')).toBe('ssh ops@10.0.0.2');
  });
});

describe('create', () => {
  const linux = img({ id: 'ubuntu-24.04', os: 'linux', ready: true, minCpu: 2, minMemoryMiB: 2048, defaultDiskGiB: 20 });
  const base: CreateForm = {
    name: 'web-01',
    project: 'default',
    image: 'ubuntu-24.04',
    cpu: 1,
    memory: 1024,
    disk: 10,
    storageClass: '',
    ttl: 0,
    network: '',
    linuxUsername: '',
    linuxSSHKeys: 'ssh-ed25519 AAAA a@b\n\n  ssh-rsa BBBB c@d  ',
    dockur: { username: 'Docker', password: 'admin', extraDisks: '32, x, 64' },
  };
  it('parses keys', () => {
    expect(parseKeys(base.linuxSSHKeys)).toEqual(['ssh-ed25519 AAAA a@b', 'ssh-rsa BBBB c@d']);
  });
  it('raises resources to image minimums', () => {
    const f = applyImageMinimums(base, linux);
    expect([f.cpu, f.memory, f.disk]).toEqual([2, 2048, 20]);
  });
  it('validates', () => {
    expect(validateCreate(base, linux)).toBeNull();
    expect(validateCreate({ ...base, name: 'Bad_Name' }, linux)).toMatch(/lowercase/);
    expect(validateCreate(base, { ...linux, ready: false })).toMatch(/not ready/);
    expect(validateCreate({ ...base, linuxUsername: 'Root' }, linux)).toMatch(/username/);
  });
  it('builds a linux body with initialization only', () => {
    const b = buildCreateBody(base, linux, 'libvirt') as Record<string, any>;
    expect(b.initialization.sshAuthorizedKeys).toHaveLength(2);
    expect(b.dockur).toBeUndefined();
    expect(b.disk).toEqual({ sizeGiB: 10 });
  });
  it('builds dockur options', () => {
    const b = buildCreateBody({ ...base, storageClass: ' fast ' }, img({ id: 'w', ready: true }), 'dockur') as Record<string, any>;
    expect(b.initialization).toBeUndefined();
    expect(b.dockur.extraDisksGiB).toEqual([32, 64]);
    expect(b.disk.storageClass).toBe('fast');
  });
});

describe('machines filter', () => {
  it('filters by state and search', () => {
    const m = machine({ ipAddresses: ['192.168.122.9'] });
    expect(matchesFilter(m, 'all', '')).toBe(true);
    expect(matchesFilter(m, 'stopped', '')).toBe(false);
    expect(matchesFilter(machine({ state: 'starting' }), 'provisioning', '')).toBe(true);
    expect(matchesFilter(machine({ state: 'unknown' }), 'failed', '')).toBe(true);
    expect(matchesFilter(m, 'all', '122.9')).toBe(true);
    expect(matchesFilter(m, 'all', 'UBUNTU')).toBe(true);
    expect(matchesFilter(m, 'all', 'nope')).toBe(false);
  });
});
