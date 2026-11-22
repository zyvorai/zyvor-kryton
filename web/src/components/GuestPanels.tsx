// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

import { useCallback, useEffect, useRef, useState } from 'react';
import { api, apiFetch, apiReport, q } from '../api';
import { useApp } from '../state';
import { ago } from '../lib/format';
import {
  buildJobBody,
  encodeSharedPath,
  fmtBytes,
  isJobActive,
  jobTone,
  joinShared,
  parentShared,
  readyPollMs,
  readyView,
  validateJob,
  validSharedName,
  type JobForm,
} from '../lib/guest';
import { Chip, Progress } from './kit';
import type { GuestJob, Machine, Readiness, SharedFile } from '../types';

const base = (m: Machine) => `/api/v1/machines/${q(m.id)}`;
const proj = (m: Machine) => `project=${q(m.project)}`;

function saveBlob(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 10000);
}

/** Install and readiness indicator. Polls GET /ready quickly until the guest answers on RDP. */
export function ReadinessBar({ m, refreshKey }: { m: Machine; refreshKey: number }) {
  const [r, setR] = useState<Readiness | null>(null);
  const [err, setErr] = useState('');
  const rRef = useRef<Readiness | null>(null);

  useEffect(() => {
    let stop = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const tick = async () => {
      if (stop) return;
      if (!document.hidden) {
        try {
          // /ready answers 503 with the same body while not ready, so read it without the throwing helper.
          const body = await apiReport<Readiness & { error?: { message?: string } }>(`${base(m)}/ready?${proj(m)}`);
          if (stop) return;
          if (typeof body.ready === 'boolean') {
            rRef.current = body;
            setR(body);
            setErr('');
          } else setErr(body.error?.message || 'Readiness is not available for this machine.');
        } catch (e) {
          if (!stop) setErr((e as Error).message);
        }
      }
      const ms = readyPollMs(m.state, rRef.current);
      if (!stop && ms) timer = setTimeout(tick, ms);
    };
    tick();
    return () => {
      stop = true;
      if (timer) clearTimeout(timer);
    };
  }, [m.id, m.project, m.state, refreshKey]);

  if (err && !r) return <p className="muted ready-detail">Readiness unavailable: {err}</p>;
  const v = readyView(r);
  return (
    <section className="detail-section ready-bar" aria-label="Install and readiness" aria-live="polite">
      <p className="eyebrow">Readiness</p>
      <div className="ready-label">
        <strong>{v.label}</strong>
        <Chip tone={v.ready ? 'good' : 'warn'}>{v.ready ? 'Ready' : `${v.percent}%`}</Chip>
      </div>
      <div className="detail-progress">
        <Progress value={v.percent} />
      </div>
      {v.detail && <p className="ready-detail">{v.detail}</p>}
    </section>
  );
}

