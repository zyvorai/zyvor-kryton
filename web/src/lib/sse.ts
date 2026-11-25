// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

import type { CloudEvent } from '../types';

/** Reads krytond's server-sent event stream until it ends or is aborted. */
export async function readEventStream(body: ReadableStream<Uint8Array>, onEvent: (e: CloudEvent) => void): Promise<void> {
  const reader = body.getReader();
  const dec = new TextDecoder();
  let buf = '';
  for (;;) {
    const { value, done } = await reader.read();
    if (done) return;
    buf += dec.decode(value, { stream: true });
    const parts = buf.split('\n\n');
    buf = parts.pop() || '';
    for (const part of parts) {
      if (!part.includes('event: cloudevent')) continue;
      const line = part.split('\n').find((l) => l.startsWith('data: '));
      if (!line) continue;
      let e: CloudEvent;
      try {
        e = JSON.parse(line.slice(6)) as CloudEvent;
      } catch {
        continue; // ignore malformed frames
      }
      onEvent(e);
    }
  }
}
