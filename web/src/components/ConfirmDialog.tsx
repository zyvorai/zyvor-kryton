// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import { useState } from 'react';
import { useApp } from '../state';
import Overlay from './Overlay';

export default function ConfirmDialog() {
  const { confirmReq } = useApp();
  if (!confirmReq) return null;
  return <Dialog key={confirmReq.title} />;
}

function Dialog() {
  const { confirmReq } = useApp();
  const [value, setValue] = useState('');
  if (!confirmReq) return null;
  const cancel = () => confirmReq.resolve(null);
  return (
    <Overlay kind="dialog" label={confirmReq.title} onClose={cancel}>
      <form
        className="dialog-body"
        onSubmit={(e) => {
          e.preventDefault();
          confirmReq.resolve(value.trim());
        }}
      >
        <h2>{confirmReq.title}</h2>
        {confirmReq.message && <p>{confirmReq.message}</p>}
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
        <div className="dialog-actions">
          <button type="button" className="btn-secondary" onClick={cancel}>
            Cancel
          </button>
          <button type="submit" className={confirmReq.destructive ? 'danger' : 'primary'} data-autofocus={confirmReq.input ? undefined : true}>
            {confirmReq.confirmLabel || 'Confirm'}
          </button>
        </div>
      </form>
    </Overlay>
  );
}
