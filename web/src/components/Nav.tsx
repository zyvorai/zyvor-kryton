// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import { useApp, type Page } from '../state';
import type { Theme } from '../theme';
import Icon from './Icon';

const LINKS: { page: Page; label: string }[] = [
  { page: 'overview', label: 'Overview' },
  { page: 'machines', label: 'Machines' },
  { page: 'images', label: 'Images' },
  { page: 'jobs', label: 'Jobs' },
  { page: 'activity', label: 'Activity' },
  { page: 'settings', label: 'Settings' },
];

export default function Nav({ theme, onToggleTheme, onSignOut }: { theme: Theme; onToggleTheme: () => void; onSignOut: () => void }) {
  const app = useApp();
  const running = app.jobs.filter((j) => j.state === 'running').length;
  return (
    <nav className="nav" aria-label="Global">
      <div className="nav-inner">
        <button type="button" className="brand" onClick={() => app.go('overview')} aria-label="Kryton home">
          <img src="/zyvor-mark.svg" alt="" className="brand-mark" aria-hidden />
          Kryton
        </button>
        <div className="navlinks">
          {LINKS.map((l) => (
            <button
              key={l.page}
              type="button"
              className={app.page === l.page ? 'active' : ''}
              aria-current={app.page === l.page ? 'page' : undefined}
              onClick={() => app.go(l.page)}
            >
              {l.label}
              {l.page === 'jobs' && running > 0 && (
                <span className="nav-badge" aria-label={`${running} running`}>
                  {running}
                </span>
              )}
            </button>
          ))}
        </div>
        <div className="nav-actions">
          {app.projects.length > 1 && (
            <select
              className="nav-project"
              aria-label="Project"
              value={app.project}
              onChange={(e) => app.setProject(e.target.value)}
            >
              {app.projects.map((p) => (
                <option key={p} value={p}>
                  {p}
                </option>
              ))}
            </select>
          )}
          {app.ready && app.canOperate && (
            <button type="button" className="primary nav-create" onClick={() => app.openCreate()}>
              <Icon name="plus" size={14} />
              <span>New machine</span>
            </button>
          )}
          <button type="button" className="theme-toggle" onClick={onSignOut} aria-label="Sign out" title="Sign out">
            <Icon name="logout" />
          </button>
          <button
            type="button"
            className="theme-toggle"
            onClick={onToggleTheme}
            aria-label={theme === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'}
            title={theme === 'dark' ? 'Light' : 'Dark'}
          >
            <Icon name={theme === 'dark' ? 'sun' : 'moon'} />
          </button>
        </div>
      </div>
    </nav>
  );
}
