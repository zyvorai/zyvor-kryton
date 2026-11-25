// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

import { useState } from 'react';
import { api } from '../api';
import { useApp } from '../state';
import { acceptedIds, allows, bulkBody, bulkHeadline } from '../lib/fleet';
import { plural } from '../lib/format';
import type { BulkAction, BulkResponse, Machine } from '../types';

const ACTIONS: { action: BulkAction; label: string }[] = [
  { action: 'start', label: 'Start' },
  { action: 'stop', label: 'Stop' },
  { action: 'snapshot', label: 'Snapshot' },
  { action: 'delete', label: 'Delete' },
];

/**
 * Bulk actions for the selected machines. Every action is previewed with a dry
 * run first; only the machines the preview accepted are then changed.
 */
export default function BulkBar({ selected, onClear }: { selected: Machine[]; onClear: () => void }) {
  const app = useApp();
  // Both are tagged with what they were asked for, so a stale one is ignored.
  const [asked, setAsked] = useState<{ target: string; res: BulkResponse } | null>(null);
  const [done, setDone] = useState<{ project: string; res: BulkResponse } | null>(null);
  const [snapshotName, setSnapshotName] = useState('');
  const [busy, setBusy] = useState(false);
  const target = `${app.project}:${selected.map((m) => m.id).join(',')}`;
  // Once the target differs the preview is gone for good, even if it returns.
  const [seen, setSeen] = useState(target);
  if (seen !== target) {
    setSeen(target);
    setAsked(null);
    if (selected.length) setDone(null);
  }
  const preview = asked?.target === target ? asked.res : null;
  const result = !selected.length && done?.project === app.project ? done.res : null;

  const send = async (action: BulkAction, ids: string[], dryRun: boolean) => {
    setBusy(true);
    try {
      return await api<BulkResponse>('/api/v1/machines/actions', {
        method: 'POST',
        body: JSON.stringify(bulkBody(app.project, action, ids, { dryRun, snapshotName })),
      });
    } catch (e) {
      app.toast((e as Error).message);
      return null;
    } finally {
      setBusy(false);
    }
  };

  const runPreview = async (action: BulkAction) => {
    setDone(null);
    const res = await send(action, selected.map((m) => m.id), true);
    setAsked(res ? { target, res } : null);
  };

  const apply = async () => {
    if (!preview) return;
    const res = await send(preview.action, acceptedIds(preview), false);
    if (!res) return;
    setAsked(null);
    setDone({ project: app.project, res });
    app.toast(bulkHeadline(res));
    onClear();
    await app.refreshProject().catch((e: Error) => app.toast(e.message));
  };

  const shown = preview || result;
  if (!selected.length && !shown) return null;
  const targets = preview ? acceptedIds(preview).length : 0;

  return (
    <section className="card bulk-bar" aria-label="Bulk actions">
      {selected.length > 0 && (
        <div className="bulk-row">
          <strong>{plural(selected.length, 'machine')} selected</strong>
          {ACTIONS.filter((a) => a.action !== 'snapshot' || app.caps?.snapshots).map((a) => (
            <button
              key={a.action}
              type="button"
              className={a.action === 'delete' ? 'danger compact' : 'btn-secondary compact'}
              disabled={busy || !selected.some((m) => allows(m, a.action))}
              onClick={() => runPreview(a.action)}
            >
              {a.label}…
            </button>
          ))}
          <button type="button" className="apple-text-link" onClick={onClear}>
            Clear selection
          </button>
        </div>
      )}
      {shown && (
        <div className="bulk-results">
          <p role="status">{bulkHeadline(shown)}</p>
          <ul>
            {shown.results.map((r) => (
              <li key={r.id} className={r.ok ? '' : 'bulk-failed'}>
                <span>{r.name || r.id}</span>
                <span>{r.ok ? (shown.dryRun ? 'ready' : r.state || 'done') : r.error?.message || r.error?.code || 'failed'}</span>
              </li>
            ))}
          </ul>
          {preview ? (
            <div className="bulk-row">
              {preview.action === 'snapshot' && (
                <input
                  className="input-field bulk-name"
                  aria-label="Snapshot name"
                  placeholder="Snapshot name (optional)"
                  value={snapshotName}
                  maxLength={63}
                  onChange={(e) => setSnapshotName(e.target.value)}
                />
              )}
              {preview.action === 'delete' && <span className="bulk-warning">Deleting removes disks. It cannot be undone.</span>}
              <button type="button" className={preview.action === 'delete' ? 'danger compact' : 'primary compact'} disabled={busy || !targets} onClick={apply}>
                {busy ? 'Working…' : `Confirm ${preview.action} of ${plural(targets, 'machine')}`}
              </button>
              <button type="button" className="btn-secondary compact" onClick={() => setAsked(null)}>
                Cancel
              </button>
            </div>
          ) : (
            <button type="button" className="apple-text-link" onClick={() => setDone(null)}>
              Dismiss
            </button>
          )}
        </div>
      )}
    </section>
  );
}
