// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { api, AUTH_REQUIRED, clearToken, ensureAuth, errorMessage, setToken, setUnauthorizedHandler, token } from './api';

class MemStorage {
  private m = new Map<string, string>();
  getItem(k: string) {
    return this.m.has(k) ? this.m.get(k)! : null;
  }
  setItem(k: string, v: string) {
    this.m.set(k, v);
  }
  removeItem(k: string) {
    this.m.delete(k);
  }
}

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });

beforeEach(() => {
  vi.stubGlobal('sessionStorage', new MemStorage());
});
afterEach(() => {
  vi.unstubAllGlobals();
});

describe('token storage', () => {
  it('stores and clears the session token', () => {
    setToken('kryton_abc');
    expect(token()).toBe('kryton_abc');
    clearToken();
    expect(token()).toBe('');
  });
});

describe('ensureAuth', () => {
  it('adopts a lab bootstrap token', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json(200, { autoAuth: true, token: 'lab' })));
    expect(await ensureAuth()).toBe(true);
    expect(token()).toBe('lab');
  });
  it('does not auto-auth after an explicit sign out', async () => {
    const f = vi.fn(async () => json(200, { autoAuth: true, token: 'lab' }));
    vi.stubGlobal('fetch', f);
    clearToken();
    expect(await ensureAuth()).toBe(false);
    expect(f).not.toHaveBeenCalled();
  });
});

describe('api', () => {
  it('sends the bearer token and parses JSON', async () => {
    setToken('t1');
    const f = vi.fn(async (_p: string, init?: RequestInit) => {
      expect(new Headers(init?.headers).get('Authorization')).toBe('Bearer t1');
      return json(200, { ok: true });
    });
    vi.stubGlobal('fetch', f);
    expect(await api('/api/v1/me')).toEqual({ ok: true });
  });
  it('surfaces API error message and hint', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json(409, { error: { message: 'exists', hint: 'pick another name', code: 'CONFLICT' } })));
    await expect(api('/api/v1/machines', { method: 'POST', body: '{}' })).rejects.toThrow('exists — pick another name');
  });
  it('calls the unauthorized handler on a persistent 401', async () => {
    const handler = vi.fn();
    setUnauthorizedHandler(handler);
    clearToken();
    vi.stubGlobal('fetch', vi.fn(async () => json(401, {})));
    await expect(api('/api/v1/me')).rejects.toThrow(AUTH_REQUIRED);
    expect(handler).toHaveBeenCalledOnce();
  });
  it('returns null for 204', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(null, { status: 204 })));
    expect(await api('/api/v1/machines/x', { method: 'DELETE' })).toBeNull();
  });
  it('falls back to a status message', () => {
    expect(errorMessage(500, null).message).toBe('Request failed (500)');
  });
});
