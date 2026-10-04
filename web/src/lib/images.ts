// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import type { Image } from '../types';

export type Availability = 'stored' | 'on-demand' | 'catalog';

export function availability(img: Image): Availability {
  const a = img.availability;
  return a === 'stored' || a === 'on-demand' ? a : 'catalog';
}

export function availabilityLabel(img: Image): string {
  const a = availability(img);
  if (a === 'stored') return 'Stored';
  if (a === 'on-demand') return 'Downloads on create';
  return img.os === 'linux' ? 'Fetch required' : 'Build required';
}

export function isLinux(img?: Image): boolean {
  return img?.os === 'linux';
}

export function osLabel(img: Image): string {
  if (isLinux(img)) return `Linux · ${img.architecture || 'amd64'}`;
  return img.family === 'windows-server' ? 'Windows Server' : 'Windows Desktop';
}

export function storageFoot(img: Image): string {
  if (img.storagePath) return img.storagePath;
  if (img.storageNamespace && img.storageSource === 'cdi') return `CDI · ${img.storageNamespace}`;
  if (img.storageSource === 'dockur') return 'dockur/windows unattended';
  if (img.storageSource === 'demo') return 'In-memory demo';
  if (img.storageSource === 'verified') return 'Checksum-verified artifact';
  if (img.os === 'linux') return `Not fetched · kryton-image fetch -image ${img.id}`;
  return 'Not on this host yet';
}

const RANK: Record<Availability, number> = { stored: 0, 'on-demand': 1, catalog: 2 };

export function sortImages(images: Image[]): Image[] {
  return [...images].sort((a, b) => RANK[availability(a)] - RANK[availability(b)] || a.name.localeCompare(b.name));
}

/** Mirrors model.Image.SupportsProvider on the server. */
export function supportsProvider(img: Image, provider?: string): boolean {
  if (!provider || provider === 'demo') return true;
  if (!img.providers?.length) return provider === 'kubevirt' || provider === 'dockur';
  return img.providers.includes(provider);
}

/** Images offered in the create sheet: provider-compatible, ready ones first. */
export function pickableImages(images: Image[], provider?: string): { items: Image[]; anyReady: boolean } {
  const compatible = sortImages(images).filter((i) => supportsProvider(i, provider));
  const ready = compatible.filter((i) => i.ready);
  return { items: ready.length ? ready : compatible, anyReady: ready.length > 0 };
}

export function defaultImageId(images: Image[], provider: string | undefined, previous?: string): string {
  const { items } = pickableImages(images, provider);
  if (previous && items.some((i) => i.id === previous)) return previous;
  return items.find((i) => i.ready)?.id || items[0]?.id || '';
}
