// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import { useMemo, useState } from 'react';
import { api } from '../api';
import { useApp } from '../state';
import { applyImageMinimums, buildCreateBody, validateCreate, type CreateForm } from '../lib/create';
import { defaultImageId, pickableImages } from '../lib/images';
import Overlay from '../components/Overlay';
import { ImageOption } from '../components/ImageCard';
import { Segmented } from '../components/kit';
import type { Machine } from '../types';

type OsFilter = 'all' | 'linux' | 'windows';

export default function CreateMachine() {
  const app = useApp();
  const provider = app.caps?.provider;
  const { items, anyReady } = useMemo(() => pickableImages(app.images, provider), [app.images, provider]);
  const hasLinux = items.some((i) => i.os === 'linux');
  const hasWindows = items.some((i) => i.os !== 'linux');
  const initialImage = app.createFor?.imageId && items.some((i) => i.id === app.createFor?.imageId) ? app.createFor.imageId : defaultImageId(app.images, provider);
  const initialImg = items.find((i) => i.id === initialImage);
  const [os, setOs] = useState<OsFilter>('all');
  const [form, setForm] = useState<CreateForm>(() =>
    applyImageMinimums(
      {
        name: initialImg?.os === 'linux' ? 'linux-01' : 'win-01',
        project: app.project,
        image: initialImage,
        cpu: 2,
        memory: 2048,
        disk: 20,
        storageClass: '',
        ttl: 0,
        network: '',
        linuxUsername: '',
        linuxSSHKeys: '',
        dockur: { username: 'Docker', password: 'admin' },
      },
      initialImg,
    ),
  );
  const [error, setError] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const img = items.find((i) => i.id === form.image);
  const visible = items.filter((i) => os === 'all' || (os === 'linux' ? i.os === 'linux' : i.os !== 'linux'));
  const storageClasses = (app.storage?.storageClasses || []).filter((c) => c.backend !== 'local-path');
  const activeSC = app.storage?.config?.storageClass || '';

  const set = <K extends keyof CreateForm>(k: K, v: CreateForm[K]) => {
    setForm((f) => ({ ...f, [k]: v }));
    if (error) setError('');
  };
  const setDockur = (k: string, v: string | boolean) => setForm((f) => ({ ...f, dockur: { ...f.dockur, [k]: v } }));
  const selectImage = (id: string) => {
    const next = items.find((i) => i.id === id);
    if (next && !next.ready && anyReady) return;
    setForm((f) => applyImageMinimums({ ...f, image: id }, next));
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const problem = validateCreate(form, img);
    if (problem) {
      setError(problem);
      return;
    }
    setSubmitting(true);
    try {
      const m = await api<Machine>('/api/v1/machines', { method: 'POST', body: JSON.stringify(buildCreateBody(form, img, provider)) });
      app.toast(`${m.spec.name} is being provisioned`);
      app.closeCreate();
      if (form.project !== app.project) app.setProject(form.project);
      else await app.refreshProject();
      await app.loadJobs();
      app.openDetail(m.id);
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Overlay kind="sheet" label="Create machine" onClose={app.closeCreate}>
      <form className="create-form" onSubmit={submit} noValidate>
        <p className="eyebrow">New machine · {provider}</p>
        <h2>Create a machine</h2>
        <p className="sheet-lede">Kryton provisions it through {provider || 'the configured provider'} and keeps the API contract the same everywhere.</p>

        <div className="create-section">
          <div className="create-section-head">
            <h3>Operating system</h3>
            {hasLinux && hasWindows && (
              <Segmented<OsFilter>
                label="OS filter"
                value={os}
                onChange={setOs}
                options={[
                  { value: 'all', label: 'All' },
                  { value: 'linux', label: 'Linux' },
                  { value: 'windows', label: 'Windows' },
                ]}
              />
            )}
          </div>
          {visible.length ? (
            <div className="image-picker" role="radiogroup" aria-label="Operating system image">
              {visible.map((i) => (
                <ImageOption key={i.id} img={i} selected={i.id === form.image} disabled={!i.ready && anyReady} onSelect={selectImage} />
              ))}
            </div>
          ) : (
            <p className="muted">No images are available for {provider}.</p>
          )}
          {!anyReady && (
            <p className="hint-warn">
              No image is ready on this host yet.{' '}
              {provider === 'libvirt' ? (
                <>
                  Fetch one first, e.g. <code>kryton-image fetch -image ubuntu-24.04 -sha256 …</code>.
                </>
              ) : provider === 'kubevirt' ? (
                'Build a golden image or run scripts/bootstrap-kubevirt-images.sh.'
              ) : (
                'Prepare an image before deploying.'
              )}
            </p>
          )}
        </div>

        <div className="create-section">
          <h3>Machine</h3>
          <div className="form-grid">
            <label className="field wide">
              <span>Name</span>
              <input className="input-field" name="name" value={form.name} onChange={(e) => set('name', e.target.value)} required autoComplete="off" data-autofocus />
            </label>
            {app.projects.length > 1 && (
              <label className="field">
                <span>Project</span>
                <select className="input-field" name="project" value={form.project} onChange={(e) => set('project', e.target.value)}>
                  {app.projects.map((p) => (
                    <option key={p}>{p}</option>
                  ))}
                </select>
              </label>
            )}
            <label className="field">
              <span>vCPU</span>
              <input className="input-field" name="cpu" type="number" min={1} max={256} value={form.cpu} onChange={(e) => set('cpu', Number(e.target.value))} />
            </label>
            <label className="field">
              <span>Memory (MiB)</span>
              <input className="input-field" name="memory" type="number" min={512} step={512} value={form.memory} onChange={(e) => set('memory', Number(e.target.value))} />
            </label>
            <label className="field">
              <span>Boot disk (GiB)</span>
              <input className="input-field" name="disk" type="number" min={1} value={form.disk} onChange={(e) => set('disk', Number(e.target.value))} />
            </label>
            {app.caps?.ttl && (
              <label className="field">
                <span>
                  TTL (minutes) <em>0 = keep</em>
                </span>
                <input className="input-field" name="ttl" type="number" min={0} value={form.ttl} onChange={(e) => set('ttl', Number(e.target.value))} />
              </label>
            )}
            {storageClasses.length > 0 && (
              <label className="field">
                <span>
                  Storage class <em>optional</em>
                </span>
                <select className="input-field" name="storageClass" value={form.storageClass} onChange={(e) => set('storageClass', e.target.value)}>
                  <option value="">{activeSC ? `Default (${activeSC})` : 'Default'}</option>
                  {storageClasses.map((c) => (
                    <option key={c.name} value={c.name}>
                      {[c.name, c.backend, c.snapshotCapable ? 'snapshots' : ''].filter(Boolean).join(' · ')}
                    </option>
                  ))}
                </select>
              </label>
            )}
            {app.caps?.networks && (
              <label className="field wide">
                <span>
                  Network <em>optional</em>
                </span>
                <input
                  className="input-field"
                  name="network"
                  value={form.network}
                  placeholder={provider === 'kubevirt' ? 'namespace/network-attachment-definition' : 'Provider default'}
                  onChange={(e) => set('network', e.target.value)}
                />
              </label>
            )}
          </div>
        </div>

        {img?.os === 'linux' && (
          <div className="create-section">
            <h3>Linux access</h3>
            <p className="muted">Password login is disabled. Add a public key to SSH in once the guest has an IP.</p>
            <div className="form-grid">
              <label className="field">
                <span>
                  Username <em>default {img.defaultUser || 'image user'}</em>
                </span>
                <input
                  className="input-field"
                  name="linuxUsername"
                  value={form.linuxUsername}
                  placeholder={img.defaultUser || ''}
                  onChange={(e) => set('linuxUsername', e.target.value)}
                  autoComplete="off"
                />
              </label>
              <label className="field wide">
                <span>SSH public keys · one per line</span>
                <textarea
                  className="input-field"
                  name="sshAuthorizedKeys"
                  rows={3}
                  value={form.linuxSSHKeys}
                  placeholder="ssh-ed25519 AAAA… you@laptop"
                  onChange={(e) => set('linuxSSHKeys', e.target.value)}
                />
              </label>
            </div>
          </div>
        )}

        {provider === 'dockur' && (
          <details className="create-section create-advanced">
            <summary>
              <h3>dockur options</h3>
              <span className="muted">Defaults: user Docker / password admin</span>
            </summary>
            <div className="form-grid">
              {(
                [
                  ['username', 'Username'],
                  ['hostname', 'Hostname'],
                  ['language', 'Language'],
                  ['region', 'Region'],
                  ['keyboard', 'Keyboard'],
                  ['edition', 'Edition'],
                  ['domain', 'Domain'],
                  ['domainOu', 'Domain OU'],
                ] as const
              ).map(([k, label]) => (
                <label key={k} className="field">
                  <span>{label}</span>
                  <input className="input-field" name={`dockur-${k}`} value={String(form.dockur[k] || '')} onChange={(e) => setDockur(k, e.target.value)} />
                </label>
              ))}
              <label className="field">
                <span>Password</span>
                <input className="input-field" name="dockur-password" type="password" value={form.dockur.password || ''} autoComplete="new-password" onChange={(e) => setDockur('password', e.target.value)} />
              </label>
              <label className="field">
                <span>Extra disks (GiB)</span>
                <input className="input-field" name="dockur-extraDisks" value={form.dockur.extraDisks || ''} placeholder="32,64" onChange={(e) => setDockur('extraDisks', e.target.value)} />
              </label>
              {(
                [
                  ['productKey', 'Product key'],
                  ['sharedDir', 'Shared host folder'],
                  ['oemDir', 'OEM folder'],
                  ['command', 'Post-install command'],
                  ['customIso', 'Custom ISO URL or path'],
                ] as const
              ).map(([k, label]) => (
                <label key={k} className="field wide">
                  <span>{label}</span>
                  <input className="input-field" name={`dockur-${k}`} value={String(form.dockur[k] || '')} onChange={(e) => setDockur(k, e.target.value)} />
                </label>
              ))}
              <label className="wide">
                <input type="checkbox" checked={!!form.dockur.audio} onChange={(e) => setDockur('audio', e.target.checked)} /> Web viewer audio
              </label>
              <label className="wide">
                <input type="checkbox" checked={!!form.dockur.secureBoot} onChange={(e) => setDockur('secureBoot', e.target.checked)} /> Secure boot + TPM
              </label>
              <label className="wide">
                <input type="checkbox" checked={!!form.dockur.noAutologin} onChange={(e) => setDockur('noAutologin', e.target.checked)} /> Require the login screen
              </label>
            </div>
          </details>
        )}

        {error && (
          <p className="login-error" role="alert">
            {error}
          </p>
        )}
        <div className="sheet-footer">
          <button type="button" className="btn-secondary" onClick={app.closeCreate}>
            Cancel
          </button>
          <button type="submit" className="primary" disabled={submitting || !app.canOperate || !img?.ready}>
            {submitting ? 'Creating…' : 'Create machine'}
          </button>
        </div>
      </form>
    </Overlay>
  );
}
