// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import { useApp } from '../state';
import { fmtMemory, healthPercent, plural } from '../lib/format';
import { Card, ListEmpty, PageHero, StatePill } from '../components/kit';
import JobConsole from '../components/JobConsole';
import Timeline from '../components/Timeline';
import DoctorBanner from '../components/DoctorBanner';

export default function Overview() {
  const app = useApp();
  const s = app.summary;
  const provider = app.caps?.provider || s?.provider || '';
  const running = app.jobs.filter((j) => j.state === 'running');
  const health = s ? healthPercent(s.machines, s.running) : 100;
  const title = !s
    ? 'Connecting to Kryton…'
    : s.attention
      ? `${plural(s.attention, 'workload')} ${s.attention === 1 ? 'needs' : 'need'} attention.`
      : s.machines
        ? `${plural(s.running, 'machine')} running smoothly.`
        : 'Ready for the first machine.';
  const lede = s?.machines
    ? `${s.stopped} stopped · ${s.cpu} vCPU · ${fmtMemory(s.memoryMiB)} allocated in ${app.project}.`
    : `Create a Linux or Windows machine in ${app.project || 'this project'} through the ${provider || 'configured'} provider.`;

  return (
    <div className="page">
      <PageHero
        eyebrow={`Overview · ${provider}`}
        title={title}
        lede={lede}
        tint={s?.attention ? 'amber' : undefined}
      />
      <DoctorBanner />
      <div className="apple-metric-band" aria-label="Estate metrics">
        <div>
          <span>machines</span>
          <b>{s?.machines ?? '—'}</b>
        </div>
        <div>
          <span>running</span>
          <b className={s && s.running ? 'tone-good' : ''}>{s?.running ?? '—'}</b>
        </div>
        <div>
          <span>vCPU allocated</span>
          <b>{s?.cpu ?? '—'}</b>
        </div>
        <div>
          <span>memory allocated</span>
          <b>{s ? fmtMemory(s.memoryMiB) : '—'}</b>
        </div>
        <div>
          <span>healthy</span>
          <b className={health < 100 ? 'tone-warn' : ''}>{s ? `${health}%` : '—'}</b>
        </div>
      </div>

      {running[0] && (
        <Card
          eyebrow="Active job"
          title={running[0].name}
          lede={running[0].message}
          actions={
            <button type="button" className="btn-secondary compact" onClick={() => app.go('jobs')}>
              All jobs
            </button>
          }
        >
          <JobConsole job={running[0]} project={app.project} />
        </Card>
      )}

      <div className="grid-2">
        <Card
          eyebrow="Machines"
          title="Recent workloads"
          actions={
            <button type="button" className="apple-text-link" onClick={() => app.go('machines')}>
              See all ›
            </button>
          }
        >
          {app.machines.length ? (
            <ul className="mini-list">
              {app.machines.slice(0, 6).map((m) => (
                <li key={m.id}>
                  <button type="button" className="mini-row" onClick={() => app.openDetail(m.id)}>
                    <span className="mini-name">
                      <strong>{m.spec.name}</strong>
                      <span>{m.spec.image}</span>
                    </span>
                    <span className="mini-ip">{m.ipAddresses?.[0] || 'No IP yet'}</span>
                    <StatePill state={m.state} />
                  </button>
                </li>
              ))}
            </ul>
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
              Pick a Linux cloud image or a Windows build and Kryton provisions it through {provider}.
            </ListEmpty>
          )}
        </Card>
        <Card
          eyebrow="Activity"
          title="What just happened"
          actions={
            <button type="button" className="apple-text-link" onClick={() => app.go('activity')}>
              Open activity ›
            </button>
          }
        >
          <Timeline items={app.events.slice(0, 6)} compact />
        </Card>
      </div>

      <Card eyebrow="Integration" title="Built to be consumed." className="integration-card">
        <p>CI systems, portals and other Zyvor products drive the same stable contract this console uses.</p>
        <div className="endpoint-row">
          <code>
            <b>POST</b> /api/v1/machines
          </code>
          <code>
            <b>GET</b> /api/v1/events/stream
          </code>
          <a className="apple-text-link" href="/openapi.yaml" target="_blank" rel="noopener noreferrer">
            OpenAPI spec ›
          </a>
        </div>
      </Card>
    </div>
  );
}
