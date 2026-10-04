// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import { availability, availabilityLabel, isLinux, osLabel, storageFoot, supportsProvider } from '../lib/images';
import type { Image } from '../types';
import Icon from './Icon';
import { Chip } from './kit';

export function osIcon(img: Image): string {
  if (isLinux(img)) return 'linux';
  return img.family === 'windows-server' ? 'server' : 'windows';
}

export default function ImageCard({
  img,
  provider,
  onDeploy,
}: {
  img: Image;
  provider?: string;
  onDeploy?: (id: string) => void;
}) {
  const a = availability(img);
  const compatible = supportsProvider(img, provider);
  return (
    <article className={`image-card os-${isLinux(img) ? 'linux' : 'windows'}${compatible ? '' : ' incompatible'}`}>
      <div className="image-card-top">
        <span className="image-card-icon">
          <Icon name={osIcon(img)} size={18} />
        </span>
        <span className="image-card-kicker">{osLabel(img)}</span>
      </div>
      <h3>{img.name}</h3>
      <div className="image-card-version">{img.version}</div>
      <p className="image-card-desc">{img.description}</p>
      <div className="image-card-meta">
        <Chip tone={a === 'stored' ? 'good' : a === 'on-demand' ? 'info' : 'neutral'}>{availabilityLabel(img)}</Chip>
        {img.storageSource === 'golden' && img.certified && <Chip tone="good">Certified</Chip>}
        {img.storageSource === 'golden' && !img.certified && a === 'stored' && <Chip tone="warn">Unverified</Chip>}
        {!compatible && <Chip tone="warn">Not on {provider}</Chip>}
        {(img.tags || []).slice(0, 2).map((t) => (
          <Chip key={t}>{t}</Chip>
        ))}
      </div>
      <div className="image-card-foot">
        <small title={storageFoot(img)}>{storageFoot(img)}</small>
        {onDeploy && img.ready && compatible && (
          <button type="button" className="btn-diag compact" onClick={() => onDeploy(img.id)}>
            Deploy
          </button>
        )}
      </div>
    </article>
  );
}

export function ImageOption({
  img,
  selected,
  disabled,
  onSelect,
}: {
  img: Image;
  selected: boolean;
  disabled: boolean;
  onSelect: (id: string) => void;
}) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={selected}
      disabled={disabled}
      title={disabled ? 'Fetch or build this image before deploying it' : undefined}
      className={`image-option${selected ? ' selected' : ''}`}
      onClick={() => onSelect(img.id)}
    >
      <span className={`image-card-icon small${isLinux(img) ? ' linux' : ''}`}>
        <Icon name={osIcon(img)} size={16} />
      </span>
      <span className="image-option-copy">
        <strong>{img.name}</strong>
        <span>
          {img.version} · {availabilityLabel(img)}
        </span>
      </span>
    </button>
  );
}
