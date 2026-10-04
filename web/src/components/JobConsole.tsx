// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import { useEffect, useRef } from 'react';
import { fmtTime } from '../lib/format';
import { jobConsoleOpen } from '../lib/console';
import type { Job } from '../types';

const STEP_ICON: Record<string, string> = { done: '✓', active: '●', failed: '✕' };

export default function JobConsole({ job, project }: { job: Job; project: string }) {
  const log = useRef<HTMLPreElement | null>(null);
  const lines = job.logs || [];
  useEffect(() => {
    const el = log.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [lines.length]);
  const openUrl = jobConsoleOpen(job, project);
  return (
    <div className="terminal job-console">
      <div className="terminalbar">
        <span>
          <i />
          <i />
          <i />
        </span>
        <span className="terminal-title">{job.name}</span>
        <span>{job.progressPercent || 0}%</span>
      </div>
      <div className="job-console-layout">
        <ol className="job-steps">
          {(job.steps || []).map((s, i) => (
            <li key={i} className={`job-step ${s.state}`}>
              <span className="job-step-icon" aria-hidden>
                {STEP_ICON[s.state] || '○'}
              </span>
              <div>
                <span className="job-step-label">{s.label}</span>
                {s.detail && <span className="job-step-detail">{s.detail}</span>}
              </div>
            </li>
          ))}
        </ol>
        <pre className="job-log" ref={log}>
          {lines.length ? (
            lines.map((l, i) => (
              <div key={i} className={`job-log-line ${l.level || 'info'}`}>
                {l.time && <span className="job-log-time">{fmtTime(l.time)}</span>}
                {l.message || ''}
              </div>
            ))
          ) : (
            <div className="job-log-line info">Waiting for log output…</div>
          )}
        </pre>
      </div>
      <div className="terminal-foot">
        <span>{job.state}</span>
        {openUrl && (
          <a href={openUrl} target="_blank" rel="noopener noreferrer">
            Open install viewer
          </a>
        )}
      </div>
    </div>
  );
}
