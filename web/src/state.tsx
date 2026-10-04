// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { api, authHeaders, q } from './api';
import type {
  Capabilities,
  CloudEvent,
  DoctorReport,
  GoldenBuild,
  Image,
  Job,
  List,
  Machine,
  Me,
  Settings,
  StorageInventory,
  Summary,
} from './types';

export type Page = 'overview' | 'machines' | 'images' | 'jobs' | 'activity' | 'settings';
export const PAGES: Page[] = ['overview', 'machines', 'images', 'jobs', 'activity', 'settings'];

export type Toast = { id: number; message: string };
export type ConfirmRequest = {
  title: string;
  message?: string;
  confirmLabel?: string;
  destructive?: boolean;
  input?: { label: string; placeholder?: string };
  resolve: (value: string | null) => void;
};

type Ctx = {
  ready: boolean;
  me: Me | null;
  projects: string[];
  project: string;
  setProject: (p: string) => void;
  images: Image[];
  caps: Capabilities | null;
  summary: Summary | null;
  machines: Machine[];
  events: CloudEvent[];
  jobs: Job[];
  golden: GoldenBuild[];
  activeGolden: GoldenBuild | null;
  settings: Settings | null;
  setSettings: (s: Settings | null) => void;
  doctor: DoctorReport | null;
  setDoctor: (d: DoctorReport | null) => void;
  storage: StorageInventory | null;
  streamLive: boolean;
  canOperate: boolean;
  refreshProject: () => Promise<void>;
  loadImages: () => Promise<void>;
  loadJobs: () => Promise<void>;
  loadGolden: () => Promise<void>;
  loadSettings: () => Promise<void>;
  loadStorage: () => Promise<void>;
  toast: (message: string) => void;
  toasts: Toast[];
  confirm: (req: Omit<ConfirmRequest, 'resolve'>) => Promise<string | null>;
  confirmReq: ConfirmRequest | null;
  page: Page;
  go: (p: Page) => void;
  createFor: { imageId?: string } | null;
  openCreate: (imageId?: string) => void;
  closeCreate: () => void;
  detailId: string | null;
  openDetail: (id: string) => void;
  closeDetail: () => void;
};

const AppContext = createContext<Ctx | null>(null);

export function useApp(): Ctx {
  const c = useContext(AppContext);
  if (!c) throw new Error('useApp outside AppProvider');
  return c;
}