/** Guest jobs: submit, watch, read logs, cancel. Needs a machine created with jobAgent. */
export function JobsPanel({ m, canOperate }: { m: Machine; canOperate: boolean }) {
  const app = useApp();
  const [jobs, setJobs] = useState<GuestJob[]>([]);
  const [error, setError] = useState('');
  const [form, setForm] = useState<JobForm>({ name: '', shell: 'cmd', script: '', timeoutSeconds: '600' });
  const [formErr, setFormErr] = useState('');
  const [busy, setBusy] = useState(false);
  const [logFor, setLogFor] = useState<string | null>(null);
  const [log, setLog] = useState('');
  const jobsRef = useRef<GuestJob[]>([]);

  const load = useCallback(async () => {
    try {
      const res = await api<{ items?: GuestJob[] }>(`${base(m)}/jobs?${proj(m)}`);
      jobsRef.current = res.items || [];
      setJobs(jobsRef.current);
      setError('');
    } catch (e) {
      setError((e as Error).message);
    }
  }, [m.id, m.project]);

  const loadLog = useCallback(
    async (jid: string) => {
      try {
        const res = await apiFetch(`${base(m)}/jobs/${q(jid)}/logs?${proj(m)}`);
        setLog(await res.text());
      } catch (e) {
        setLog(`Could not load the log: ${(e as Error).message}`);
      }
    },
    [m.id, m.project],
  );

  // Poll while any job is queued or running; otherwise load once.
  useEffect(() => {
    let stop = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const tick = async () => {
      if (stop) return;
      await load();
      if (!stop && jobsRef.current.some(isJobActive)) timer = setTimeout(tick, 3000);
    };
    tick();
    return () => {
      stop = true;
      if (timer) clearTimeout(timer);
    };
  }, [load, jobs.length]);

  useEffect(() => {
    if (!logFor) return;
    loadLog(logFor);
    const j = jobs.find((x) => x.id === logFor);
    if (!j || !isJobActive(j)) return;
    const t = setInterval(() => loadLog(logFor), 3000);
    return () => clearInterval(t);
  }, [logFor, jobs.find((x) => x.id === logFor)?.state, loadLog]);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const problem = validateJob(form);
    if (problem) return setFormErr(problem);
    setFormErr('');
    setBusy(true);
    try {
      const j = await api<GuestJob>(`${base(m)}/jobs?${proj(m)}`, { method: 'POST', body: JSON.stringify(buildJobBody(form)) });
      app.toast('Job queued');
      setForm((f) => ({ ...f, script: '', name: '' }));
      setLogFor(j.id);
      await load();
    } catch (err) {
      setFormErr((err as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const cancel = async (j: GuestJob) => {
    try {
      await api(`${base(m)}/jobs/${q(j.id)}/cancel?${proj(m)}`, { method: 'POST' });
      app.toast('Cancel requested');
      await load();
    } catch (err) {
      app.toast((err as Error).message);
    }
  };

  return (
    <section className="detail-section" aria-label="Guest jobs">
      <p className="eyebrow">Jobs</p>
      {error && <p className="muted">{error}</p>}
      {!error && jobs.length === 0 && <p className="muted">No jobs yet.</p>}
      {jobs.length > 0 && (
        <ul className="guest-list">
          {jobs.map((j) => (
            <li key={j.id}>
              <div>
                <strong>{j.name || j.id.slice(0, 8)}</strong>
                <span className="sub">
                  {j.shell} · {j.submittedAt ? ago(j.submittedAt) : ''}
                  {j.exitCode != null && j.state !== 'queued' && j.state !== 'running' ? ` · exit ${j.exitCode}` : ''}
                </span>
              </div>
              <Chip tone={jobTone(j.state)}>{j.state.replace('_', ' ')}</Chip>
              <div className="snap-actions">
                <button type="button" className="apple-text-link" aria-expanded={logFor === j.id} aria-label={`Logs for ${j.name || j.id}`} onClick={() => setLogFor(logFor === j.id ? null : j.id)}>
                  Logs
                </button>
                {canOperate && isJobActive(j) && !j.cancelRequested && (
                  <button type="button" className="apple-text-link danger-link" aria-label={`Cancel ${j.name || j.id}`} onClick={() => cancel(j)}>
                    Cancel
                  </button>
                )}
              </div>
            </li>
          ))}
        </ul>
      )}
      {logFor && (
        <pre className="guest-log" tabIndex={0} aria-label="Job log">
          {log || 'No output yet.'}
        </pre>
      )}
      {canOperate && (
        <form className="guest-form" onSubmit={submit} noValidate aria-label="Submit a job">
          <label className="field">
            <span>Command</span>
            <textarea className="input-field" name="job-script" rows={3} value={form.script} placeholder="ipconfig /all" onChange={(e) => setForm({ ...form, script: e.target.value })} />
          </label>
          <div className="row">
            <label className="field">
              <span>Shell</span>
              <select className="input-field" name="job-shell" value={form.shell} onChange={(e) => setForm({ ...form, shell: e.target.value as JobForm['shell'] })}>
                <option value="cmd">cmd</option>
                <option value="powershell">powershell</option>
              </select>
            </label>
            <label className="field">
              <span>Timeout (s)</span>
              <input className="input-field" name="job-timeout" type="number" min={1} max={86400} value={form.timeoutSeconds} onChange={(e) => setForm({ ...form, timeoutSeconds: e.target.value })} />
            </label>
            <label className="field">
              <span>
                Name <em>optional</em>
              </span>
              <input className="input-field" name="job-name" maxLength={63} value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} autoComplete="off" />
            </label>
          </div>
          {formErr && (
            <p className="login-error" role="alert">
              {formErr}
            </p>
          )}
          <div>
            <button type="submit" className="primary" disabled={busy}>
              {busy ? 'Submitting…' : 'Run job'}
            </button>
          </div>
        </form>
      )}
    </section>
  );
}

/** Shared folder browser: list, upload, download, delete. */
export function SharedPanel({ m, canOperate }: { m: Machine; canOperate: boolean }) {
  const app = useApp();
  const [dir, setDir] = useState('');
  const [items, setItems] = useState<SharedFile[]>([]);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const fileInput = useRef<HTMLInputElement | null>(null);

  const load = useCallback(async () => {
    try {
      const res = await api<{ items?: SharedFile[] }>(`${base(m)}/shared?${proj(m)}${dir ? `&path=${q(dir)}` : ''}`);
      setItems(res.items || []);
      setError('');
    } catch (e) {
      setError((e as Error).message);
    }
  }, [m.id, m.project, dir]);
  useEffect(() => {
    load();
  }, [load]);

  const url = (p: string, extra = '') => `${base(m)}/shared/${encodeSharedPath(p)}?${proj(m)}${extra}`;

  const upload = async (file: File) => {
    if (!validSharedName(file.name)) return app.toast(`"${file.name}" is not a valid file name for the shared folder.`);
    const target = joinShared(dir, file.name);
    setBusy(true);
    try {
      let overwrite = false;
      for (;;) {
        try {
          await apiFetch(url(target, overwrite ? '&overwrite=true' : ''), { method: 'PUT', body: file, headers: { 'Content-Type': 'application/octet-stream' } });
          break;
        } catch (e) {
          if ((e as { status?: number }).status === 409 && !overwrite) {
            const ok = await app.confirm({ title: `Replace ${file.name}?`, message: 'A file with that name already exists in this folder.', confirmLabel: 'Replace' });
            if (ok === null) return;
            overwrite = true;
            continue;
          }
          throw e;
        }
      }
      app.toast(`Uploaded ${file.name}`);
      await load();
    } catch (e) {
      app.toast((e as Error).message);
    } finally {
      setBusy(false);
      if (fileInput.current) fileInput.current.value = '';
    }
  };
  const download = async (f: SharedFile) => {
    try {
      const res = await apiFetch(url(f.path));
      saveBlob(await res.blob(), f.name);
    } catch (e) {
      app.toast((e as Error).message);
    }
  };
  const remove = async (f: SharedFile) => {
    const ok = await app.confirm({ title: `Delete ${f.name}?`, message: f.isDir ? 'Only empty folders can be deleted.' : 'This removes the file from the shared folder.', confirmLabel: 'Delete', destructive: true });
    if (ok === null) return;
    try {
      await apiFetch(url(f.path), { method: 'DELETE' });
      app.toast(`Deleted ${f.name}`);
      await load();
    } catch (e) {
      app.toast((e as Error).message);
    }
  };

  const crumbs = dir.split('/').filter(Boolean);
  return (
    <section className="detail-section" aria-label="Shared folder">
      <p className="eyebrow">Shared folder</p>
      <p className="muted">Guest path \\host.lan\Data (drive Z:).</p>
      <nav className="crumbs" aria-label="Shared folder path">
        <button type="button" className="apple-text-link" onClick={() => setDir('')}>
          shared
        </button>
        {crumbs.map((c, i) => (
          <span key={i}>
            / {' '}
            <button type="button" className="apple-text-link" onClick={() => setDir(crumbs.slice(0, i + 1).join('/'))}>
              {c}
            </button>
          </span>
        ))}
        {dir && (
          <button type="button" className="apple-text-link" onClick={() => setDir(parentShared(dir))} aria-label="Up one folder">
            (up)
          </button>
        )}
      </nav>
      {error && <p className="muted">{error}</p>}
      {!error && items.length === 0 && <p className="muted">This folder is empty.</p>}
      {items.length > 0 && (
        <ul className="guest-list">
          {items.map((f) => (
            <li key={f.path}>
              <div>
                {f.isDir ? (
                  <button type="button" className="apple-text-link" onClick={() => setDir(f.path)} aria-label={`Open folder ${f.name}`}>
                    {f.name}/
                  </button>
                ) : (
                  <strong>{f.name}</strong>
                )}
                <span className="sub">{f.isDir ? 'folder' : fmtBytes(f.size)}</span>
              </div>
              <span />
              <div className="snap-actions">
                {!f.isDir && (
                  <button type="button" className="apple-text-link" aria-label={`Download ${f.name}`} onClick={() => download(f)}>
                    Download
                  </button>
                )}
                {canOperate && (
                  <button type="button" className="apple-text-link danger-link" aria-label={`Delete ${f.name}`} onClick={() => remove(f)}>
                    Delete
                  </button>
                )}
              </div>
            </li>
          ))}
        </ul>
      )}
      {canOperate && (
        <div className="upload-row">
          <label className="btn-secondary buttonlike" style={{ cursor: 'pointer' }}>
            <span>{busy ? 'Uploading…' : 'Upload file'}</span>
            <input ref={fileInput} className="sr-only" type="file" disabled={busy} aria-label="Upload a file to the shared folder" onChange={(e) => e.target.files?.[0] && upload(e.target.files[0])} />
          </label>
        </div>
      )}
    </section>
  );
}
