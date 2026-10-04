// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import type { Machine } from '../types';

export type MachineFilter = 'all' | 'running' | 'stopped' | 'provisioning' | 'failed';

export function matchesFilter(m: Machine, filter: MachineFilter, search: string): boolean {
  const byState =
    filter === 'all' ||
    m.state === filter ||
    (filter === 'failed' && ['failed', 'unknown'].includes(m.state)) ||
    (filter === 'provisioning' && ['provisioning', 'starting', 'stopping', 'pending'].includes(m.state));
  if (!byState) return false;
  const s = search.trim().toLowerCase();
  if (!s) return true;
  return [m.spec.name, m.id, m.spec.image, ...(m.ipAddresses || [])].some((v) => v.toLowerCase().includes(s));
}
