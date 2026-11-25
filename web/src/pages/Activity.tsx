// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

import { useCallback, useEffect, useState } from 'react';
import { api, authHeaders } from '../api';
import { useApp } from '../state';
import { Card, PageHero } from '../components/kit';
import Timeline from '../components/Timeline';
import Icon from '../components/Icon';
import { EVENT_TYPES, NO_EVENT_FILTER, eventFilterActive, eventsPath, mergeEvent, mergeHistory, streamPath, type EventFilter } from '../lib/fleet';
import { readEventStream } from '../lib/sse';
import type { CloudEvent, List } from '../types';

export default function Activity() {
  const app = useApp();
  const [filter, setFilter] = useState<EventFilter>(NO_EVENT_FILTER);
  const filtered = eventFilterActive(filter);
  const [matches, setMatches] = useState<CloudEvent[]>([]);
  const [live, setLive] = useState(false);

  const load = useCallback(async () => {
    const res = await api<List<CloudEvent>>(eventsPath(filter));
    return res.items || [];
  }, [filter]);

  // A filtered view has its own history query and its own filtered stream.
  useEffect(() => {
    if (!filtered) return;
    const ac = new AbortController();
    setMatches([]);
    setLive(false);
    (async () => {
      try {
        const res = await fetch(streamPath(filter), { headers: { ...authHeaders(), Accept: 'text/event-stream' }, signal: ac.signal });
        if (!res.ok || !res.body) return;
        setLive(true);
        await readEventStream(res.body, (e) => setMatches((cur) => mergeEvent(cur, e)));
      } catch {
        /* aborted or stream unsupported: the poll below still refreshes */
      }
      if (!ac.signal.aborted) setLive(false);
    })();
    const poll = () =>
      load()
        .then((items) => {
          if (ac.signal.aborted) return;
          setMatches((cur) => mergeHistory(cur, items));
        })
        .catch((e: Error) => !ac.signal.aborted && app.toast(e.message));
    poll();
    const timer = setInterval(poll, 15000);
    return () => {
      ac.abort();
      clearInterval(timer);
    };
  }, [filter, filtered, load]);

  const items = filtered ? matches : app.events;
  const isLive = filtered ? live : app.streamLive;

  const refresh = async () => {
    try {
      if (filtered) setMatches(await load());
      else await app.refreshProject();
    } catch (e) {
      app.toast((e as Error).message);
    }
  };

  return (
    <div className="page">
      <PageHero
        eyebrow="CloudEvents"
        title="Activity, without the noise."
        lede="Provider-neutral lifecycle events. The same stream is available to webhooks and other products."
        actions={
          <button type="button" className="btn-refresh" onClick={refresh}>
            <Icon name="refresh" size={14} />
            Refresh
          </button>
        }
      />
      <div className="card inline-form" role="group" aria-label="Event filters">
        <label className="field">
          <span>Type</span>
          <select value={filter.type} onChange={(e) => setFilter((f) => ({ ...f, type: e.target.value }))}>
            {EVENT_TYPES.map((t) => (
              <option key={t.value} value={t.value}>
                {t.label}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          <span>Machine</span>
          <select value={filter.machineId} onChange={(e) => setFilter((f) => ({ ...f, machineId: e.target.value }))}>
            <option value="">Any machine</option>
            {app.machines.map((m) => (
              <option key={m.id} value={m.id}>
                {m.spec.name}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          <span>History</span>
          <select value={filter.sinceHours} onChange={(e) => setFilter((f) => ({ ...f, sinceHours: Number(e.target.value) }))}>
            <option value={0}>Everything retained</option>
            <option value={1}>Last hour</option>
            <option value={24}>Last 24 hours</option>
            <option value={168}>Last 7 days</option>
          </select>
        </label>
        {filtered && (
          <button type="button" className="btn-secondary compact" onClick={() => setFilter(NO_EVENT_FILTER)}>
            Clear filters
          </button>
        )}
      </div>
      <Card
        eyebrow={`${items.length} events`}
        actions={
          <span className={`live-indicator${isLive ? ' on' : ''}`}>
            <span className="live-dot" aria-hidden />
            {isLive ? 'Live' : 'Polling'}
          </span>
        }
      >
        <Timeline items={items} />
      </Card>
    </div>
  );
}
