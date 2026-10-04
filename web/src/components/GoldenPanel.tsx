// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import { api, q } from '../api';
import { useApp } from '../state';
import { goldenStepIndex } from '../lib/console';
import { Card, Chip, Progress } from './kit';
import JobConsole from './JobConsole';
import type { GoldenBuild } from '../types';

const STEPS = ['Start dockur', 'Download ISO', 'Install Windows', 'Sysprep', 'Capture qcow2', 'Validate'];

export default function GoldenPanel() {
  const app = useApp();
  const build = app.activeGolden;
  const job = build ? app.jobs.find((j) => j.kind === 'golden' && j.id === `golden:${build.id}`) : undefined;
  const idx = goldenStepIndex(build);

  const start = async () => {
    const imageId = app.images.some((i) => i.id === 'windows-11-enterprise')
      ? 'windows-11-enterprise'
      : app.images.find((i) => i.os !== 'linux')?.id || 'windows-11-enterprise';
    try {
      await api<GoldenBuild>('/api/v1/golden', { method: 'POST', body: JSON.stringify({ imageId, auto: true }) });
      app.toast('Golden image build started');
      await Promise.all([app.loadGolden(), app.loadJobs()]);
    } catch (e) {
      app.toast((e as Error).message);
    }
  };
  const publish = async (id: string) => {
    try {
      await api(`/api/v1/golden/${q(id)}/bootstrap`, { method: 'POST', body: '{}' });
      app.toast('Publishing golden image to CDI');
      await app.loadGolden();
    } catch (e) {
      app.toast((e as Error).message);
    }
  };

  return (
    <Card
      eyebrow="Golden image factory"
      title="Build KubeVirt golden images with dockur/windows"
      lede="Unattended ISO download, Windows setup and VirtIO drivers, then a Sysprep capture published to CDI."
      className="golden-panel"
    >
      {job && job.state === 'running' ? (
        <JobConsole job={job} project={app.project} />
      ) : (
        <ol className="golden-steps">
          {STEPS.map((label, i) => (
            <li key={label} className={i + 1 < idx ? 'done' : i + 1 === idx ? 'active' : ''}>
              <span>{i + 1}</span>
              <strong>{label}</strong>
            </li>
          ))}
        </ol>
      )}
      {build && !job && (
        <>
          <Progress value={build.progressPercent || 0} />
          <p className="muted">
            {build.message || ''}
            {build.outputPath ? ` · ${build.outputPath}` : ''}
          </p>
        </>
      )}
      {build?.state === 'ready' && (
        <p>
          <Chip tone={build.certified ? 'good' : 'warn'}>
            {build.certified ? 'guestkit certified' : 'Not boot-certified'}
            {build.validationScore != null ? ` · score ${build.validationScore}` : ''}
          </Chip>
        </p>
      )}
      {build?.bootstrapState === 'running' && <p className="muted">Publishing to CDI… {build.bootstrapMessage}</p>}
      {build?.bootstrapState === 'ready' && <p className="muted">CDI DataSource ready: {build.dataSource}</p>}
      {build?.bootstrapState === 'failed' && <p className="hint-warn">CDI bootstrap failed: {build.bootstrapMessage}</p>}
      <div className="card-footer-actions">
        {app.canOperate && (!build || ['ready', 'failed'].includes(build.state)) && (
          <button type="button" className="primary" onClick={start}>
            Build golden image
          </button>
        )}
        {build?.state === 'ready' && app.canOperate && (
          <button type="button" className="btn-secondary" disabled={build.bootstrapState === 'running'} onClick={() => publish(build.id)}>
            Publish to KubeVirt
          </button>
        )}
        {build?.consoleUrl && (
          <a className="buttonlike btn-secondary" href={build.consoleUrl} target="_blank" rel="noopener noreferrer">
            Watch install
          </a>
        )}
        {build?.passportPath && (
          <a className="buttonlike btn-secondary" href={`/api/v1/golden/${q(build.id)}/passport`} target="_blank" rel="noopener noreferrer">
            View passport
          </a>
        )}
        <button type="button" className="btn-refresh" onClick={() => Promise.all([app.loadGolden(), app.loadJobs()])}>
          Refresh
        </button>
      </div>
    </Card>
  );
}
