// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

import { useCallback, useEffect, useState } from 'react';
import { api, q } from '../api';
import { useApp } from '../state';
import { Card, Chip, ListEmpty, PageHero, StatePill } from '../components/kit';
import { labelKeys, snapshotsPath, summaryPath } from '../lib/fleet';
import { ago, fmtMemory, plural } from '../lib/format';
import type { InventorySnapshot, SnapshotInventory, Summary } from '../types';

export default function Fleet() {
  const app = useApp();
  return (
    <div className="page">
      <PageHero eyebrow={`Fleet · ${app.project}`} title="The estate at a glance." lede="Break capacity down by label and find the snapshots nobody is looking after." />
      <GroupedSummary key={`g-${app.project}`} />
      {app.caps?.snapshots && <SnapshotReport key={`s-${app.project}`} />}
    </div>
  );
}

function GroupedSummary() {
  const app = useApp();
  const keys = labelKeys(app.machines);
  const [chosen, setChosen] = useState('');
  const groupBy = chosen && keys.includes(chosen) ? chosen : keys[0] || '';
  const [summary, setSummary] = useState<Summary | null>(null);

  useEffect(() => {
    if (!groupBy) {
      setSummary(null);
      return;
    }
    let cancelled = false;
    api<Summary>(summaryPath(app.project, groupBy))
      .then((s) => !cancelled && setSummary(s))
      .catch((e: Error) => !cancelled && app.toast(e.message));
    return () => {
      cancelled = true;
    };
  }, [groupBy, app.project, app.machines]);

  return (
    <Card
      eyebrow="Summary"
      title="By label"
      actions={
        keys.length > 0 && (
          <select aria-label="Group by label" value={groupBy} onChange={(e) => setChosen(e.target.value)}>
            {keys.map((k) => (
              <option key={k} value={k}>
                {k}
              </option>
            ))}
          </select>
        )
      }
    >
      {!keys.length ? (
        <ListEmpty title="No labels yet">Machines created with labels (for example team=core) can be totalled here.</ListEmpty>
      ) : summary?.groupBy === groupBy && summary.groups ? (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>{groupBy}</th>
                <th>Machines</th>
                <th>Running</th>
                <th className="hide-sm">Stopped</th>
                <th className="hide-sm">Attention</th>
                <th>vCPU</th>
                <th className="hide-sm">Memory</th>
              </tr>
            </thead>
            <tbody>
              {summary.groups.map((g) => (
                <tr key={g.value}>
                  <td>{g.value || <span className="muted">(not set)</span>}</td>
                  <td>{g.machines}</td>
                  <td>{g.running}</td>
                  <td className="hide-sm">{g.stopped}</td>
                  <td className="hide-sm">{g.attention}</td>
                  <td>{g.cpu}</td>
                  <td className="hide-sm">{fmtMemory(g.memoryMiB)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <div className="skeleton skeleton-card" aria-busy="true" />
      )}
    </Card>
  );
}

function SnapshotReport() {
  const app = useApp();
  const [olderThan, setOlderThan] = useState('');
  const [selector, setSelector] = useState('');
  const [query, setQuery] = useState({ olderThan: '', selector: '' });
  const [report, setReport] = useState<SnapshotInventory | null>(null);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    try {
      setReport(await api<SnapshotInventory>(snapshotsPath(app.project, query.olderThan, query.selector)));
      setError('');
    } catch (e) {
      setReport(null);
      setError((e as Error).message);
    }
  }, [app.project, query]);

  useEffect(() => {
    load();
  }, [load]);

  const remove = async (s: InventorySnapshot) => {
    const ok = await app.confirm({
      title: `Delete snapshot “${s.name}”?`,
      message: `Taken from ${s.machineName}. It cannot be undone.`,
      confirmLabel: 'Delete',
      destructive: true,
    });
    if (ok === null) return;
    try {
      await api(`/api/v1/machines/${q(s.machineId)}/snapshots/${q(s.id)}?project=${q(app.project)}`, { method: 'DELETE' });
      app.toast('Snapshot deleted');
    } catch (e) {
      app.toast((e as Error).message);
    }
    await load();
  };

  return (
    <Card eyebrow="Snapshots" title="Oldest first" lede={report ? `${plural(report.total, 'snapshot')} across ${plural(report.machines, 'machine')}.` : undefined}>
      <form
        className="inline-form"
        aria-label="Snapshot filter"
        onSubmit={(e) => {
          e.preventDefault();
          setQuery({ olderThan, selector });
        }}
      >
        <label className="field">
          <span>Age</span>
          <select value={olderThan} onChange={(e) => setOlderThan(e.target.value)}>
            <option value="">Any age</option>
            <option value="1d">Older than 1 day</option>
            <option value="7d">Older than 7 days</option>
            <option value="30d">Older than 30 days</option>
            <option value="90d">Older than 90 days</option>
          </select>
        </label>
        <label className="field">
          <span>Label selector</span>
          <input className="input-field mono" value={selector} placeholder="env=ci" onChange={(e) => setSelector(e.target.value)} />
        </label>
        <button type="submit" className="btn-secondary compact">
          Show
        </button>
      </form>
      {error && (
        <p className="hint-warn" role="alert">
          {error}
        </p>
      )}
      {report?.errors?.length ? (
        <p className="hint-warn" role="alert">
          Partial result: snapshots of {plural(report.errors.length, 'machine')} could not be read ({report.errors[0].error.message}).
        </p>
      ) : null}
      {report &&
        (report.items.length ? (
          <>
            <ul className="snap-list">
              {report.items.map((s) => (
                <li key={`${s.machineId}/${s.id}`}>
                  <div>
                    <strong>{s.name}</strong>
                    <span>
                      <button type="button" className="apple-text-link" onClick={() => app.openDetail(s.machineId)}>
                        {s.machineName}
                      </button>{' '}
                      · {ago(s.createdAt)} {s.scheduleId && <Chip tone="info">retention</Chip>}
                    </span>
                  </div>
                  <StatePill state={s.state} />
                  {app.canOperate && (
                    <div className="snap-actions">
                      <button type="button" className="apple-text-link danger-link" onClick={() => remove(s)}>
                        Delete
                      </button>
                    </div>
                  )}
                </li>
              ))}
            </ul>
            {report.total > report.items.length && (
              <p className="muted">
                Showing the oldest {report.items.length} of {report.total}.
              </p>
            )}
          </>
        ) : (
          <p className="muted">No snapshots match.</p>
        ))}
    </Card>
  );
}
