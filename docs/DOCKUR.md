---
hero:
  eyebrow: DOCKUR
  title: Dockur lab provider
---

Kryton can provision **real Windows guests** on a Linux host using [dockur/windows](https://github.com/dockur/windows) — the same engine behind [WinPodX](https://github.com/kernalix7/winpodx) — without a full KubeVirt cluster.

**Step-by-step lab workflow:** [USER-GUIDE.md § Lab operator](USER-GUIDE.md#2-lab-operator-dockur)

Use this provider for labs, demos, and developer workstations. For production estates, use the KubeVirt provider with Helm.

---

## Requirements

| Requirement | Notes |
|-------------|-------|
| Linux x86_64 | With hardware virtualization |
| `/dev/kvm` | KVM device accessible to your user |
| Docker or Podman | With Compose v2 (`docker compose` / `podman-compose`) |
| Disk space | ~32–64 GiB+ per Windows image |
| Network | Outbound HTTPS for dockur to fetch ISOs |

Run `krytonctl doctor` to validate all of the above before creating machines.

### Secure lab (recommended on shared hosts)

```bash
./scripts/ensure-api-keys.sh
cat ~/.kryton/lab.token          # show the bearer key
./scripts/harden-lab-services.sh
```

With `KRYTON_LAB_AUTO_AUTH=true` (set by `harden-lab-services.sh`), the UI **auto-connects** — no manual token paste. The bearer is read from `lab.token` via `GET /api/v1/lab/bootstrap` (lab-only; requires `KRYTON_ALLOW_INSECURE=true`).

Without auto-auth, open the UI login chapters and paste the token from `lab.token`.

For CLI/scripts: `export KRYTON_TOKEN=$(cat ~/.kryton/lab.token)`

Full guide (create, rotate, Helm, troubleshooting): [AUTH.md](AUTH.md) · [USER.md](USER.md).

---

## Enable

```bash
export KRYTON_PROVIDER=dockur
export KRYTON_AUTH_MODE=disabled          # lab only; use apikey on shared hosts
export KRYTON_DOCKUR_RUNTIME=docker       # or podman
export KRYTON_DOCKUR_PUBLIC_HOST=<host> # hostname/IP clients use for console + RDP
export KRYTON_DOCKUR_DATA_DIR=$HOME/.kryton/dockur

krytond
krytonctl doctor
```

### Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `KRYTON_DOCKUR_RUNTIME` | `docker` | Container runtime (`docker` or `podman`) |
| `KRYTON_DOCKUR_DATA_DIR` | *(temp)* | Persistent compose + disk state |
| `KRYTON_DOCKUR_PUBLIC_HOST` | `127.0.0.1` | Hostname in `consoleUrl` and RDP hints |
| `KRYTON_DOCKUR_HTTP_BASE` | `18006` | First HTTP console port (incremented per VM) |
| `KRYTON_DOCKUR_RDP_BASE` | `13389` | First RDP host port (incremented per VM) |
| `KRYTON_DOCKUR_MAX_UPLOAD_MIB` | `1024` | Largest single shared-folder upload |

---

## Image → VERSION map

Kryton image IDs map to dockur `VERSION` codes:

| Kryton image ID | dockur `VERSION` |
|-----------------|------------------|
| `windows-11-enterprise` | `11e` |
| `windows-11-pro` | `11` |
| `windows-11-ltsc` | `11l` |
| `windows-10-enterprise` | `10e` |
| `windows-10-pro` | `10` |
| `windows-10-ltsc` | `10l` |
| `windows-tiny11` | `tiny11` |
| `windows-tiny11-core` | `core11` |
| `windows-server-2025` | `2025` |
| `windows-server-2022` | `2022` |
| `windows-server-2019` | `2019` |
| `windows-server-2016` | `2016` |
| `windows-server-2012` | `2012` |
| `windows-tiny10` | `tiny10` |

Codes were checked against the [dockur/windows README](https://github.com/dockur/windows) `VERSION` table. Windows 11 entries are labelled 25H2; the LTSC entries are Windows 11 LTSC 2024 and Windows 10 LTSC 2021. dockur also offers Windows 8.1/7/Vista/XP/2000/ME/98/95, Server 2008/2003 and ReactOS; Kryton does not list those (add them with `KRYTON_IMAGES_FILE` and a `dockurVersion` if you need them).

**Unsupported:** dockur/windows is x86_64 only. Windows on ARM64 is a separate project (`dockur/windows-arm`); the dockur provider rejects ARM image IDs with `501 UNSUPPORTED`, and the catalog only accepts `amd64`.

Catalog entries carry `dockurVersion` so custom image files can override the built-in map.

---

## Create a machine

```bash
krytonctl create --image windows-11-enterprise --cpu 4 --memory 8192 \
  --dockur-username labuser \
  --dockur-language English --dockur-region en-US --dockur-audio \
  --dockur-extra-disks 32 lab-win01
krytonctl get <id>
```

While installing, the machine reports:

- `consoleUrl` — dockur web viewer (watch unattended setup)
- `progressPercent` — install progress when available
- `message` — current phase description
- `rdpHost` / `rdpPort` / `rdpUsername` — RDP endpoint (default user `Docker`)

Open the console URL in a browser. RDP is published on the host port starting at `KRYTON_DOCKUR_RDP_BASE`.

**Credentials.** The default user is `Docker`. If you do not pass `dockur.password`, Kryton generates a 20-character random password (upper, lower, digits) and returns it **once**, in the create response as `generatedPassword` (`krytonctl create` prints it with the rest of the response). It is not returned by any later call; it stays in the machine's `compose.yml` and `state.json` (mode 0600) under the data dir. There is no longer a default `admin` password.

**Network exposure.** RDP (TCP+UDP) and the web viewer bind to `127.0.0.1` by default, so a fresh machine is reachable only from the Kryton host (use an SSH tunnel or the proxied console). Set `dockur.publicBind: true` (`--dockur-public-bind`) to publish on `0.0.0.0`; then `rdpHost` is `KRYTON_DOCKUR_PUBLIC_HOST`, otherwise it is `127.0.0.1`. Machines created by older versions keep their original (all-interface) publishing until recreated.

**Ports.** Ports are allocated upward from the bases, skipping ports used by any other machine or busy on the host. `dockur.rdpPort` / `dockur.consolePort` pin a port (1024-65535); a port already allocated to another machine or in use on the host returns `409 CONFLICT` with the reason, and nothing is created.

### Dockur options (`spec.dockur`)

| Field | dockur env / volume | Notes |
|-------|---------------------|-------|
| `username` / `password` | `USERNAME` / `PASSWORD` | Password is write-only (redacted on GET); generated when omitted |
| `shared` | `<machineDir>/shared` → `/shared` | Guest `\\host.lan\Data` (the **Shared** desktop shortcut; a `Z:` drive is not guaranteed to be mapped in job sessions, so scripts should use the UNC path); enables the shared-folder API |
| `oemScript` | `<machineDir>/oem/install.bat` → `/oem` | Batch script run by dockur at the end of the first install (max 64 KiB) |
| `jobAgent` | agent in `/oem` | Installs the Kryton guest agent; enables the jobs API; implies `shared` |
| `publicBind` | port publishing address | `false` (default) = `127.0.0.1`, `true` = `0.0.0.0` |
| `rdpPort` / `consolePort` | host ports | Fixed host ports; `0`/omitted = auto |
| `hostname` | `HOST` | Defaults to machine name |
| `language` / `region` / `keyboard` | `LANGUAGE` / `REGION` / `KEYBOARD` | Locale |
| `productKey` | `KEY` | Activation key |
| `domain` / `domainOu` | `DOMAIN` / `DOMAIN_OU` | AD join during install |
| `sharedDir` | *(legacy)* single directory name under the machine dir → `/shared` | Absolute/host paths and `..` are rejected (400); prefer `shared` |
| `oemDir` | *(legacy)* single directory name under the machine dir → `/oem` | Prefer `oemScript` |
| `command` | `COMMAND` | Single post-install command |
| `customIso` | URL as `VERSION`, or file → `/custom.iso` | Bring-your-own media |
| `edition` | `EDITION` | e.g. `core` for Server Core |
| `audio` | `AUDIO=Y` | Web viewer audio |
| `secureBoot` | `BOOT_MODE=windows_secure` + `TPM=Y` | Win11-style secure guest |
| `extraDisksGiB` | `DISK2_SIZE`… + `/storage2`… | Up to 3 extra disks |
| `autologin` | `AUTOLOGIN=N` when `false` | Default dockur autologin stays on |

### API example

```bash
curl -s -X POST http://localhost:8080/api/v1/machines \
  -H 'Content-Type: application/json' \
  -d '{
    "project": "default",
    "name": "lab-win01",
    "image": "windows-11-enterprise",
    "compute": {"cpu": 4, "memoryMiB": 8192},
    "disk": {"sizeGiB": 80},
    "dockur": {
      "username": "labuser",
      "language": "English",
      "shared": true,
      "jobAgent": true,
      "audio": true
    }
  }' | jq '{id, generatedPassword}'
```

> **Upgrade note.** Earlier versions accepted arbitrary host paths in `sharedDir` / `oemDir`. That was a way to mount any host directory into a guest from the API, so those fields now only accept a plain directory name inside the machine directory. The web console no longer sends them; its create form offers `shared`, `oemScript`, `jobAgent`, `publicBind` and the port fields instead. Use `shared` and `oemScript` from the API too.

---

## Lifecycle

Start, stop, and delete work through the standard Kryton API:

```bash
krytonctl stop <id>
krytonctl start <id>
krytonctl delete <id>
```

Compose projects live under `KRYTON_DOCKUR_DATA_DIR`. Deleting a machine removes its compose stack.

`start` now leaves the machine in `starting` until RDP answers; `GET /machines/{id}` (or the list) flips it to `running` once the guest speaks RDP. A published port alone proves nothing, because the container runtime listens on it whether or not Windows is up, so Kryton sends an RDP X.224 negotiation and requires a reply.

### Restart (graceful) and readiness

```bash
krytonctl restart <id> --timeout 600     # ACPI shutdown, start, wait for RDP
krytonctl restart <id> --force           # kill instead of ACPI
krytonctl restart <id> --no-wait         # return immediately
krytonctl ready <id> --wait --timeout 300 && echo up
```

`POST /machines/{id}/restart` runs `compose stop -t 120` (dockur converts SIGTERM into an ACPI power-button press and waits for Windows to shut down cleanly) then `compose start`. With `wait` (default) it blocks until the guest is ready or `timeoutSeconds` (default 300, max 1800): `200` when ready, `202` if not yet. Events: `io.kryton.machine.restarted`, then `io.kryton.machine.ready`.

`GET /machines/{id}/ready` is a probe: `200` when ready, `503` with the same body when not. Checks:

| Check | Passes when |
|-------|-------------|
| `containerUp` | `compose ps --status running` lists the container |
| `rdpOpen` | RDP (TPKT/X.224) answers on the host RDP port |
| `agentPresent` | *(only for `jobAgent` machines)* `<shared>/.kryton/agent-ready` was touched within 90 s |

While not ready, `install` reports `{phase, percent, message}` parsed from `compose logs`: `starting`, `downloading` (percent from the log's `NN%`, mapped to 5-45), `extracting` (50), `building` (60), `installing` (70, Windows booting/unattended setup), then `complete` (100) only when RDP answers. This is a heuristic over dockur's human-readable log lines and can lag or be coarse; `GET /machines/{id}/logs?tail=N` returns the raw log. The same figures feed `progressPercent`/`message` on `GET /machines/{id}` while the machine is provisioning or starting.

---

## Unattended build/test workflows

Replace hand-edited compose files with three create options: a shared folder, an OEM first-boot script and a guest agent for running commands.

```bash
krytonctl create --image windows-11-enterprise --cpu 4 --memory 8192 --disk 96 \
  --dockur-job-agent --dockur-oem-script ./install.bat ci-win
# note the generatedPassword in the response
krytonctl ready <id> --wait --timeout 3600     # install takes a while

krytonctl shared-put <id> ./src.zip work/src.zip
krytonctl job-run <id> --wait --timeout 1800 --shell powershell --name build \
  --file ./build.ps1                           # streams output, exit code mirrors the job
krytonctl jobs <id>
krytonctl job-artifacts <id> <jobId>
krytonctl job-artifact <id> <jobId> out/app.zip -o app.zip
```

### Shared folder

`shared: true` creates `<KRYTON_DOCKUR_DATA_DIR>/<project>/<id>/shared` and mounts it at `/shared`; inside Windows it is `\\host.lan\Data`. Use that UNC path in job scripts: `Z:` is not mapped in the agent's session.

| Endpoint | Purpose |
|----------|---------|
| `GET /machines/{id}/shared?path=dir` | list one directory (Viewer) |
| `GET /machines/{id}/shared/{path}` | download (Viewer; always `application/octet-stream` attachment, Range supported) |
| `PUT /machines/{id}/shared/{path}` | upload raw body (Operator); `409` if it exists unless `?overwrite=true` |
| `DELETE /machines/{id}/shared/{path}` | delete a file or empty directory (Operator) |

Safety rules: paths are relative and cleaned; `..`, empty segments, backslashes, drive letters/`:`, control characters and the reserved top-level `.kryton` directory (the job protocol) are rejected with 400; **symbolic links are never followed or listed**, because the guest can write to the share and could plant one to reach host files. Uploads are capped at `KRYTON_DOCKUR_MAX_UPLOAD_MIB` (default 1024) and rejected with 413 (checked from `Content-Length`, enforced while streaming); they go through a temp file and rename, so a failed upload leaves nothing behind. Every call is project-scoped (the machine must belong to the requested project, and the identity needs the role for it). There is no total-size quota on the share; bound it with the filesystem the data dir lives on.

### OEM script

`oemScript` is written (CRLF) to `<machineDir>/oem/install.bat` and mounted at `/oem`; dockur copies it to `C:\OEM` and runs it at the end of the first installation. It runs once, at first boot only. Changing it later has no effect on an installed guest.

### Guest jobs

`jobAgent: true` adds a small PowerShell agent to the OEM script (`oem/kryton-agent.ps1`, registered in the all-users Startup folder and started immediately). It needs dockur's default autologin, since it starts at logon. It polls `\\host.lan\Data\.kryton\jobs` (falling back to `Z:\`) every 2 s and touches `.kryton/agent-ready` as a heartbeat. No WinRM or SSH is involved: the share is the only channel, so jobs work with no guest networking set up. (WinRM/SSH executors are not implemented.)

```bash
curl -X POST "$KRYTON_URL/api/v1/machines/$ID/jobs" -H "Authorization: Bearer $KRYTON_TOKEN" \
  -d '{"name":"build","shell":"powershell","script":"msbuild app.sln; Copy-Item bin\\*.zip $env:KRYTON_ARTIFACTS","timeoutSeconds":1800,"env":{"CONFIG":"Release"}}'
```

| Endpoint | Purpose |
|----------|---------|
| `POST /machines/{id}/jobs` | submit (Operator) -> `202`; `409 PRECONDITION_FAILED` without `jobAgent` |
| `GET /machines/{id}/jobs`, `.../{jid}` | list (newest 200) / status, `exitCode`, `artifacts` |
| `POST /machines/{id}/jobs/{jid}/cancel` | cancel (Operator); immediate if not started, else the agent kills the process tree |
| `GET /machines/{id}/jobs/{jid}/logs?offset=&limit=` | combined stdout+stderr; headers `X-Log-Size`, `X-Log-Next-Offset` |
| `GET /machines/{id}/jobs/{jid}/artifacts[/{path}]` | files the job left in `$env:KRYTON_ARTIFACTS` |

States: `queued` -> `running` -> `succeeded` (exit 0) / `failed` (non-zero) / `timed_out` / `canceled`. `shell` is `cmd` (default; the script runs as a batch file) or `powershell`. The script sees `KRYTON_JOB_ID`, `KRYTON_ARTIFACTS` and `KRYTON_SHARED` plus your `env` (names `[A-Za-z_][A-Za-z0-9_]*`, not starting `KRYTON_`). Limits: script 64 KiB, timeout 1 s to 24 h (default 10 min), 32 env vars.

Timeouts are enforced twice: by the agent (kills the process tree) and by Kryton (a running job still unreported 60 s past its timeout becomes `timed_out`; a queued job nobody picks up within 10 minutes becomes `failed` with "no guest agent picked up the job"). Job state lives in files in the share, so it survives a krytond restart.

**CloudEvents** (also on `/api/v1/events` and `/events/stream`, project-scoped): `io.kryton.machine.job.submitted` and `io.kryton.machine.job.completed` with `{project, machineId, jobId, name, state, exitCode, durationSeconds, message}`. Completion is detected by a 5 s background monitor, so it fires even when nobody polls; each job completes exactly once.

**Trust model.** Everything the guest writes to the share (`status.json`, logs, artifacts) is untrusted: status is parsed with a size cap and a fixed schema, artifact paths are validated and symlink-checked, downloads are forced to attachments. A submitted script runs with the autologin user's rights inside the VM; submitting jobs therefore needs Operator on the project.

**Caveat.** The PowerShell agent could not be exercised against a real Windows guest in Kryton's test suite (CI has no KVM); the host side is covered with a fake docker runtime and a simulated agent. Try it on a lab VM before relying on it, and check `\\host.lan\Data\.kryton\agent-ready` appears after first boot.

---

## Operating notes from a real Windows lab

Observed while running a Windows 11 guest end to end on a shared Linux host:

- **`ready` means RDP answers, not "the desktop is usable".** Windows can accept the RDP handshake while it is still at the first-sign-in preparation screen ("This might take a few minutes"). For
  scripted work, wait for the job agent heartbeat (`agentPresent`) or poll a flag your script writes into the shared folder.
- **The first-boot (OEM) script runs once, when the install finishes.** It is read from `/oem` at that moment, so swapping `install.bat` afterwards does nothing; create the machine with the
  script you want. Anything long-running there (toolchain installs) shares a disk with Windows setup.
- **Storage speed decides everything.** Install time is dominated by disk writes. On a host where other workloads keep disk I/O wait at 25–30%, a Windows 11 install took several hours and
  installing Rust inside the guest ran at hundreds of bytes per second. Prefer a dedicated or SSD-backed data directory, and do heavy builds elsewhere (cross-compile instead).
- **A black web viewer is usually a sleeping display.** Move the mouse or press a key in the viewer; the sign-in screen and autologin follow. After a restart the desktop can take a few minutes.
- **Install progress is a heuristic.** The percentage comes from container log lines and stays low while Windows setup runs inside the guest; trust the guest screen for the real stage.
- **Deleting a machine removes its container and its data** (`DELETE /api/v1/machines/{id}`), including the shared folder, so copy artifacts out first.
- **Compose is required.** The provider shells out to `docker compose`; install the Compose plugin (`docker-compose-v2` on Ubuntu) on the lab host. With rootless Podman and no compose provider the create call fails.
- **Do not enable `KRYTON_LAB_AUTO_AUTH` on a host with a public address.** It hands the API key to any unauthenticated visitor of the console; use API-key sign-in there.

## Events

Dockur provisioning emits `io.kryton.machine.install.started` when unattended setup begins, in addition to the standard create/start/stop/delete events.

---

## Doctor checks

`krytonctl doctor` (or `GET /api/v1/doctor`) validates:

- Auth mode appropriateness for a non-demo provider
- Project configuration
- Image catalog
- Provider health
- Container runtime binary (`docker` / `podman`)
- Compose availability
- `/dev/kvm` access
- Data directory writability

---

## Security notes

- The dockur provider is **lab-oriented**. On shared hosts, set `KRYTON_AUTH_MODE=apikey`.
- Console and RDP ports bind to `127.0.0.1` unless `publicBind` is set; when you opt in, restrict them with firewall rules. Guest passwords are generated when not supplied and shown once.
- The share and job directories are guest-writable; Kryton never follows symlinks in them (see above).
- Kryton does not ship Microsoft ISOs; dockur downloads media when you opt into this provider.

---

## What we deliberately did not copy

- FreeRDP RemoteApp / Linux `.desktop` integration (WinPodX product surface)
- Reverse-open of Linux apps inside Windows
- Shipping Microsoft ISOs from Kryton itself

### Why the agent writes a scratch file before every poll

Job directories are created by Kryton on the host, outside Samba. dockur's Samba (4.22, `smb3 directory leases = Auto`) therefore never tells the guest's SMB client that `.kryton/jobs` changed, and the client keeps serving a cached listing: new jobs stayed `queued` until the guest rebooted (measured: minutes to indefinitely; the heartbeat kept beating, so `ready` looked healthy). Plain re-listing, `cmd dir`, a fresh PSDrive and `net use /delete` did not help (the last also hangs while the job's own log file holds the connection). Creating and deleting a scratch file (`jobs\.poll`) through the share invalidates the client's cache for that directory, and the next listing is current. Since 1.4.3 the agent does this on every 2 s poll; fresh jobs now start within a few seconds.

The agent is installed at machine creation. A machine created before 1.4.3 keeps the old agent until it is reinstalled: copy the new `kryton-agent.ps1` over `%ProgramData%\Kryton\agent.ps1` from a job (it works right after a reboot, when the listing is still fresh) and reboot once.
