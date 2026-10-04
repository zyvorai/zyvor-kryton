// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import { useApp } from '../state';
import { Card, PageHero } from '../components/kit';
import Timeline from '../components/Timeline';
import Icon from '../components/Icon';

export default function Activity() {
  const app = useApp();
  return (
    <div className="page">
      <PageHero
        eyebrow="CloudEvents"
        title="Activity, without the noise."
        lede="Provider-neutral lifecycle events. The same stream is available to webhooks and other products."
        actions={
          <button type="button" className="btn-refresh" onClick={() => app.refreshProject().catch((e: Error) => app.toast(e.message))}>
            <Icon name="refresh" size={14} />
            Refresh
          </button>
        }
      />
      <Card
        eyebrow={`${app.events.length} events`}
        actions={
          <span className={`live-indicator${app.streamLive ? ' on' : ''}`}>
            <span className="live-dot" aria-hidden />
            {app.streamLive ? 'Live' : 'Polling'}
          </span>
        }
      >
        <Timeline items={app.events} />
      </Card>
    </div>
  );
}
