// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

const TOKEN_KEY = 'kryton.token';
const SIGNED_OUT_KEY = 'kryton.signedOut';

export class ApiError extends Error {
  code?: string;
  hint?: string;
  requestId?: string;
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

export const AUTH_REQUIRED = 'Authentication required';

let onUnauthorized: () => void = () => {};
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn;
}

function storage(): Storage | null {
  try {
    return globalThis.sessionStorage ?? null;
  } catch {
    return null;
  }
}

export function token(): string {
  return storage()?.getItem(TOKEN_KEY) || '';
}

export function setToken(tok: string) {
  const s = storage();
  if (!s) return;
  s.setItem(TOKEN_KEY, tok);
  s.removeItem(SIGNED_OUT_KEY);
}

export function clearToken() {
  const s = storage();
  if (!s) return;
  s.removeItem(TOKEN_KEY);
  s.setItem(SIGNED_OUT_KEY, '1');
}

export function authHeaders(extra: Record<string, string> = {}): Record<string, string> {
  const h: Record<string, string> = { Accept: 'application/json', ...extra };
  const t = token();
  if (t) h.Authorization = 'Bearer ' + t;
  return h;
}

/** Lab hosts can hand out a token without a prompt (KRYTON_LAB_AUTO_AUTH). */
export async function ensureAuth(): Promise<boolean> {
  if (token()) return true;
  if (storage()?.getItem(SIGNED_OUT_KEY) === '1') return false;
  try {
    const res = await fetch('/api/v1/lab/bootstrap', { headers: { Accept: 'application/json' } });
    if (!res.ok) return false;
    const b = (await res.json()) as { autoAuth?: boolean; token?: string };
    if (b.autoAuth && b.token) {
      storage()?.setItem(TOKEN_KEY, b.token);
      return true;
    }
  } catch {
    /* lab bootstrap is optional */
  }
  return false;
}

/** True when krytond runs with KRYTON_AUTH_MODE=disabled and answers without a key. */
export async function openAccess(): Promise<boolean> {
  if (storage()?.getItem(SIGNED_OUT_KEY) === '1') return false;
  try {
    const res = await fetch('/api/v1/me', { headers: { Accept: 'application/json' } });
    return res.ok && (res.headers.get('content-type') || '').includes('application/json');
  } catch {
    return false;
  }
}

export function errorMessage(status: number, body: unknown): ApiError {
  const err = (body as { error?: { message?: string; hint?: string; code?: string; requestId?: string } } | null)?.error || {};
  const msg = err.message || `Request failed (${status})`;
  const e = new ApiError(msg + (err.hint ? ` — ${err.hint}` : ''), status);
  e.code = err.code;
  e.hint = err.hint;
  e.requestId = err.requestId;
  return e;
}

export async function api<T = unknown>(path: string, opts: RequestInit = {}): Promise<T> {
  const run = () => {
    const headers = new Headers(opts.headers || {});
    headers.set('Accept', 'application/json');
    if (opts.body) headers.set('Content-Type', 'application/json');
    const t = token();
    if (t) headers.set('Authorization', 'Bearer ' + t);
    return fetch(path, { ...opts, headers });
  };
  let res = await run();
  if (res.status === 401) {
    if (await ensureAuth()) res = await run();
    if (res.status === 401) {
      onUnauthorized();
      throw new ApiError(AUTH_REQUIRED, 401);
    }
  }
  if (res.status === 204) return null as T;
  let body: unknown = null;
  try {
    body = await res.json();
  } catch {
    /* empty or non-JSON body */
  }
  if (!res.ok) throw errorMessage(res.status, body);
  return body as T;
}

/** Endpoints that return a useful JSON report even on non-2xx (tests, doctor). */
export async function apiReport<T = unknown>(path: string, method = 'GET', body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: authHeaders(body !== undefined ? { 'Content-Type': 'application/json' } : {}),
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  return (await res.json()) as T;
}

export const q = encodeURIComponent;
