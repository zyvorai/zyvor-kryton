// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import { useEffect, useState } from 'react';
import { api } from '../api';
import { consoleTicketEndpoint, withConsoleTicket } from './console';

// Server tickets live 10 minutes; re-minting well before that also renews
// the console cookie an open viewer relies on to reconnect.
const REFRESH_MS = 8 * 60 * 1000;

type Ticketed = { embed: string; open: string };

/**
 * Iframes and new tabs can't send the bearer token, so in-app console URLs
 * get a short-lived console ticket appended. `embed` stays fixed for the
 * first ticket so a refresh never reloads a live iframe; `open` carries the
 * newest ticket for "open in new tab" links. Both are '' until the first
 * ticket arrives; external URLs pass through unchanged.
 */
export function useTicketedConsole(url: string): Ticketed {
  const endpoint = consoleTicketEndpoint(url);
  const [first, setFirst] = useState<{ for: string; value: string } | null>(null);
  const [latest, setLatest] = useState<{ for: string; value: string } | null>(null);

  useEffect(() => {
    if (!endpoint) return;
    let cancelled = false;
    const mint = () =>
      api<{ ticket: string }>(endpoint, { method: 'POST' })
        .then((r) => {
          if (cancelled) return;
          const t = { for: endpoint, value: r.ticket };
          setLatest(t);
          setFirst((prev) => (prev?.for === endpoint ? prev : t));
        })
        .catch(() => undefined);
    mint();
    const timer = window.setInterval(mint, REFRESH_MS);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [endpoint]);

  if (!url || !endpoint) return { embed: url, open: url };
  return {
    embed: first?.for === endpoint ? withConsoleTicket(url, first.value) : '',
    open: latest?.for === endpoint ? withConsoleTicket(url, latest.value) : '',
  };
}
