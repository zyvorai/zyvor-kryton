// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-1.0

import type { DockurOptions, Image } from '../types';

export type CreateForm = {
  name: string;
  project: string;
  image: string;
  cpu: number;
  memory: number;
  disk: number;
  storageClass: string;
  ttl: number;
  network: string;
  linuxUsername: string;
  linuxSSHKeys: string;
  dockur: Omit<DockurOptions, 'rdpPort' | 'consolePort'> & {
    noAutologin?: boolean;
    extraDisks?: string;
    /** Port fields stay strings while typing; blank means auto-allocate. */
    rdpPort?: string;
    consolePort?: string;
  };
};

export const OEM_SCRIPT_MAX = 64 * 1024;

export const NAME_PATTERN = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;

export function parseKeys(text: string): string[] {
  return text
    .split(/\r?\n/)
    .map((k) => k.trim())
    .filter(Boolean);
}

export function validateCreate(f: CreateForm, img?: Image): string | null {
  if (!NAME_PATTERN.test(f.name.trim())) return 'Use lowercase letters, digits and dashes for the machine name.';
  if (!img) return 'Pick an operating system image.';
  if (!img.ready) return `Image "${img.name}" is not ready. Build or fetch it first.`;
  if (f.cpu < 1) return 'vCPU must be at least 1.';
  if (f.memory < 512) return 'Memory must be at least 512 MiB.';
  if (f.disk < 1) return 'Boot disk must be at least 1 GiB.';
  if (img.os === 'linux' && f.linuxUsername && !/^[a-z_][a-z0-9_-]{0,31}$/.test(f.linuxUsername.trim()))
    return 'Linux username must be lowercase letters, digits, "_" or "-".';
  if (img.os !== 'linux') return validateDockur(f.dockur);
  return null;
}

/** Parses an optional host port; '' is auto, anything else must be 1024-65535. Returns NaN when invalid. */
export function parsePort(v?: string): number {
  const t = (v || '').trim();
  if (!t) return 0;
  const n = Number(t);
  return Number.isInteger(n) && n >= 1024 && n <= 65535 ? n : NaN;
}

export function validateDockur(d: CreateForm['dockur']): string | null {
  const rdp = parsePort(d.rdpPort);
  const con = parsePort(d.consolePort);
  if (Number.isNaN(rdp)) return 'RDP port must be blank (automatic) or a number from 1024 to 65535.';
  if (Number.isNaN(con)) return 'Console port must be blank (automatic) or a number from 1024 to 65535.';
  if (rdp && rdp === con) return 'RDP and console ports must differ.';
  if (new TextEncoder().encode(d.oemScript || '').length > OEM_SCRIPT_MAX) return 'The install script is larger than 64 KiB.';
  return null;
}

/** Bumps resources up to an image's minimums when it is selected. */
export function applyImageMinimums(f: CreateForm, img?: Image): CreateForm {
  if (!img) return f;
  return {
    ...f,
    cpu: Math.max(f.cpu || 0, img.minCpu || 1),
    memory: Math.max(f.memory || 0, img.minMemoryMiB || 512),
    disk: Math.max(f.disk || 0, img.defaultDiskGiB || 10),
  };
}

export function buildCreateBody(f: CreateForm, img: Image | undefined, provider?: string) {
  const body: Record<string, unknown> = {
    project: f.project,
    name: f.name.trim(),
    image: f.image,
    compute: { cpu: Number(f.cpu), memoryMiB: Number(f.memory) },
    disk: { sizeGiB: Number(f.disk), ...(f.storageClass.trim() ? { storageClass: f.storageClass.trim() } : {}) },
    network: { networkId: f.network.trim() },
    ttlMinutes: Number(f.ttl || 0),
  };
  if (img?.os === 'linux') {
    body.initialization = { username: f.linuxUsername.trim(), sshAuthorizedKeys: parseKeys(f.linuxSSHKeys) };
  }
  if (provider === 'dockur') {
    const d = f.dockur;
    const dockur: DockurOptions = {
      username: (d.username || '').trim(),
      hostname: (d.hostname || '').trim(),
      language: (d.language || '').trim(),
      region: (d.region || '').trim(),
      keyboard: (d.keyboard || '').trim(),
      productKey: (d.productKey || '').trim(),
      domain: (d.domain || '').trim(),
      domainOu: (d.domainOu || '').trim(),
      command: (d.command || '').trim(),
      customIso: (d.customIso || '').trim(),
      edition: (d.edition || '').trim(),
      audio: !!d.audio,
      secureBoot: !!d.secureBoot,
    };
    // Left empty, the server generates a password and returns it once as generatedPassword.
    if (d.password) dockur.password = d.password;
    if (d.jobAgent) dockur.jobAgent = true; // the agent needs the shared folder; the server implies it too
    if (d.shared || d.jobAgent) dockur.shared = true;
    if ((d.oemScript || '').trim()) dockur.oemScript = d.oemScript;
    if (d.publicBind) dockur.publicBind = true;
    const rdpPort = parsePort(d.rdpPort);
    const consolePort = parsePort(d.consolePort);
    if (rdpPort) dockur.rdpPort = rdpPort;
    if (consolePort) dockur.consolePort = consolePort;
    if (d.noAutologin) dockur.autologin = false;
    const extra = String(d.extraDisks || '')
      .split(',')
      .map((s) => Number(s.trim()))
      .filter((n) => n > 0);
    if (extra.length) dockur.extraDisksGiB = extra;
    body.dockur = dockur;
  }
  return body;
}
