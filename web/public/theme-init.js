// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

// Applies the stored theme before first paint. Kept as an external file because
// krytond's CSP (script-src 'self') blocks inline scripts.
(function () {
  try {
    var t = localStorage.getItem('kryton-theme');
    if (t !== 'dark' && t !== 'light') t = 'light';
    document.documentElement.setAttribute('data-theme', t);
    var m = document.querySelector('meta[name="theme-color"]');
    if (m) m.setAttribute('content', t === 'dark' ? '#000000' : '#ffffff');
  } catch (e) {
    document.documentElement.setAttribute('data-theme', 'light');
  }
})();
