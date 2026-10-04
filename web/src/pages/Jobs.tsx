// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import { useEffect } from 'react';
import { useApp } from '../state';
import { jobConsoleEmbed, jobConsoleOpen } from '../lib/console';
import { Card, ListEmpty, PageHero, StatePill } from '../components/kit';
import JobConsole from '../components/JobConsole';
import Icon from '../components/Icon';

export default function Jobs() {
  const app = useApp();
  useEffect(() => {
    app.loadJobs();
  }, []);
  const running = app.jobs.filter((j) => j.state === 'running');
  const rest = app.jobs.filter((j) => j.state !== 'running');

  return (
    <div className="page">
      <PageHero
        eyebrow="Jobs"
        title="What Kryton is doing right now."
        lede="Live steps and log output for machine provisioning, image builds and storage setup."
        actions={
          <button type="button" className="btn-refresh" onClick={() => app.loadJobs()}>
            <Icon name="refresh" size={14} />
            Refresh
          </button>
        }
      />
      {!app.jobs.length && (
        <ListEmpty
          title="No jobs yet"
          action={
            app.canOperate && (
              <button type="button" className="btn-secondary" onClick={() => app.openCreate()}>
                Create machine
              </button>
            )
          }
        >
          Create a machine and its provisioning steps stream here live.
        </ListEmpty>
      )}
      <div className="stack">
        {[...running, ...rest].map((job) => {
          const embed = jobConsoleEmbed(job, app.project);
          return (
            <Card
              key={job.id}
              eyebrow={`${job.kind} job`}
              title={job.name}
              lede={job.message}
              actions={<StatePill state={job.state === 'running' ? 'provisioning' : job.state} />}
            >
              <JobConsole job={job} project={app.project} />
              {embed && job.state === 'running' && (
                <div className="console-frame">
                  <div className="console-frame-head">
                    <span>Live install viewer</span>
                    <a href={jobConsoleOpen(job, app.project)} target="_blank" rel="noopener noreferrer">
                      Full screen
                    </a>
                  </div>
                  <iframe src={embed} title="Install console" />
                </div>
              )}
            </Card>
          );
        })}
      </div>
    </div>
  );
}
