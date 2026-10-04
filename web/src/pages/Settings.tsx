// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import { useEffect, useState } from 'react';
import { api, apiReport } from '../api';
import { useApp } from '../state';
import { Card, Chip, DefList, FindingRow, PageHero } from '../components/kit';
import JobConsole from '../components/JobConsole';
import type { AtlasTest, ConnectionTest, Settings, StorageInventory } from '../types';

const yes = (v?: boolean) => (v ? 'Available' : 'Not advertised');

export default function SettingsPage({ onSignOut }: { onSignOut: () => void }) {
  const app = useApp();
  useEffect(() => {
    app.loadSettings();
    app.loadStorage();
  }, []);
  const caps = app.caps;
  return (
    <div className="page">
      <PageHero
        eyebrow="Control plane"
        title="Settings"
        lede="Identity, provider capabilities, connection health and runtime configuration for this Kryton instance."
      />
      <div className="grid-3">
        <Card eyebrow="Identity" title={app.me?.name || '—'}>
          <DefList
            rows={[
              ['Role', app.me?.role || '—'],
              ['Projects', (app.me?.projects || app.projects).join(', ')],
            ]}
          />
        </Card>
        <Card eyebrow="Provider" title={caps?.provider || '—'}>
          <DefList
            rows={[
              ['Snapshots', yes(caps?.snapshots)],
              ['Console', yes(caps?.console)],
              ['Networks', yes(caps?.networks)],
              ['TTL cleanup', yes(caps?.ttl)],
              ['Live migration', yes(caps?.liveMigration)],
              ['Golden images', yes(caps?.goldenImages)],
            ]}
          />
        </Card>
        <Card eyebrow="API access" title="Bearer token">
          <p>This tab keeps its API key in session storage only. Services should use their own project-scoped key.</p>
          <div className="card-footer-actions">
            <button type="button" className="btn-secondary" onClick={onSignOut}>
              Sign out
            </button>
            <a className="buttonlike btn-secondary" href="/openapi.yaml" target="_blank" rel="noopener noreferrer">
              OpenAPI
            </a>
          </div>
        </Card>
      </div>
      <ConnectionCard />
      <RuntimeCard />
      <AtlasCard />
      {caps?.provider === 'kubevirt' && <StorageCard />}
    </div>
  );
}

