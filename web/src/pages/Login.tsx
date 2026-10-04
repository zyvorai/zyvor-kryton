// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import { useEffect, useState } from 'react';
import { setToken } from '../api';

type HostFacts = { auth?: string; provider?: string; ready?: string };

/** Probe the key against a JSON endpoint: 401 means rejected, HTML means wrong URL. */
async function probe(tok: string): Promise<'ok' | 'unauthorized' | 'unreachable'> {
  try {
    const r = await fetch('/api/v1/me', { headers: { Authorization: `Bearer ${tok}`, Accept: 'application/json' } });
    if (r.status === 401 || r.status === 403) return 'unauthorized';
    const ct = r.headers.get('content-type') || '';
    if (!r.ok || !ct.includes('application/json')) return 'unreachable';
    return 'ok';
  } catch {
    return 'unreachable';
  }
}

export default function Login({ onLogin, initialError = '' }: { onLogin: () => void; initialError?: string }) {
  const [key, setKey] = useState('');
  const [error, setError] = useState(initialError);
  const [busy, setBusy] = useState(false);
  const [facts, setFacts] = useState<HostFacts>({});
  const host = window.location.hostname || 'localhost';
  const port = window.location.port || (window.location.protocol === 'https:' ? '443' : '80');

  useEffect(() => setError(initialError), [initialError]);

  useEffect(() => {
    (async () => {
      const disc = await fetch('/api/v1', { headers: { Accept: 'application/json' } })
        .then((r) => r.json())
        .catch(() => null);
      const ready = await fetch('/readyz', { headers: { Accept: 'application/json' } })
        .then((r) => r.json())
        .catch(() => null);
      setFacts({
        auth: disc?.auth?.mode,
        provider: ready?.provider || disc?.provider,
        ready: ready?.status,
      });
    })();
  }, []);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    const tok = key.trim();
    if (!tok) {
      setError('Paste an API key to continue.');
      return;
    }
    setBusy(true);
    setError('');
    const result = await probe(tok);
    setBusy(false);
    if (result === 'ok') {
      setToken(tok);
      setKey('');
      onLogin();
      return;
    }
    setError(result === 'unauthorized' ? 'That API key was not accepted.' : 'Could not reach Kryton. Check the URL and try again.');
  }

  return (
    <div className="login-shell">
      <div className="login-info">
        <img src="/zyvor-mark.svg" alt="Zyvor" className="login-logo" />
        <p className="eyebrow">Kryton · Zyvor</p>
        <h1>Linux and Windows machines, under one API.</h1>
        <p>
          Kryton provisions and operates virtual machines through libvirt, KubeVirt or dockur — one REST contract for this console, your CI and
          every Zyvor product.
        </p>
        <ul className="login-facts" aria-label="This host">
          <li>
            <span>Host</span>
            <b>
              {host}:{port}
            </b>
          </li>
          <li>
            <span>Provider</span>
            <b>{facts.provider || '—'}</b>
          </li>
          <li>
            <span>Auth</span>
            <b>{facts.auth || '—'}</b>
          </li>
          <li>
            <span>Status</span>
            <b className={facts.ready === 'ok' || facts.ready === 'ready' ? 'good' : ''}>{facts.ready || '—'}</b>
          </li>
        </ul>
      </div>
      <form className="card login-card" onSubmit={submit} noValidate>
        <h1>Sign in.</h1>
        <label className="field">
          <span>API key</span>
          <input
            className="input-field"
            name="apiKey"
            type="password"
            value={key}
            placeholder="kryton_…"
            autoComplete="off"
            autoFocus
            disabled={busy}
            aria-invalid={Boolean(error)}
            onChange={(e) => {
              setKey(e.target.value);
              if (error) setError('');
            }}
          />
        </label>
        {error ? (
          <p className="login-error" role="alert" aria-live="assertive">
            {error}
          </p>
        ) : null}
        <button type="submit" className="primary" disabled={busy}>
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
        <p className="login-hint">
          The key stays in this tab's session storage. On the Kryton host, <code>./scripts/ensure-api-keys.sh</code> writes one to{' '}
          <code>/etc/kryton/lab.token</code>.
        </p>
      </form>
    </div>
  );
}
