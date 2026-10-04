// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import { useApp } from '../state';

export default function DoctorBanner() {
  const { doctor, go } = useApp();
  if (!doctor || doctor.healthy) return null;
  const findings = doctor.findings || [];
  const lead = findings.find((f) => f.status === 'fail') || findings.find((f) => f.status === 'warn');
  if (!lead) return null;
  const failed = lead.status === 'fail';
  return (
    <div className={`banner ${failed ? 'banner-bad' : 'banner-warn'}`} role="status">
      <div>
        <p className="banner-kicker">Setup check · {lead.check}</p>
        <strong>{lead.message}</strong>
        {lead.hint && <p>{lead.hint}</p>}
      </div>
      <button type="button" className="btn-secondary compact" onClick={() => go('settings')}>
        Review
      </button>
    </div>
  );
}
