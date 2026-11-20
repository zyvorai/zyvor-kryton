// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-1.0

import type { GuestJob, GuestJobState, Readiness } from '../types';

export type ReadyView = { ready: boolean; percent: number; label: string; detail: string };

const PHASE_LABEL: Record<string, string> = {
  starting: 'Starting container',
  downloading: 'Downloading Windows media',
  extracting: 'Extracting media',
  building: 'Building the disk image',
  installing: 'Installing Windows',
  stopped: 'Container stopped',
  unknown: 'Waiting for the guest',
  complete: 'Ready',
};

/** Turns a /ready body into a label and percent for the progress bar. */
export function readyView(r?: Readiness | null): ReadyView {
  if (!r) return { ready: false, percent: 0, label: 'Checking readiness', detail: '' };
  if (r.ready) return { ready: true, percent: 100, label: 'Ready', detail: r.agentRequired ? 'RDP is answering and the job agent is alive.' : 'RDP is answering.' };
  const phase = r.install?.phase || (r.containerUp ? 'unknown' : 'stopped');
  let label = PHASE_LABEL[phase] || 'Waiting for the guest';
  // Windows is installed and booting; say what is still missing.
  if (r.containerUp && r.rdpOpen && r.agentRequired && !r.agentPresent) label = 'Waiting for the job agent';
  const failing = (r.checks || []).filter((c) => !c.ok).map((c) => c.detail || c.name);
  const detail = r.install?.message || failing[0] || '';
  return { ready: false, percent: Math.max(0, Math.min(99, r.install?.percent ?? 0)), label, detail };
}

/** Poll quickly while installing, slowly when ready, not at all for stopped machines. */
export function readyPollMs(state: string, r?: Readiness | null): number {
  if (state === 'stopped' || state === 'failed') return 0;
  return r?.ready ? 30000 : 4000;
}

export const JOB_ACTIVE: GuestJobState[] = ['queued', 'running'];
export const isJobActive = (j: Pick<GuestJob, 'state'>) => JOB_ACTIVE.includes(j.state);

export type JobForm = { name: string; shell: 'cmd' | 'powershell'; script: string; timeoutSeconds: string };

export function validateJob(f: JobForm): string | null {
  if (!f.script.trim()) return 'Enter a command to run.';
  if (new TextEncoder().encode(f.script).length > 65536) return 'The command is larger than 64 KiB.';
  const t = Number(f.timeoutSeconds);
  if (!Number.isInteger(t) || t < 1 || t > 86400) return 'Timeout must be 1 to 86400 seconds.';
  if (f.name.length > 63) return 'Job name is limited to 63 characters.';
  return null;
}

export function buildJobBody(f: JobForm) {
  const body: Record<string, unknown> = { shell: f.shell, script: f.script, timeoutSeconds: Number(f.timeoutSeconds) };
  if (f.name.trim()) body.name = f.name.trim();
  return body;
}

/** Percent-encodes each segment of a shared-folder path, keeping the slashes. */
export function encodeSharedPath(p: string): string {
  return p
    .split('/')
    .filter(Boolean)
    .map(encodeURIComponent)
    .join('/');
}

export function joinShared(dir: string, name: string): string {
  return [dir, name].filter(Boolean).join('/');
}

export function parentShared(dir: string): string {
  const parts = dir.split('/').filter(Boolean);
  parts.pop();
  return parts.join('/');
}

/** Client-side check mirroring the server's path rules, so errors show before the upload. */
export function validSharedName(name: string): boolean {
  return !!name && name !== '.' && name !== '..' && !/[\\/:\u0000-\u001f]/.test(name);
}

export function fmtBytes(n: number): string {
  if (!Number.isFinite(n) || n < 1024) return `${n || 0} B`;
  const units = ['KiB', 'MiB', 'GiB', 'TiB'];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

export function jobTone(s: GuestJobState): 'good' | 'warn' | 'bad' | 'info' | 'neutral' {
  if (s === 'succeeded') return 'good';
  if (s === 'failed' || s === 'timed_out') return 'bad';
  if (s === 'running') return 'info';
  if (s === 'queued') return 'warn';
  return 'neutral';
}

/** True when the machine was created with the shared folder (explicitly or through the job agent). */
export const hasShared = (d?: { shared?: boolean; jobAgent?: boolean }) => !!(d?.shared || d?.jobAgent);