function ConnectionCard() {
  const app = useApp();
  const [test, setTest] = useState<ConnectionTest | null>(null);
  const [busy, setBusy] = useState(false);
  const run = async () => {
    setBusy(true);
    try {
      const r = await apiReport<ConnectionTest>('/api/v1/settings/test', 'POST');
      setTest(r);
      if (r.doctor) app.setDoctor(r.doctor);
      app.toast(r.healthy ? 'Connection OK' : 'Connection issues found');
    } catch (e) {
      app.toast((e as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const d = test || app.settings;
  const doctor = d?.doctor || app.doctor;
  const sys = app.settings?.system;
  return (
    <Card
      eyebrow="Connection"
      title="Control plane health"
      lede={
        d?.healthy === false
          ? 'Some checks failed — fix them before creating machines.'
          : d?.healthy === true
            ? 'All connection checks passed.'
            : 'Verify the provider, storage and install scripts before creating machines.'
      }
      actions={
        <button type="button" className="primary" onClick={run} disabled={busy}>
          {busy ? 'Testing…' : 'Test connection'}
        </button>
      }
    >
      {!!d?.connection?.probes?.length && (
        <div className="finding-list">
          {d.connection.probes.map((p) => (
            <FindingRow key={p.name} status={p.status} title={p.name} message={`${p.message}${p.latencyMs ? ` · ${p.latencyMs}ms` : ''}`} hint={p.hint} />
          ))}
        </div>
      )}
      {!!doctor?.findings?.length && (
        <>
          <p className="eyebrow subhead">Doctor</p>
          <div className="finding-list">
            {doctor.findings.map((f) => (
              <FindingRow key={f.check + f.message} status={f.status} title={f.check} message={f.message} hint={f.hint} />
            ))}
          </div>
        </>
      )}
      {sys && (
        <>
          <p className="eyebrow subhead">System</p>
          <DefList
            rows={[
              ['Provider', sys.provider],
              ['Auth', sys.authMode],
              ['Kubernetes', sys.kubernetesConnected ? sys.kubernetesEndpoint || 'connected' : 'not connected'],
              ['Projects', (sys.projects || []).join(', ')],
              ['Install scripts', sys.scriptsAvailable ? 'available' : 'not on host'],
            ]}
          />
        </>
      )}
    </Card>
  );
}

function RuntimeCard() {
  const app = useApp();
  const s = app.settings;
  const rt = s?.runtime || {};
  const sys = s?.system || {};
  const [form, setForm] = useState({ defaultProject: '', imageNamespace: '', storageClass: '', eventWebhookUrl: '' });
  useEffect(() => {
    setForm({
      defaultProject: rt.defaultProject || sys.defaultProjectEnv || '',
      imageNamespace: rt.imageNamespace || sys.imageNamespaceEnv || '',
      storageClass: rt.storageClass || '',
      eventWebhookUrl: rt.eventWebhookUrl || '',
    });
  }, [s]);
  if (!s) return <Card eyebrow="Runtime" title="Operator settings" lede="Loading operator settings…" />;
  const kubevirt = app.caps?.provider === 'kubevirt';
  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    const body: Record<string, string> = { defaultProject: form.defaultProject, storageClass: form.storageClass, eventWebhookUrl: form.eventWebhookUrl };
    if (kubevirt) body.imageNamespace = form.imageNamespace;
    try {
      const next = await api<Settings>('/api/v1/settings', { method: 'PUT', body: JSON.stringify(body) });
      app.setSettings(next);
      if (next.doctor) app.setDoctor(next.doctor);
      app.toast('Settings saved');
      app.loadStorage();
    } catch (err) {
      app.toast((err as Error).message);
    }
  };
  return (
    <Card eyebrow="Runtime" title="Operator settings" lede="Persisted on this krytond host and applied without a restart.">
      <form onSubmit={save}>
        <div className="form-grid">
          <label className="field">
            <span>Default project</span>
            <select className="input-field" value={form.defaultProject} onChange={(e) => setForm({ ...form, defaultProject: e.target.value })}>
              {(sys.projects || []).map((p) => (
                <option key={p}>{p}</option>
              ))}
            </select>
          </label>
          {kubevirt && (
            <label className="field">
              <span>CDI image namespace</span>
              <input className="input-field" value={form.imageNamespace} placeholder="kryton-images" onChange={(e) => setForm({ ...form, imageNamespace: e.target.value })} />
            </label>
          )}
          {kubevirt && (
            <label className="field">
              <span>VM storage class</span>
              <input className="input-field" value={form.storageClass} placeholder="rook-ceph-block" onChange={(e) => setForm({ ...form, storageClass: e.target.value })} />
            </label>
          )}
          <label className="field wide">
            <span>Event webhook URL</span>
            <input className="input-field" value={form.eventWebhookUrl} placeholder="https://…" onChange={(e) => setForm({ ...form, eventWebhookUrl: e.target.value })} />
          </label>
        </div>
        <div className="card-footer-actions">
          {app.canOperate && (
            <button type="submit" className="primary">
              Save settings
            </button>
          )}
          <small className="muted">
            Saved to <code>{sys.settingsConfigFile || '~/.kryton/settings.json'}</code>. Provider, auth mode, TLS and projects come from the service
            environment and need a restart.
          </small>
        </div>
      </form>
    </Card>
  );
}

function AtlasCard() {
  const app = useApp();
  const s = app.settings;
  const a = s?.runtime?.atlas || {};
  const [form, setForm] = useState({ enabled: false, baseUrl: '', token: '', product: 'kryton', preferAtlas: false });
  const [test, setTest] = useState<AtlasTest | null>(null);
  useEffect(() => {
    setForm({ enabled: !!a.enabled, baseUrl: a.baseUrl || '', token: '', product: a.product || 'kryton', preferAtlas: !!a.preferAtlas });
  }, [s]);
  if (!s) return null;
  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    try {
      const next = await api<Settings>('/api/v1/settings', {
        method: 'PUT',
        body: JSON.stringify({ atlas: { ...form, baseUrl: form.baseUrl.trim(), token: form.token.trim(), product: form.product.trim() || 'kryton' } }),
      });
      app.setSettings(next);
      app.toast(form.enabled ? 'Atlas integration saved' : 'Atlas integration disabled');
    } catch (err) {
      app.toast((err as Error).message);
    }
  };
  const runTest = async () => {
    try {
      const r = await apiReport<AtlasTest>('/api/v1/integrations/atlas/test', 'POST', {
        enabled: true,
        baseUrl: form.baseUrl.trim(),
        token: form.token.trim(),
        product: form.product.trim() || 'kryton',
      });
      setTest(r);
      app.toast(r.healthy ? 'Atlas connection OK' : 'Atlas connection issues');
    } catch (err) {
      app.toast((err as Error).message);
    }
  };
  return (
    <Card eyebrow="Integrations" title="Atlas storage control plane" lede="Let VM disks use Atlas-managed Ceph StorageClasses with product ownership.">
      <form onSubmit={save}>
        <div className="form-grid">
          <label className="wide">
            <input type="checkbox" checked={form.enabled} onChange={(e) => setForm({ ...form, enabled: e.target.checked })} /> Enable Atlas integration
          </label>
          <label className="field wide">
            <span>Atlas base URL</span>
            <input className="input-field" value={form.baseUrl} placeholder="http://127.0.0.1:5110" onChange={(e) => setForm({ ...form, baseUrl: e.target.value })} />
          </label>
          <label className="field">
            <span>Atlas token</span>
            <input
              className="input-field"
              type="password"
              autoComplete="off"
              value={form.token}
              placeholder={s.runtime?.atlasTokenSet ? '•••••••• (blank keeps it)' : 'Bearer JWT from Atlas'}
              onChange={(e) => setForm({ ...form, token: e.target.value })}
            />
          </label>
          <label className="field">
            <span>Owner product id</span>
            <input className="input-field" value={form.product} onChange={(e) => setForm({ ...form, product: e.target.value })} />
          </label>
          <label className="wide">
            <input type="checkbox" checked={form.preferAtlas} onChange={(e) => setForm({ ...form, preferAtlas: e.target.checked })} /> Prefer Atlas
            StorageClasses for VM disks
          </label>
        </div>
        <div className="card-footer-actions">
          {app.canOperate && (
            <button type="submit" className="primary">
              Save Atlas
            </button>
          )}
          <button type="button" className="btn-secondary" onClick={runTest}>
            Test Atlas
          </button>
        </div>
      </form>
      {test && (
        <div className="finding-list">
          {(test.probes || []).map((p) => (
            <FindingRow key={p.name} status={p.status} title={p.name} message={`${p.message}${p.latencyMs ? ` · ${p.latencyMs}ms` : ''}`} hint={p.hint} />
          ))}
          {!!test.storageClasses?.length && <p className="muted">Classes: {test.storageClasses.join(', ')}</p>}
        </div>
      )}
    </Card>
  );
}

function StorageCard() {
  const app = useApp();
  const inv = app.storage;
  const [selected, setSelected] = useState('');
  const [rookMode, setRookMode] = useState('lab');
  const [device, setDevice] = useState('');
  const [wipe, setWipe] = useState(false);
  const [logs, setLogs] = useState<string[]>([]);
  const setupState = inv?.setup?.state;
  useEffect(() => {
    if (!selected) setSelected(inv?.config?.storageClass || inv?.storageClasses?.find((c) => c.backend === 'rook-ceph')?.name || '');
  }, [inv]);
  useEffect(() => {
    if (!setupState || setupState === 'idle') return;
    api<{ logs?: string[] }>('/api/v1/storage/setup')
      .then((r) => setLogs(r.logs || []))
      .catch(() => {});
  }, [inv]);
  if (!inv) return <Card eyebrow="VM disks" title="Cluster storage" lede="Loading cluster StorageClasses…" />;
  const running = setupState === 'running';
  const installed = inv.backendsInstalled || {};
  const startSetup = async (body: Record<string, unknown>) => {
    try {
      await api('/api/v1/storage/setup', { method: 'POST', body: JSON.stringify(body) });
      app.toast('Storage setup started');
      await Promise.all([app.loadStorage(), app.loadJobs()]);
    } catch (e) {
      app.toast((e as Error).message);
    }
  };
  const saveClass = async () => {
    try {
      const cfg = await api<StorageInventory['config']>('/api/v1/storage/config', { method: 'PUT', body: JSON.stringify({ storageClass: selected }) });
      app.toast(cfg?.storageClass ? `VM disks use ${cfg.storageClass}` : 'Cleared storage class default');
      await app.loadStorage();
    } catch (e) {
      app.toast((e as Error).message);
    }
  };
  const classes = inv.storageClasses || [];
  return (
    <Card eyebrow="VM disks" title="Cluster storage" lede="Install Rook Ceph or Longhorn, then pick the default StorageClass for new disks and snapshots.">
      {inv.scriptsAvailable && (
        <div className="grid-2 storage-install">
          <div className="subcard">
            <h3>Longhorn</h3>
            <p>{installed.longhorn ? 'Already installed on this cluster.' : 'Single-node CSI backend with snapshots — good for lab hosts.'}</p>
            <button
              type="button"
              className={installed.longhorn ? 'btn-secondary' : 'primary'}
              disabled={!!installed.longhorn || running || !app.canOperate}
              onClick={() => startSetup({ backend: 'longhorn' })}
            >
              {installed.longhorn ? 'Installed' : 'Install Longhorn'}
            </button>
          </div>
          <div className="subcard">
            <h3>Rook Ceph RBD</h3>
            <p>{installed['rook-ceph'] ? 'Already installed on this cluster.' : 'Production-style RBD disks and CSI snapshots.'}</p>
            <label className="field">
              <span>Rook mode</span>
              <select className="input-field" value={rookMode} onChange={(e) => setRookMode(e.target.value)}>
                <option value="lab">Lab — directory OSD on this node</option>
                <option value="pool-only">Pool only — operator already running</option>
                <option value="device">Device — dedicated partition</option>
              </select>
            </label>
            {rookMode === 'device' && (
              <>
                <label className="field">
                  <span>OSD device</span>
                  <select className="input-field" value={device} onChange={(e) => setDevice(e.target.value)}>
                    <option value="">Select partition…</option>
                    {(inv.blockDevices || []).map((d) => (
                      <option key={d.path} value={d.path} disabled={d.blocked}>
                        {d.path} · {d.size}
                        {d.model ? ` · ${d.model}` : ''}
                        {d.blocked ? ' (blocked)' : d.recommended ? ' · recommended' : ''}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  <input type="checkbox" checked={wipe} onChange={(e) => setWipe(e.target.checked)} /> Wipe device signatures first
                </label>
              </>
            )}
            <button
              type="button"
              className={installed['rook-ceph'] ? 'btn-secondary' : 'primary'}
              disabled={!!installed['rook-ceph'] || running || !app.canOperate}
              onClick={() => {
                if (rookMode === 'device' && !device) {
                  app.toast('Pick an OSD device partition');
                  return;
                }
                startSetup({ backend: 'rook-ceph', rookMode, setDefault: true, wipeDevice: wipe, ...(rookMode === 'device' ? { device } : {}) });
              }}
            >
              {installed['rook-ceph'] ? 'Installed' : 'Install Rook Ceph'}
            </button>
          </div>
        </div>
      )}
      {setupState && setupState !== 'idle' && (
        <JobConsole
          project={app.project}
          job={{
            id: 'storage',
            kind: 'storage',
            name: running ? 'Installing cluster storage…' : `Storage setup · ${setupState}`,
            state: setupState,
            logs: logs.slice(-80).map((l) => ({ message: l, level: /\[ERR\]|error/i.test(l) ? 'err' : l.includes('[OK]') ? 'ok' : 'info' })),
          }}
        />
      )}
      <p className="eyebrow subhead">Default for new VM disks</p>
      {classes.length ? (
        <div className="choice-list" role="radiogroup" aria-label="Storage class">
          {classes.map((c) => (
            <button
              key={c.name}
              type="button"
              role="radio"
              aria-checked={selected === c.name}
              disabled={c.backend === 'local-path'}
              className={`choice-row${selected === c.name ? ' selected' : ''}`}
              onClick={() => setSelected(c.name)}
            >
              <span className="choice-dot" aria-hidden />
              <span className="choice-copy">
                <strong>{c.name}</strong>
                <span>
                  {c.provisioner}
                  {c.snapshotClass ? ` · snapshot: ${c.snapshotClass}` : ''}
                  {c.volumeBindingMode ? ` · ${c.volumeBindingMode}` : ''}
                </span>
              </span>
              <span className="choice-chips">
                <Chip tone={c.backend === 'rook-ceph' ? 'info' : 'neutral'}>{c.backend}</Chip>
                <Chip tone={c.snapshotCapable ? 'good' : 'warn'}>{c.snapshotCapable ? 'Snapshots' : 'No CSI snapshots'}</Chip>
              </span>
            </button>
          ))}
        </div>
      ) : (
        <p className="muted">No StorageClasses found yet. Install Rook Ceph or Longhorn above, then refresh.</p>
      )}
      <div className="card-footer-actions">
        {app.canOperate && (
          <button type="button" className="primary" disabled={!selected || selected === (inv.config?.storageClass || '')} onClick={saveClass}>
            Use selected class
          </button>
        )}
        <button type="button" className="btn-refresh" onClick={() => app.loadStorage()}>
          Refresh
        </button>
        <small className="muted">{inv.config?.storageClass ? `Active default: ${inv.config.storageClass}` : 'No default set.'}</small>
      </div>
    </Card>
  );
}
