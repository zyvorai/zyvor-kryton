// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-1.0

import { useState } from 'react';
import { useApp } from '../state';
import Overlay from './Overlay';

export default function ConfirmDialog() {
  const { confirmReq } = useApp();
  if (!confirmReq) return null;
  return <Dialog key={confirmReq.title} />;
}

function Dialog() {
  const { confirmReq, toast } = useApp();
  const [value, setValue] = useState('');
  const [checked, setChecked] = useState(false);
  const [copied, setCopied] = useState(false);
  if (!confirmReq) return null;
  const secret = confirmReq.secret;
  // A copy-once secret cannot be dismissed by Escape or a stray click.
  const cancel = secret ? () => {} : () => confirmReq.resolve(null);
  const copySecret = async () => {
    try {
      await navigator.clipboard.writeText(secret!.value);
      setCopied(true);
    } catch {
      toast('Copy failed. Select the value and copy it manually.');
    }
  };
  return (
    <Overlay kind="dialog" label={confirmReq.title} onClose={cancel}>
      <form
        className="dialog-body"
        onSubmit={(e) => {
          e.preventDefault();
          confirmReq.resolve(confirmReq.checkbox ? (checked ? 'checked' : '') : value.trim());
        }}
      >
        <h2>{confirmReq.title}</h2>
        {confirmReq.message && <p>{confirmReq.message}</p>}
        {secret && (
          <div className="field secret-box">
            <span id="secret-label">{secret.label}</span>
            <div className="secret-row">
              <input
                className="input-field mono"
                readOnly
                value={secret.value}
                aria-labelledby="secret-label"
                onFocus={(e) => e.currentTarget.select()}
                data-testid="secret-value"
              />
              <button type="button" className="btn-secondary" onClick={copySecret} data-autofocus>
                {copied ? 'Copied' : 'Copy'}
              </button>
            </div>
            <span className="muted" role="status">
              This is the only time it is shown. Kryton does not return it again.
            </span>
          </div>
        )}
        {confirmReq.input && (
          <label className="field">
            <span>{confirmReq.input.label}</span>
            <input
              className="input-field"
              value={value}
              placeholder={confirmReq.input.placeholder}
              onChange={(e) => setValue(e.target.value)}
              data-autofocus
            />
          </label>
        )}
        {confirmReq.checkbox && (
          <label className="check-row">
            <input type="checkbox" checked={checked} onChange={(e) => setChecked(e.target.checked)} /> {confirmReq.checkbox.label}
          </label>
        )}
        <div className="dialog-actions">
          {!secret && (
            <button type="button" className="btn-secondary" onClick={cancel}>
              Cancel
            </button>
          )}
          <button
            type="submit"
            className={confirmReq.destructive ? 'danger' : 'primary'}
            data-autofocus={confirmReq.input || secret ? undefined : true}
          >
            {confirmReq.confirmLabel || 'Confirm'}
          </button>
        </div>
      </form>
    </Overlay>
  );
}