function pageFromHash(): Page {
  const h = window.location.hash.replace(/^#\/?/, '') as Page;
  return PAGES.includes(h) ? h : 'overview';
}

function storedProject(): string {
  try {
    return localStorage.getItem('kryton.project') || '';
  } catch {
    return '';
  }
}

export function AppProvider({ children, onFatal }: { children: ReactNode; onFatal: (msg: string) => void }) {
  const [ready, setReady] = useState(false);
  const [me, setMe] = useState<Me | null>(null);
  const [projects, setProjects] = useState<string[]>([]);
  const [project, setProjectState] = useState('');
  const [images, setImages] = useState<Image[]>([]);
  const [caps, setCaps] = useState<Capabilities | null>(null);
  const [summary, setSummary] = useState<Summary | null>(null);
  const [machines, setMachines] = useState<Machine[]>([]);
  const [events, setEvents] = useState<CloudEvent[]>([]);
  const [jobs, setJobs] = useState<Job[]>([]);
  const [golden, setGolden] = useState<GoldenBuild[]>([]);
  const [settings, setSettings] = useState<Settings | null>(null);
  const [doctor, setDoctor] = useState<DoctorReport | null>(null);
  const [storage, setStorage] = useState<StorageInventory | null>(null);
  const [streamLive, setStreamLive] = useState(false);
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [confirmReq, setConfirmReq] = useState<ConfirmRequest | null>(null);
  const [page, setPage] = useState<Page>(pageFromHash);
  const [createFor, setCreateFor] = useState<{ imageId?: string } | null>(null);
  const [detailId, setDetailId] = useState<string | null>(null);
  const projectRef = useRef('');
  const toastSeq = useRef(0);

  const toast = useCallback((message: string) => {
    const id = ++toastSeq.current;
    setToasts((t) => [...t.slice(-2), { id, message }]);
    const ms = Math.min(12000, Math.max(2800, String(message).length * 40));
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), ms);
  }, []);

  const confirm = useCallback(
    (req: Omit<ConfirmRequest, 'resolve'>) =>
      new Promise<string | null>((resolve) => {
        setConfirmReq({
          ...req,
          resolve: (v) => {
            setConfirmReq(null);
            resolve(v);
          },
        });
      }),
    [],
  );

  const go = useCallback((p: Page) => {
    if (window.location.hash !== `#/${p}`) window.location.hash = `/${p}`;
    setPage(p);
    window.scrollTo({ top: 0, behavior: 'smooth' });
  }, []);

  useEffect(() => {
    const onHash = () => setPage(pageFromHash());
    window.addEventListener('hashchange', onHash);
    return () => window.removeEventListener('hashchange', onHash);
  }, []);

  const loadJobs = useCallback(async () => {
    try {
      const res = await api<List<Job>>('/api/v1/jobs');
      setJobs(res.items || []);
    } catch {
      setJobs([]);
    }
  }, []);

  const refreshProject = useCallback(async () => {
    const p = projectRef.current;
    if (!p) return;
    const [s, ms, ev] = await Promise.all([
      api<Summary>(`/api/v1/summary?project=${q(p)}`),
      api<List<Machine>>(`/api/v1/machines?project=${q(p)}`),
      api<List<CloudEvent>>('/api/v1/events?limit=100'),
    ]);
    if (projectRef.current !== p) return;
    setSummary(s);
    setMachines(ms.items || []);
    setEvents(ev.items || []);
    loadJobs();
  }, [loadJobs]);

  const loadImages = useCallback(async () => {
    try {
      const res = await api<List<Image>>('/api/v1/images');
      setImages(res.items || []);
    } catch {
      /* keep the last list */
    }
  }, []);

  const loadGolden = useCallback(async () => {
    try {
      const res = await api<List<GoldenBuild>>('/api/v1/golden');
      setGolden(res.items || []);
    } catch {
      setGolden([]);
    }
  }, []);

  const loadSettings = useCallback(async () => {
    try {
      const s = await api<Settings>('/api/v1/settings');
      setSettings(s);
      setDoctor(s.doctor || null);
    } catch {
      setSettings(null);
      try {
        const res = await fetch('/api/v1/doctor', { headers: authHeaders() });
        setDoctor((await res.json()) as DoctorReport);
      } catch {
        setDoctor(null);
      }
    }
  }, []);

  const loadStorage = useCallback(async () => {
    try {
      setStorage(await api<StorageInventory>('/api/v1/storage'));
    } catch {
      setStorage(null);
    }
  }, []);

  const setProject = useCallback(
    (p: string) => {
      projectRef.current = p;
      setProjectState(p);
      try {
        localStorage.setItem('kryton.project', p);
      } catch {
        /* private mode */
      }
      setSummary(null);
      setMachines([]);
      refreshProject().catch((e: Error) => toast(e.message));
    },
    [refreshProject, toast],
  );

  // Bootstrap once the shell mounts (the token is already in session storage).
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const [meRes, projRes, imgRes, capRes] = await Promise.all([
          api<Me>('/api/v1/me'),
          api<List<string>>('/api/v1/projects'),
          api<List<Image>>('/api/v1/images'),
          api<Capabilities>('/api/v1/capabilities'),
        ]);
        if (cancelled) return;
        const ps = projRes.items || [];
        if (!ps.length) throw new Error('No projects are available to this identity');
        const saved = storedProject();
        const p = ps.includes(saved) ? saved : ps[0];
        projectRef.current = p;
        setMe(meRes);
        setProjects(ps);
        setProjectState(p);
        setImages(imgRes.items || []);
        setCaps(capRes);
        await Promise.all([refreshProject(), loadSettings(), loadGolden(), loadJobs(), loadStorage()]);
        if (!cancelled) setReady(true);
      } catch (e) {
        if (!cancelled) onFatal((e as Error).message);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  // Background refresh + CloudEvents stream.
  useEffect(() => {
    if (!ready) return;
    const timer = setInterval(() => refreshProject().catch(() => {}), 15000);
    const ac = new AbortController();
    let debounce: ReturnType<typeof setTimeout> | undefined;
    (async () => {
      try {
        const res = await fetch('/api/v1/events/stream', {
          headers: { ...authHeaders(), Accept: 'text/event-stream' },
          signal: ac.signal,
        });
        if (!res.ok || !res.body) return;
        setStreamLive(true);
        const reader = res.body.getReader();
        const dec = new TextDecoder();
        let buf = '';
        for (;;) {
          const { value, done } = await reader.read();
          if (done) break;
          buf += dec.decode(value, { stream: true });
          const parts = buf.split('\n\n');
          buf = parts.pop() || '';
          for (const part of parts) {
            if (!part.includes('event: cloudevent')) continue;
            const line = part.split('\n').find((l) => l.startsWith('data: '));
            if (!line) continue;
            try {
              const e = JSON.parse(line.slice(6)) as CloudEvent;
              setEvents((cur) => [e, ...cur.filter((x) => x.id !== e.id)].slice(0, 100));
              clearTimeout(debounce);
              debounce = setTimeout(() => refreshProject().catch(() => {}), 400);
            } catch {
              /* ignore malformed frames */
            }
          }
        }
      } catch {
        /* aborted or stream unsupported */
      } finally {
        setStreamLive(false);
      }
    })();
    return () => {
      clearInterval(timer);
      clearTimeout(debounce);
      ac.abort();
    };
  }, [ready, refreshProject]);

  const runningJobs = jobs.some((j) => j.state === 'running');
  useEffect(() => {
    if (!ready || !runningJobs) return;
    const t = setInterval(() => loadJobs(), 5000);
    return () => clearInterval(t);
  }, [ready, runningJobs, loadJobs]);

  const activeGolden = useMemo(
    () => golden.find((b) => !['ready', 'failed', 'idle'].includes(b.state)) || golden[0] || null,
    [golden],
  );
  const goldenBusy = !!activeGolden && (!['ready', 'failed', 'idle'].includes(activeGolden.state) || activeGolden.bootstrapState === 'running');
  useEffect(() => {
    if (!ready || !goldenBusy) return;
    const t = setInterval(() => loadGolden(), 8000);
    return () => clearInterval(t);
  }, [ready, goldenBusy, loadGolden]);

  const storageBusy = storage?.setup?.state === 'running';
  useEffect(() => {
    if (!ready || !storageBusy) return;
    const t = setInterval(() => loadStorage(), 4000);
    return () => clearInterval(t);
  }, [ready, storageBusy, loadStorage]);

  const role = me?.role;
  const value: Ctx = {
    ready,
    me,
    projects,
    project,
    setProject,
    images,
    caps,
    summary,
    machines,
    events,
    jobs,
    golden,
    activeGolden,
    settings,
    setSettings,
    doctor,
    setDoctor,
    storage,
    streamLive,
    canOperate: !role || role === 'operator' || role === 'admin',
    refreshProject,
    loadImages,
    loadJobs,
    loadGolden,
    loadSettings,
    loadStorage,
    toast,
    toasts,
    confirm,
    confirmReq,
    page,
    go,
    createFor,
    openCreate: (imageId?: string) => setCreateFor({ imageId }),
    closeCreate: () => setCreateFor(null),
    detailId,
    openDetail: (id: string) => setDetailId(id),
    closeDetail: () => setDetailId(null),
  };
  return <AppContext.Provider value={value}>{children}</AppContext.Provider>;
}
