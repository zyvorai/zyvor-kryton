// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

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
  dockur: DockurOptions & { noAutologin?: boolean; extraDisks?: string };
};

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
      password: d.password || '',
      hostname: (d.hostname || '').trim(),
      language: (d.language || '').trim(),
      region: (d.region || '').trim(),
      keyboard: (d.keyboard || '').trim(),
      productKey: (d.productKey || '').trim(),
      domain: (d.domain || '').trim(),
      domainOu: (d.domainOu || '').trim(),
      sharedDir: (d.sharedDir || '').trim(),
      oemDir: (d.oemDir || '').trim(),
      command: (d.command || '').trim(),
      customIso: (d.customIso || '').trim(),
      edition: (d.edition || '').trim(),
      audio: !!d.audio,
      secureBoot: !!d.secureBoot,
    };
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
