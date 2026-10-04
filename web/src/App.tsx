// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import { useCallback, useEffect, useState } from 'react';
import { AUTH_REQUIRED, clearToken, ensureAuth, openAccess, setUnauthorizedHandler, token } from './api';
import { AppProvider, useApp } from './state';
import Nav from './components/Nav';
import Toasts from './components/Toasts';
import ConfirmDialog from './components/ConfirmDialog';
import Login from './pages/Login';
import Overview from './pages/Overview';
import Machines from './pages/Machines';
import Images from './pages/Images';
import Jobs from './pages/Jobs';
import Activity from './pages/Activity';
import SettingsPage from './pages/Settings';
import CreateMachine from './pages/CreateMachine';
import MachineDetail from './pages/MachineDetail';
import { currentTheme, applyTheme, type Theme } from './theme';

type Gate = 'checking' | 'login' | 'app';

export default function App() {
  const [gate, setGate] = useState<Gate>('checking');
  const [loginError, setLoginError] = useState('');
  const [session, setSession] = useState(0);
  const [theme, setTheme] = useState<Theme>(currentTheme);

  const toLogin = useCallback((msg = '') => {
    setLoginError(msg);
    setGate('login');
  }, []);

  useEffect(() => {
    setUnauthorizedHandler(() => toLogin('That API key was not accepted.'));
    (async () => {
      if ((await ensureAuth()) && token()) setGate('app');
      else setGate((await openAccess()) ? 'app' : 'login');
    })();
  }, [toLogin]);

  useEffect(() => {
    document.title = gate === 'app' ? 'Kryton · Zyvor' : `Sign in · Kryton · ${window.location.hostname || 'localhost'}`;
  }, [gate]);

  const toggleTheme = () => {
    const next: Theme = theme === 'dark' ? 'light' : 'dark';
    applyTheme(next);
    setTheme(next);
  };

  const signOut = () => {
    clearToken();
    setSession((s) => s + 1);
    toLogin('');
  };

  if (gate === 'checking') return <div className="boot-splash" aria-busy="true" />;
  if (gate === 'login')
    return (
      <Login
        initialError={loginError}
        onLogin={() => {
          setLoginError('');
          setSession((s) => s + 1);
          setGate('app');
        }}
      />
    );
  return (
    <AppProvider
      key={session}
      onFatal={(msg) => {
        if (msg === AUTH_REQUIRED) toLogin('That API key was not accepted.');
        else toLogin(msg);
      }}
    >
      <Shell theme={theme} onToggleTheme={toggleTheme} onSignOut={signOut} />
    </AppProvider>
  );
}

function Shell({ theme, onToggleTheme, onSignOut }: { theme: Theme; onToggleTheme: () => void; onSignOut: () => void }) {
  const app = useApp();
  return (
    <>
      <Nav theme={theme} onToggleTheme={onToggleTheme} onSignOut={onSignOut} />
      <main id="main">
        {!app.ready ? (
          <PageSkeleton />
        ) : (
          <>
            {app.page === 'overview' && <Overview />}
            {app.page === 'machines' && <Machines />}
            {app.page === 'images' && <Images />}
            {app.page === 'jobs' && <Jobs />}
            {app.page === 'activity' && <Activity />}
            {app.page === 'settings' && <SettingsPage onSignOut={onSignOut} />}
          </>
        )}
      </main>
      <footer className="site-footer">
        Kryton by{' '}
        <a href="https://zyvor.dev" target="_blank" rel="noopener noreferrer">
          Zyvor
        </a>{' '}
        · Apache License 2.0
      </footer>
      {app.createFor && <CreateMachine />}
      {app.detailId && <MachineDetail />}
      <ConfirmDialog />
      <Toasts />
    </>
  );
}

function PageSkeleton() {
  return (
    <div className="page-skeleton" aria-busy="true" aria-label="Loading Kryton">
      <div className="skeleton skeleton-eyebrow" />
      <div className="skeleton skeleton-title" />
      <div className="skeleton skeleton-lede" />
      <div className="skeleton skeleton-band" />
      <div className="skeleton skeleton-card" />
    </div>
  );
}
