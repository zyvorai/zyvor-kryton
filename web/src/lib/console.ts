// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import type { GoldenBuild, Job, Machine } from '../types';

export function withConsoleHTML(url: string): string {
  if (!url) return '';
  return url.includes('format=') ? url : url + (url.includes('?') ? '&' : '?') + 'format=html';
}

export function jobConsoleEmbed(job: Job | undefined, project: string): string {
  if (!job?.consoleUrl) return '';
  if (job.consoleUrl.startsWith('/')) return withConsoleHTML(job.consoleUrl);
  if (job.kind === 'machine' && job.id.startsWith('machine:')) {
    const id = encodeURIComponent(job.id.slice(8));
    return withConsoleHTML(`/api/v1/machines/${id}/console/?project=${encodeURIComponent(job.project || project || 'default')}`);
  }
  return '';
}

export function jobConsoleOpen(job: Job | undefined, project: string): string {
  return jobConsoleEmbed(job, project) || job?.consoleUrl || '';
}

export function machineConsole(m: Machine | undefined, project: string): string {
  if (!m?.consoleUrl) return '';
  const url = m.consoleUrl.startsWith('/')
    ? m.consoleUrl
    : `/api/v1/machines/${encodeURIComponent(m.id)}/console/?project=${encodeURIComponent(m.project || project || 'default')}`;
  return withConsoleHTML(url);
}

/** Step 1..6 of the golden image factory for a build's phase/state. */
export function goldenStepIndex(build?: GoldenBuild | null): number {
  if (!build) return 0;
  const phase = build.phase || '';
  if (['validate', 'complete'].includes(phase) || build.state === 'ready') return 6;
  if (phase === 'convert') return 5;
  if (['generalize', 'sysprep'].includes(phase) || build.state === 'sysprep') return 4;
  if (phase === 'windows_setup' || build.state === 'installing') return 3;
  if (phase === 'download') return 2;
  return 1;
}

export function sshCommand(m: Machine, defaultUser?: string): string {
  const user = m.spec.initialization?.username || defaultUser || 'root';
  return `ssh ${user}@${m.ipAddresses?.[0] ?? ''}`;
}
