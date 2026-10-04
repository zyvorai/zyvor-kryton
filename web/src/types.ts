// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

export type DockurOptions = {
  username?: string;
  password?: string;
  hostname?: string;
  language?: string;
  region?: string;
  keyboard?: string;
  productKey?: string;
  domain?: string;
  domainOu?: string;
  autologin?: boolean;
  audio?: boolean;
  secureBoot?: boolean;
  sharedDir?: string;
  oemDir?: string;
  command?: string;
  customIso?: string;
  edition?: string;
  extraDisksGiB?: number[];
};

export type Initialization = { username?: string; sshAuthorizedKeys?: string[] };

export type MachineSpec = {
  name: string;
  image: string;
  initialization?: Initialization;
  compute: { cpu: number; memoryMiB: number };
  disk: { sizeGiB: number; storageClass?: string };
  network?: { networkId?: string };
  ttlMinutes?: number;
  dockur?: DockurOptions;
};

export type Condition = { type: string; status: string; reason?: string; message?: string };

export type Machine = {
  id: string;
  project: string;
  provider: string;
  state: string;
  spec: MachineSpec;
  providerRef?: { provider: string; namespace?: string; name: string };
  ipAddresses?: string[];
  consoleUrl?: string;
  rdpHost?: string;
  rdpPort?: number;
  rdpUsername?: string;
  progressPercent?: number;
  message?: string;
  conditions?: Condition[];
  createdAt: string;
  updatedAt?: string;
  expiresAt?: string;
};

export type Snapshot = { id: string; name: string; state: string; message?: string; createdAt: string };

export type Image = {
  id: string;
  name: string;
  version: string;
  family: string;
  description: string;
  os?: string;
  architecture?: string;
  defaultUser?: string;
  providers?: string[];
  minCpu: number;
  minMemoryMiB: number;
  defaultDiskGiB: number;
  tags?: string[];
  availability?: 'stored' | 'on-demand' | 'catalog' | string;
  storageSource?: string;
  storageNamespace?: string;
  storagePath?: string;
  ready: boolean;
  certified?: boolean;
  validationScore?: number;
};

export type Capabilities = {
  provider: string;
  snapshots: boolean;
  networks: boolean;
  ttl: boolean;
  liveMigration: boolean;
  console: boolean;
  goldenImages: boolean;
};

export type Me = { name: string; role?: string; projects?: string[] };

export type Summary = {
  project: string;
  provider: string;
  machines: number;
  running: number;
  stopped: number;
  attention: number;
  cpu: number;
  memoryMiB: number;
};

export type CloudEvent = {
  id: string;
  type: string;
  subject?: string;
  time?: string;
  data?: { name?: string; machineId?: string; project?: string; [k: string]: unknown };
};

export type JobStep = { label: string; state: string; detail?: string };
export type JobLog = { time?: string; level?: string; message?: string };
export type Job = {
  id: string;
  kind: string;
  name: string;
  state: string;
  project?: string;
  message?: string;
  progressPercent?: number;
  consoleUrl?: string;
  steps?: JobStep[];
  logs?: JobLog[];
};

export type GoldenBuild = {
  id: string;
  state: string;
  phase?: string;
  progressPercent?: number;
  message?: string;
  outputPath?: string;
  consoleUrl?: string;
  certified?: boolean;
  validationScore?: number;
  passportPath?: string;
  bootstrapState?: string;
  bootstrapMessage?: string;
  dataSource?: string;
};

export type DoctorFinding = { check: string; status: string; message: string; hint?: string };
export type DoctorReport = { provider?: string; healthy: boolean; findings?: DoctorFinding[] };

export type Probe = { name: string; status: string; message: string; latencyMs?: number; hint?: string };

export type AtlasSettings = {
  enabled?: boolean;
  baseUrl?: string;
  product?: string;
  preferAtlas?: boolean;
};

export type Settings = {
  healthy?: boolean;
  doctor?: DoctorReport;
  connection?: { probes?: Probe[] };
  runtime?: {
    defaultProject?: string;
    imageNamespace?: string;
    storageClass?: string;
    eventWebhookUrl?: string;
    atlas?: AtlasSettings;
    atlasTokenSet?: boolean;
  };
  system?: {
    provider?: string;
    authMode?: string;
    kubernetesConnected?: boolean;
    kubernetesEndpoint?: string;
    projects?: string[];
    scriptsAvailable?: boolean;
    defaultProjectEnv?: string;
    imageNamespaceEnv?: string;
    settingsConfigFile?: string;
  };
};

export type ConnectionTest = {
  healthy?: boolean;
  doctor?: DoctorReport;
  connection?: { probes?: Probe[] };
};

export type AtlasTest = { healthy?: boolean; probes?: Probe[]; storageClasses?: string[] };

export type StorageClass = {
  name: string;
  backend: string;
  provisioner?: string;
  snapshotCapable?: boolean;
  snapshotClass?: string;
  volumeBindingMode?: string;
};

export type BlockDevice = { path: string; size?: string; model?: string; blocked?: boolean; recommended?: boolean };

export type StorageInventory = {
  config?: { storageClass?: string };
  storageClasses?: StorageClass[];
  scriptsAvailable?: boolean;
  backendsInstalled?: Record<string, boolean>;
  blockDevices?: BlockDevice[];
  setup?: { state?: string };
};

export type List<T> = { items?: T[] };
