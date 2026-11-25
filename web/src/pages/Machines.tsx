// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

import { useEffect, useMemo, useState } from 'react';
import { api } from '../api';
import { useApp } from '../state';
import { fmtMemory } from '../lib/format';
import { Chip, ListEmpty, PageHero, Segmented, StatePill } from '../components/kit';
import BulkBar from '../components/BulkBar';
import Icon from '../components/Icon';
import { matchesFilter, type MachineFilter as Filter } from '../lib/machines';
import { EMPTY_FILTER, MACHINE_STATES, MAX_BULK, fetchAll, filterActive, machinesPath, type ServerFilter } from '../lib/fleet';
import type { List, Machine } from '../types';

export default function Machines() {
  const app = useApp();
  const [filter, setFilter] = useState<Filter>('all');
  const [search, setSearch] = useState('');
  const [refreshing, setRefreshing] = useState(false);
  const [showServer, setShowServer] = useState(false);
  const [draft, setDraft] = useState<ServerFilter>(EMPTY_FILTER);
  const [applied, setApplied] = useState<ServerFilter>(EMPTY_FILTER);
  const [reply, setReply] = useState<{ scope: string; items: Machine[]; error: string } | null>(null);
  const [ticked, setTicked] = useState<{ scope: string; ids: Set<string> }>({ scope: '', ids: new Set() });

  const serverActive = filterActive(applied);
  // Query results and ticks belong to one project and query; others are ignored.
  const scope = JSON.stringify([app.project, applied]);
  const current = reply?.scope === scope ? reply : null;
  const matched = serverActive ? (current?.items ?? null) : null;
  const filterError = serverActive ? current?.error || '' : '';
  const selection = ticked.scope === scope ? ticked.ids : new Set<string>();
  const setSelection = (ids: Set<string>) => setTicked({ scope, ids });

  // Server results follow the background refresh, so they never go stale.
  useEffect(() => {
    if (!serverActive) return;
    let cancelled = false;
    fetchAll<Machine>(machinesPath(app.project, applied), (p) => api<List<Machine>>(p))
      .then((ms) => !cancelled && setReply({ scope, items: ms, error: '' }))
      .catch((e: Error) => !cancelled && setReply({ scope, items: [], error: e.message }));
    return () => {
      cancelled = true;
    };
  }, [scope, app.machines]);

  const source = serverActive ? matched || [] : app.machines;
  const items = useMemo(() => source.filter((m) => matchesFilter(m, filter, search)), [source, filter, search]);
  const count = (f: Filter) => source.filter((m) => matchesFilter(m, f, '')).length;
  // Only machines on screen can be selected, so nothing hidden is ever acted on.
  const selected = items.filter((m) => selection.has(m.id));
  const selectable = items.slice(0, MAX_BULK);
  const allSelected = selectable.length > 0 && selectable.every((m) => selection.has(m.id));

  const toggle = (id: string) => {
    const next = new Set(selected.map((m) => m.id));
    if (next.has(id)) next.delete(id);
    else if (next.size < MAX_BULK) next.add(id);
    else app.toast(`At most ${MAX_BULK} machines can be selected.`);
    setSelection(next);
  };

  const toggleState = (s: string) =>
    setDraft((d) => ({ ...d, states: d.states.includes(s) ? d.states.filter((x) => x !== s) : [...d.states, s] }));

  const clearAll = () => {
    setFilter('all');
    setSearch('');
    setDraft(EMPTY_FILTER);
    setApplied(EMPTY_FILTER);
  };

  const refresh = async () => {
    setRefreshing(true);
    try {
      await app.refreshProject();
    } catch (e) {
      app.toast((e as Error).message);
    } finally {
      setRefreshing(false);
    }
  };

  return (
    <div className="page">
      <PageHero
        eyebrow={`Machines · ${app.project}`}
        title="Every workload, one table."
        lede="Provision, inspect, snapshot and power-cycle machines. Select a row for details, console and SSH, or tick several to act on them together."
      />
      <div className="toolbar-pill">
        <label className="search-field">
          <Icon name="search" size={15} />
          <input
            className="input-field"
            name="search"
            type="search"
            placeholder="Search name, image, ID, IP or label (env=ci)"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            aria-label="Search machines"
          />
        </label>
        <Segmented<Filter>
          label="State filter"
          value={filter}
          onChange={setFilter}
          options={[
            { value: 'all', label: 'All', count: source.length },
            { value: 'running', label: 'Running', count: count('running') },
            { value: 'stopped', label: 'Stopped', count: count('stopped') },
            { value: 'provisioning', label: 'Busy', count: count('provisioning') },
            { value: 'failed', label: 'Attention', count: count('failed') },
          ]}
        />
        <div className="toolbar-pill__trailing">
          <button type="button" className="btn-secondary compact" aria-expanded={showServer} onClick={() => setShowServer((v) => !v)}>
            Query{serverActive ? ' (on)' : ''}
          </button>
          <button type="button" className="btn-refresh compact" onClick={refresh} disabled={refreshing}>
            <Icon name="refresh" size={14} />
            {refreshing ? 'Refreshing…' : 'Refresh'}
          </button>
        </div>
      </div>

      {showServer && (
        <form
          className="card inline-form"
          aria-label="Machine query"
          onSubmit={(e) => {
            e.preventDefault();
            setApplied({ ...draft });
            setSelection(new Set());
          }}
        >
          <label className="field">
            <span>Label selector</span>
            <input
              className="input-field mono"
              placeholder="env in (ci,qa),!pinned"
              value={draft.selector}
              onChange={(e) => setDraft((d) => ({ ...d, selector: e.target.value }))}
            />
          </label>
          <label className="field">
            <span>Name contains</span>
            <input className="input-field" value={draft.name} maxLength={128} onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))} />
          </label>
          <fieldset className="state-checks">
            <legend>States (none = any)</legend>
            {MACHINE_STATES.map((s) => (
              <label key={s}>
                <input type="checkbox" checked={draft.states.includes(s)} onChange={() => toggleState(s)} /> {s}
              </label>
            ))}
          </fieldset>
          <div className="bulk-row">
            <button type="submit" className="primary compact">
              Apply query
            </button>
            <button type="button" className="btn-secondary compact" onClick={clearAll}>
              Reset
            </button>
          </div>
        </form>
      )}
      {filterError && (
        <p className="hint-warn" role="alert">
          {filterError}
        </p>
      )}

      {app.canOperate && <BulkBar selected={selected} onClear={() => setSelection(new Set())} />}

      <div className="table-wrap">
        {items.length ? (
          <table className="machine-table">
            <thead>
              <tr>
                {app.canOperate && (
                  <th className="cell-check">
                    <input
                      type="checkbox"
                      aria-label="Select all shown machines"
                      checked={allSelected}
                      onChange={() => setSelection(allSelected ? new Set() : new Set(selectable.map((m) => m.id)))}
                    />
                  </th>
                )}
                <th>Machine</th>
                <th className="hide-sm">Image</th>
                <th className="hide-sm">Resources</th>
                <th>State</th>
                <th className="hide-sm">IP address</th>
              </tr>
            </thead>
            <tbody>
              {items.map((m) => (
                <tr
                  key={m.id}
                  tabIndex={0}
                  onClick={() => app.openDetail(m.id)}
                  onKeyDown={(e) => {
                    if (e.target === e.currentTarget && (e.key === 'Enter' || e.key === ' ')) {
                      e.preventDefault();
                      app.openDetail(m.id);
                    }
                  }}
                >
                  {app.canOperate && (
                    <td className="cell-check" onClick={(e) => e.stopPropagation()}>
                      <input type="checkbox" aria-label={`Select ${m.spec.name}`} checked={selection.has(m.id)} onChange={() => toggle(m.id)} />
                    </td>
                  )}
                  <td>
                    <div className="cell-name">
                      <strong>
                        {m.spec.name} {m.protection && <Chip tone="info">protected</Chip>}
                      </strong>
                      <span>{m.id}</span>
                    </div>
                  </td>
                  <td className="cell-muted hide-sm">{m.spec.image}</td>
                  <td className="cell-muted hide-sm">
                    {m.spec.compute.cpu} vCPU · {fmtMemory(m.spec.compute.memoryMiB)} · {m.spec.disk.sizeGiB} GiB
                  </td>
                  <td>
                    <StatePill state={m.state} />
                  </td>
                  <td className="cell-mono hide-sm">{m.ipAddresses?.[0] || '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : serverActive && matched === null ? (
          <div className="skeleton skeleton-card" aria-busy="true" />
        ) : app.machines.length ? (
          <ListEmpty
            title="No matches"
            action={
              <button type="button" className="btn-secondary" onClick={clearAll}>
                Clear filters
              </button>
            }
          >
            No machine in {app.project} matches the current search and filters.
          </ListEmpty>
        ) : (
          <ListEmpty
            title="No machines yet"
            action={
              app.canOperate && (
                <button type="button" className="btn-secondary" onClick={() => app.openCreate()}>
                  Create machine
                </button>
              )
            }
          >
            Create the first workload in {app.project}. Linux templates boot in about a minute once their image is fetched.
          </ListEmpty>
        )}
      </div>
    </div>
  );
}
