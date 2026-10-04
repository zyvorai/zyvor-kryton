// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

import { useEffect, useMemo, useState } from 'react';
import { useApp } from '../state';
import { availability, sortImages, supportsProvider } from '../lib/images';
import { ListEmpty, PageHero, Segmented } from '../components/kit';
import ImageCard from '../components/ImageCard';
import GoldenPanel from '../components/GoldenPanel';
import type { Image } from '../types';

type OsFilter = 'all' | 'linux' | 'windows';

export default function Images() {
  const app = useApp();
  const provider = app.caps?.provider;
  const [os, setOs] = useState<OsFilter>(provider === 'libvirt' ? 'linux' : 'all');
  const [onlyCompatible, setOnlyCompatible] = useState(true);

  useEffect(() => {
    app.loadImages();
    app.loadGolden();
  }, []);

  const filtered = useMemo(
    () =>
      sortImages(app.images).filter(
        (i) =>
          (os === 'all' || (os === 'linux' ? i.os === 'linux' : i.os !== 'linux')) && (!onlyCompatible || supportsProvider(i, provider)),
      ),
    [app.images, os, onlyCompatible, provider],
  );
  const sections: { key: string; title: string; lede: string; items: Image[] }[] = [
    {
      key: 'stored',
      title: 'Stored on this host',
      lede: 'Verified artifacts, golden images and CDI DataSources — deploy immediately.',
      items: filtered.filter((i) => availability(i) === 'stored'),
    },
    {
      key: 'on-demand',
      title: 'Ready on demand',
      lede: 'Downloaded and installed automatically when you create a machine.',
      items: filtered.filter((i) => availability(i) === 'on-demand'),
    },
    {
      key: 'catalog',
      title: 'Catalog',
      lede:
        provider === 'libvirt'
          ? 'Fetch a checksum-pinned cloud image with kryton-image before deploying.'
          : 'Available after a golden image build or CDI bootstrap.',
      items: filtered.filter((i) => availability(i) === 'catalog'),
    },
  ];
  const linuxCount = app.images.filter((i) => i.os === 'linux').length;

  return (
    <div className="page">
      <PageHero
        eyebrow={`Images · ${provider}`}
        title="Pick a baseline. Deploy with confidence."
        lede={`${linuxCount} Linux cloud images and ${app.images.length - linuxCount} Windows builds in the catalog. Ready images deploy in one click.`}
      />
      <div className="toolbar-pill">
        <Segmented<OsFilter>
          label="Operating system"
          value={os}
          onChange={setOs}
          options={[
            { value: 'all', label: 'All' },
            { value: 'linux', label: 'Linux', count: linuxCount },
            { value: 'windows', label: 'Windows', count: app.images.length - linuxCount },
          ]}
        />
        <div className="toolbar-pill__trailing">
          <label>
            <input type="checkbox" checked={onlyCompatible} onChange={(e) => setOnlyCompatible(e.target.checked)} />
            Only {provider} images
          </label>
        </div>
      </div>

      {filtered.length === 0 && (
        <ListEmpty title="No images match">Try another operating system filter or include images for other providers.</ListEmpty>
      )}
      {sections
        .filter((s) => s.items.length)
        .map((s) => (
          <section key={s.key} className="image-section">
            <div className="section-head">
              <h2>{s.title}</h2>
              <p>{s.lede}</p>
            </div>
            <div className="image-grid">
              {s.items.map((img) => (
                <ImageCard key={img.id} img={img} provider={provider} onDeploy={app.canOperate ? app.openCreate : undefined} />
              ))}
            </div>
          </section>
        ))}

      {app.caps?.goldenImages && <GoldenPanel />}
    </div>
  );
}
