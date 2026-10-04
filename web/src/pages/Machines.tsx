// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import { useMemo, useState } from 'react';
import { useApp } from '../state';
import { fmtMemory } from '../lib/format';
import { ListEmpty, PageHero, Segmented, StatePill } from '../components/kit';
import Icon from '../components/Icon';
import { matchesFilter, type MachineFilter as Filter } from '../lib/machines';

export default function Machines() {
  const app = useApp();
  const [filter, setFilter] = useState<Filter>('all');
  const [search, setSearch] = useState('');
  const [refreshing, setRefreshing] = useState(false);
  const items = useMemo(() => app.machines.filter((m) => matchesFilter(m, filter, search)), [app.machines, filter, search]);
  const count = (f: Filter) => app.machines.filter((m) => matchesFilter(m, f, '')).length;

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
        lede="Provision, inspect, snapshot and power-cycle machines. Select a row for details, console and SSH."
      />
      <div className="toolbar-pill">
        <label className="search-field">
          <Icon name="search" size={15} />
          <input
            className="input-field"
            name="search"
            type="search"
            placeholder="Search name, image, ID or IP"
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
            { value: 'all', label: 'All', count: app.machines.length },
            { value: 'running', label: 'Running', count: count('running') },
            { value: 'stopped', label: 'Stopped', count: count('stopped') },
            { value: 'provisioning', label: 'Busy', count: count('provisioning') },
            { value: 'failed', label: 'Attention', count: count('failed') },
          ]}
        />
        <div className="toolbar-pill__trailing">
          <button type="button" className="btn-refresh compact" onClick={refresh} disabled={refreshing}>
            <Icon name="refresh" size={14} />
            {refreshing ? 'Refreshing…' : 'Refresh'}
          </button>
        </div>
      </div>

      <div className="table-wrap">
        {items.length ? (
          <table className="machine-table">
            <thead>
              <tr>
                <th>Machine</th>
                <th>Image</th>
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
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault();
                      app.openDetail(m.id);
                    }
                  }}
                >
                  <td>
                    <div className="cell-name">
                      <strong>{m.spec.name}</strong>
                      <span>{m.id}</span>
                    </div>
                  </td>
                  <td className="cell-muted">{m.spec.image}</td>
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
        ) : app.machines.length ? (
          <ListEmpty
            title="No matches"
            action={
              <button
                type="button"
                className="btn-secondary"
                onClick={() => {
                  setFilter('all');
                  setSearch('');
                }}
              >
                Clear filters
              </button>
            }
          >
            No machine in {app.project} matches the current search and state filter.
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
