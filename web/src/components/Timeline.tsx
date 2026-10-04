// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import { ago, eventDescription, eventTitle } from '../lib/format';
import type { CloudEvent } from '../types';
import { ListEmpty } from './kit';

function eventTone(type: string): string {
  if (/fail|error/i.test(type)) return 'bad';
  if (/deleted|stopp/i.test(type)) return 'neutral';
  if (/created|started|ready|running/i.test(type)) return 'good';
  return 'info';
}

export default function Timeline({ items, compact }: { items: CloudEvent[]; compact?: boolean }) {
  if (!items.length)
    return <ListEmpty title="No activity yet">Lifecycle events appear here as soon as a machine is created, started or stopped.</ListEmpty>;
  return (
    <ol className={`timeline${compact ? ' compact' : ''}`}>
      {items.map((e) => (
        <li key={e.id} className="timeline-item">
          <span className={`timeline-dot dot-${eventTone(e.type)}`} aria-hidden />
          <div>
            <div className="timeline-title">{eventTitle(e.type)}</div>
            <div className="timeline-copy">{eventDescription(e)}</div>
          </div>
          <time className="timeline-time" dateTime={e.time}>
            {ago(e.time)}
          </time>
        </li>
      ))}
    </ol>
  );
}
