// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import { useCallback, useEffect, useState, type ReactNode } from 'react';
import { api, q } from '../api';
import { useApp } from '../state';
import { ago, fmtDateTime, fmtMemory } from '../lib/format';
import { machineConsole, sshCommand } from '../lib/console';
import { DefList, Progress, StatePill } from '../components/kit';
import Overlay from '../components/Overlay';
import JobConsole from '../components/JobConsole';
import Icon from '../components/Icon';
import type { Machine, Snapshot } from '../types';

export default function MachineDetail() {
  const app = useApp();
  const id = app.detailId!;
  const [m, setM] = useState<Machine | null>(null);
  const [snaps, setSnaps] = useState<Snapshot[]>([]);
  const [busy, setBusy] = useState('');

  const load = useCallback(async () => {
    try {
      const machine = await api<Machine>(`/api/v1/machines/${q(id)}?project=${q(app.project)}`);
      setM(machine);
      if (app.caps?.snapshots) {
        const res = await api<{ items?: Snapshot[] }>(`/api/v1/machines/${q(id)}/snapshots?project=${q(machine.project || app.project)}`).catch(
          () => ({ items: [] as Snapshot[] }),
        );
        setSnaps(res.items || []);
      }
    } catch (e) {
      app.toast((e as Error).message);
      app.closeDetail();
    }
  }, [id, app.project, app.caps?.snapshots]);

  useEffect(() => {
    load();
  }, [load]);

  // Follow live state from the background refresh while the drawer is open.
  const listed = app.machines.find((x) => x.id === id);
  useEffect(() => {
    if (listed && m && (listed.state !== m.state || listed.ipAddresses?.[0] !== m.ipAddresses?.[0])) load();
  }, [listed?.state, listed?.ipAddresses?.[0]]);

  const img = m ? app.images.find((i) => i.id === m.spec.image) : undefined;
  const linux = img?.os === 'linux';
  const job = m ? app.jobs.find((j) => j.kind === 'machine' && j.id === `machine:${m.id}`) : undefined;
  const consoleUrl = machineConsole(m || undefined, app.project);

  const act = async (label: string, fn: () => Promise<unknown>, done: string) => {
    setBusy(label);
    try {
      await fn();
      app.toast(done);
      await app.refreshProject();
      await load();
    } catch (e) {
      app.toast((e as Error).message);
    } finally {
      setBusy('');
    }
  };

  const path = (suffix = '') => `/api/v1/machines/${q(m!.id)}${suffix}?project=${q(m!.project)}`;

  const power = () => {
    const action = m!.state === 'running' ? 'stop' : 'start';
    return act(action, () => api(path('/' + action), { method: 'POST' }), `${m!.spec.name}: ${action} requested`);
  };
  const snapshot = async () => {
    const name = await app.confirm({ title: 'Take a snapshot', input: { label: 'Snapshot name (optional)', placeholder: 'before-upgrade' }, confirmLabel: 'Snapshot' });
    if (name === null) return;
    act('snapshot', () => api(path('/snapshot'), { method: 'POST', body: JSON.stringify({ name }) }), 'Snapshot requested');
  };
  const restore = async (s: Snapshot) => {
    const ok = await app.confirm({
      title: `Restore ${m!.spec.name}?`,
      message: `The machine is stopped first, then rolled back to “${s.name}”.`,
      confirmLabel: 'Restore',
    });
    if (ok === null) return;
    act('restore', () => api(path(`/snapshots/${q(s.id)}/restore`), { method: 'POST' }), 'Restore requested');
  };
  const deleteSnap = async (s: Snapshot) => {
    const ok = await app.confirm({ title: `Delete snapshot “${s.name}”?`, confirmLabel: 'Delete', destructive: true });
    if (ok === null) return;
    act('snapdel', () => api(path(`/snapshots/${q(s.id)}`), { method: 'DELETE' }), 'Snapshot deleted');
  };
  const remove = async () => {
    const ok = await app.confirm({
      title: `Delete ${m!.spec.name}?`,
      message: 'This removes the provider VM, its disks and owned resources. It cannot be undone.',
      confirmLabel: 'Delete machine',
      destructive: true,
    });
    if (ok === null) return;
    setBusy('delete');
    try {
      await api(path(), { method: 'DELETE' });
      app.toast(`${m!.spec.name} deleted`);
      app.closeDetail();
      await app.refreshProject();
    } catch (e) {
      app.toast((e as Error).message);
      setBusy('');
    }
  };
  const copy = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
      app.toast('Copied ' + text);
    } catch {
      app.toast(text);
    }
  };

  let body: ReactNode = <div className="skeleton skeleton-card" aria-busy="true" />;
  if (m) {
    const ip = m.ipAddresses?.[0];
    const rdpUser = m.rdpUsername || m.spec.dockur?.username || 'Docker';
    const pendingReason = (m.conditions || []).find((c) => c.status === 'False')?.message || m.message || 'The guest is still provisioning.';
    body = (
      <>
        <header className="detail-hero">
          <p className="eyebrow">{linux ? 'Linux machine' : 'Windows machine'} · {m.provider}</p>
          <h2>{m.spec.name}</h2>
          <div className="detail-sub">
            <StatePill state={m.state} />
            <code className="detail-id">{m.id}</code>
          </div>
          {m.message && <p className="detail-message">{m.message}</p>}
        </header>

        {job ? (
          <JobConsole job={job} project={app.project} />
        ) : m.progressPercent != null && m.state !== 'running' ? (
          <div className="detail-progress">
            <Progress value={m.progressPercent} />
            <span>{m.progressPercent}%</span>
          </div>
        ) : null}

        <div className="detail-actions">
          {app.canOperate && (
            <button type="button" className={m.state === 'running' ? 'btn-secondary' : 'primary'} onClick={power} disabled={!!busy}>
              <Icon name="power" size={14} />
              {busy === 'start' || busy === 'stop' ? 'Working…' : m.state === 'running' ? 'Stop' : 'Start'}
            </button>
          )}
          {linux && ip && (
            <button type="button" className="btn-secondary" onClick={() => copy(sshCommand(m, img?.defaultUser))}>
              <Icon name="copy" size={14} />
              Copy SSH
            </button>
          )}
          {m.rdpHost && m.rdpPort && (
            <button type="button" className="btn-secondary" onClick={() => copy(`${m.rdpHost}:${m.rdpPort} (user ${rdpUser})`)}>
              <Icon name="copy" size={14} />
              Copy RDP
            </button>
          )}
          {consoleUrl && (
            <a className="buttonlike btn-secondary" href={consoleUrl} target="_blank" rel="noopener noreferrer">
              <Icon name="terminal" size={14} />
              Console
            </a>
          )}
          {app.caps?.snapshots && app.canOperate && (
            <button type="button" className="btn-secondary" onClick={snapshot} disabled={!!busy}>
              <Icon name="camera" size={14} />
              Snapshot
            </button>
          )}
          {app.canOperate && (
            <button type="button" className="danger" onClick={remove} disabled={!!busy}>
              <Icon name="trash" size={14} />
              {busy === 'delete' ? 'Deleting…' : 'Delete'}
            </button>
          )}
        </div>

        <div className="detail-stats">
          <div>
            <span>vCPU</span>
            <b>{m.spec.compute.cpu}</b>
          </div>
          <div>
            <span>Memory</span>
            <b>{fmtMemory(m.spec.compute.memoryMiB)}</b>
          </div>
          <div>
            <span>Boot disk</span>
            <b>{m.spec.disk.sizeGiB} GiB</b>
          </div>
          <div>
            <span>IP address</span>
            <b className="mono">{ip || '—'}</b>
          </div>
        </div>

        {linux && ip && (
          <div className="ssh-block">
            <span>SSH</span>
            <code>{sshCommand(m, img?.defaultUser)}</code>
          </div>
        )}

        {consoleUrl && m.state === 'running' && (
          <div className="console-frame">
            <div className="console-frame-head">
              <span>Console</span>
              <a href={consoleUrl} target="_blank" rel="noopener noreferrer">
                Full screen
              </a>
            </div>
            <iframe src={consoleUrl} title="Machine console" allow="clipboard-read; clipboard-write" referrerPolicy="no-referrer" />
          </div>
        )}
        {consoleUrl && ['provisioning', 'starting'].includes(m.state) && (
          <div className="console-pending">
            <strong>Console not ready yet</strong>
            <p>{pendingReason}</p>
          </div>
        )}

        {app.caps?.snapshots && (
          <section className="detail-section">
            <p className="eyebrow">Snapshots</p>
            {snaps.length ? (
              <ul className="snap-list">
                {snaps.map((s) => (
                  <li key={s.id}>
                    <div>
                      <strong>{s.name}</strong>
                      <span>{s.message || ago(s.createdAt)}</span>
                    </div>
                    <StatePill state={s.state} />
                    {app.canOperate && (
                      <div className="snap-actions">
                        {s.state === 'ready' && (
                          <button type="button" className="apple-text-link" onClick={() => restore(s)}>
                            Restore
                          </button>
                        )}
                        <button type="button" className="apple-text-link danger-link" onClick={() => deleteSnap(s)}>
                          Delete
                        </button>
                      </div>
                    )}
                  </li>
                ))}
              </ul>
            ) : (
              <p className="muted">No snapshots yet.</p>
            )}
          </section>
        )}

        {m.provider === 'dockur' && m.spec.dockur && <DockurSummary m={m} />}

        <section className="detail-section">
          <p className="eyebrow">Details</p>
          <DefList
            rows={[
              ['Project', m.project],
              ['Image', img ? `${img.name} ${img.version}` : m.spec.image],
              ...(linux ? ([['Login user', m.spec.initialization?.username || img?.defaultUser || '—']] as [string, string][]) : []),
              ['Provider', m.provider],
              ['Provider name', m.providerRef?.name || '—'],
              ['Namespace', m.providerRef?.namespace || '—'],
              ['Network', m.spec.network?.networkId || 'Default network'],
              ...(m.rdpHost && m.rdpPort ? ([['RDP', `${m.rdpHost}:${m.rdpPort} · ${rdpUser}`]] as [string, string][]) : []),
              ['Created', fmtDateTime(m.createdAt)],
              ['Expires', m.expiresAt ? fmtDateTime(m.expiresAt) : 'No TTL'],
            ]}
          />
        </section>
      </>
    );
  }

  return (
    <Overlay kind="drawer" label="Machine details" onClose={app.closeDetail}>
      {body}
    </Overlay>
  );
}

