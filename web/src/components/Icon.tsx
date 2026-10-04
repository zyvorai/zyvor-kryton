// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

const PATHS: Record<string, string> = {
  plus: 'M12 5v14M5 12h14',
  close: 'M6 6l12 12M18 6 6 18',
  search: 'M11 18a7 7 0 1 0 0-14 7 7 0 0 0 0 14zM20 20l-4-4',
  sun: 'M12 16a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41',
  moon: 'M21 14.5A8.5 8.5 0 1 1 11.5 3a7 7 0 0 0 9.5 11.5z',
  logout: 'M15 4H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h8M10 12h11m0 0-3.5-3.5M21 12l-3.5 3.5',
  refresh: 'M20 11a8 8 0 1 0-2.3 5.7M20 4v7h-7',
  linux: 'M4 6h16v10H4zM8 20h8M12 16v4',
  windows: 'M3 5.5 10.5 4.5v7H3zM12 4.3 21 3v8.5h-9zM3 12.5h7.5v7L3 18.5zM12 12.5h9V21l-9-1.3z',
  server: 'M4 4h16v6H4zM4 14h16v6H4zM8 7h.01M8 17h.01',
  chevron: 'm9 6 6 6-6 6',
  copy: 'M9 9h10v10H9zM5 15V5h10',
  terminal: 'M4 5h16v14H4zM8 10l3 2-3 2M13 15h3',
  power: 'M12 3v9M6.3 7.3a8 8 0 1 0 11.4 0',
  camera: 'M4 8h3l2-3h6l2 3h3v11H4zM12 17a4 4 0 1 0 0-8 4 4 0 0 0 0 8z',
  trash: 'M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13',
};

export default function Icon({ name, size = 16, label }: { name: keyof typeof PATHS | string; size?: number; label?: string }) {
  return (
    <svg
      className="icon"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      role={label ? 'img' : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
    >
      <path d={PATHS[name] || ''} />
    </svg>
  );
}
