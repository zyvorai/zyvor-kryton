// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

import { useCallback, useEffect, useState } from 'react';
import { api, ApiError, q } from '../api';
import { useApp } from '../state';
import { EMPTY_SCHEDULE, scheduleBody, scheduleSummary, validateSchedule, type ScheduleForm } from '../lib/fleet';
import { fmtDateTime } from '../lib/format';
import type { List, Machine, Schedule } from '../types';

/** A machine's scheduled actions, with snapshot retention and run-now. */
export default function SchedulesPanel({ m, onRan }: { m: Machine; onRan: () => void }) {
  const app = useApp();
  const [items, setItems] = useState<Schedule[] | null>(null);
  const [hidden, setHidden] = useState(false);
  const [adding, setAdding] = useState(false);
  const [form, setForm] = useState<ScheduleForm>({ ...EMPTY_SCHEDULE, action: app.caps?.snapshots ? 'snapshot' : 'stop' });
  const [busy, setBusy] = useState('');

  const load = useCallback(async () => {
    try {
      const res = await api<List<Schedule>>(`/api/v1/schedules?project=${q(m.project)}&machineId=${q(m.id)}`);
      setItems(res.items || []);
    } catch (e) {
      if ((e as ApiError).code === 'NOT_CONFIGURED') setHidden(true);
      else app.toast((e as Error).message);
    }
  }, [m.id, m.project]);

  useEffect(() => {
    load();
  }, [load]);

  const act = async (key: string, fn: () => Promise<unknown>, done: string) => {
    setBusy(key);
    try {
      await fn();
      app.toast(done);
    } catch (e) {
      app.toast((e as Error).message);
    } finally {
      setBusy('');
      await load();
    }
  };

  const url = (s: Schedule, suffix = '') => `/api/v1/schedules/${q(s.id)}${suffix}`;
  const run = (s: Schedule) =>
    act(
      s.id,
      async () => {
        try {
          await api(url(s, '/run'), { method: 'POST' });
        } finally {
          onRan();
        }
      },
      `${s.action} ran`,
    );
  const toggle = (s: Schedule) =>
    act(s.id, () => api(url(s), { method: 'PUT', body: JSON.stringify({ enabled: !s.enabled }) }), s.enabled ? 'Schedule disabled' : 'Schedule enabled');
  const remove = async (s: Schedule) => {
    const ok = await app.confirm({ title: `Delete the ${s.action} schedule?`, message: 'Snapshots it already took are kept.', confirmLabel: 'Delete', destructive: true });
    if (ok === null) return;
    act(s.id, () => api(url(s), { method: 'DELETE' }), 'Schedule deleted');
  };

  const problem = validateSchedule(form);
  const set = (k: keyof ScheduleForm) => (e: { target: { value: string } }) => setForm((f) => ({ ...f, [k]: e.target.value }));
  const create = async () => {
    if (problem) return;
    setBusy('new');
    try {
      await api('/api/v1/schedules', { method: 'POST', body: JSON.stringify(scheduleBody(m.project, m.id, form)) });
      app.toast('Schedule created');
      setAdding(false);
    } catch (e) {
      app.toast((e as Error).message);
    } finally {
      setBusy('');
      await load();
    }
  };

  if (hidden || !items) return null;
  return (
    <section className="detail-section">
      <p className="eyebrow">Schedules</p>
      {items.length ? (
        <ul className="snap-list">
          {items.map((s) => (
            <li key={s.id}>
              <div>
                <strong>{s.action}</strong>
                <span>{scheduleSummary(s)}</span>
                <span>{s.enabled && s.nextRunAt ? `Next ${fmtDateTime(s.nextRunAt)}` : 'Not scheduled'}</span>
              </div>
              {app.canOperate && (
                <div className="snap-actions">
                  <button type="button" className="apple-text-link" disabled={!!busy} onClick={() => run(s)}>
                    {busy === s.id ? 'Working…' : 'Run now'}
                  </button>
                  <button type="button" className="apple-text-link" disabled={!!busy} onClick={() => toggle(s)}>
                    {s.enabled ? 'Disable' : 'Enable'}
                  </button>
                  <button type="button" className="apple-text-link danger-link" disabled={!!busy} onClick={() => remove(s)}>
                    Delete
                  </button>
                </div>
              )}
            </li>
          ))}
        </ul>
      ) : (
        <p className="muted">No scheduled actions.</p>
      )}
      {app.canOperate && !adding && (
        <button type="button" className="apple-text-link" onClick={() => setAdding(true)}>
          Add schedule
        </button>
      )}
      {adding && (
        <form
          className="inline-form"
          onSubmit={(e) => {
            e.preventDefault();
            create();
          }}
        >
          <label className="field">
            <span>Action</span>
            <select value={form.action} onChange={set('action')}>
              {app.caps?.snapshots && <option value="snapshot">Snapshot</option>}
              <option value="start">Start</option>
              <option value="stop">Stop</option>
              <option value="restart">Restart</option>
            </select>
          </label>
          <label className="field">
            <span>Cron</span>
            <input className="input-field mono" value={form.cron} onChange={set('cron')} />
          </label>
          <label className="field">
            <span>Timezone</span>
            <input className="input-field" value={form.timezone} onChange={set('timezone')} />
          </label>
          {form.action === 'snapshot' && (
            <>
              <label className="field">
                <span>Snapshot name (optional)</span>
                <input className="input-field" value={form.snapshotName} onChange={set('snapshotName')} />
              </label>
              <label className="field">
                <span>Keep newest (0 = all)</span>
                <input className="input-field" inputMode="numeric" value={form.retain} onChange={set('retain')} />
              </label>
            </>
          )}
          <label className="field">
            <span>Disable after failures (0 = never)</span>
            <input className="input-field" inputMode="numeric" value={form.maxFailures} onChange={set('maxFailures')} />
          </label>
          {problem && (
            <p className="hint-warn" role="alert">
              {problem}
            </p>
          )}
          <div className="bulk-row">
            <button type="submit" className="primary compact" disabled={!!problem || !!busy}>
              Create schedule
            </button>
            <button type="button" className="btn-secondary compact" onClick={() => setAdding(false)}>
              Cancel
            </button>
          </div>
        </form>
      )}
    </section>
  );
}