function DockurSummary({ m }: { m: Machine }) {
  const d = m.spec.dockur!;
  const rows: [string, string][] = [];
  const add = (k: string, v?: string | null) => {
    if (v) rows.push([k, v]);
  };
  add('Username', d.username || m.rdpUsername);
  add('Hostname', d.hostname);
  add('Language', d.language);
  add('Region', d.region);
  add('Keyboard', d.keyboard);
  if (d.productKey) add('Product key', '•••••-•••••-•••••-•••••-•••••');
  add('Domain', d.domain);
  add('Domain OU', d.domainOu);
  add('Shared folder', d.sharedDir);
  add('OEM folder', d.oemDir);
  add('Post-install cmd', d.command);
  add('Custom ISO', d.customIso);
  add('Edition', d.edition);
  if (d.extraDisksGiB?.length) add('Extra disks', d.extraDisksGiB.map((g) => `${g} GiB`).join(', '));
  if (d.audio) add('Audio', 'Enabled');
  if (d.secureBoot) add('Secure boot', 'TPM + secure boot');
  if (d.autologin === false) add('Autologin', 'Disabled');
  if (!rows.length) return null;
  return (
    <section className="detail-section">
      <p className="eyebrow">dockur options</p>
      <DefList rows={rows} />
    </section>
  );
}
