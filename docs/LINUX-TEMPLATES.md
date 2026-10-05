# Linux images on libvirt and KubeVirt

Kryton now deploys Linux through the existing machine API. The catalog
contains eleven amd64 cloud images: Ubuntu 22.04/24.04/26.04, Debian 12/13, Rocky 9/10, AlmaLinux 9/10, CentOS Stream 10 and Fedora 44. Existing Windows/dockur functionality remains available.

| Image ID | Default user | Upstream checksum |
|---|---|---|
| `ubuntu-26.04`, `ubuntu-24.04`, `ubuntu-22.04` | `ubuntu` | `SHA256SUMS`, GPG-signed |
| `debian-13`, `debian-12` | `debian` | `SHA512SUMS` over HTTPS (derive the SHA-256) |
| `rocky-10`, `rocky-9` | `rocky` | `CHECKSUM`, GPG-signed |
| `almalinux-10`, `almalinux-9` | `almalinux` | `CHECKSUM`, GPG-signed |
| `centos-stream-10` | `cloud-user` | `SHA256SUM` over HTTPS |
| `fedora-44` | `fedora` | Clearsigned `CHECKSUM` |

`initialization.username` overrides the default user. All eleven pass the real
boot gate on libvirt; see [LINUX-TEST-RESULTS.md](LINUX-TEST-RESULTS.md).

The Go implementation needs no Bento, Packer, Vagrant or VirtualBox. Native
libvirt provisioning uses the host's `virsh`, `qemu-img` and `genisoimage`;
KubeVirt uses its existing Kubernetes REST client and CDI.

## Acquire an image

`make build` builds `krytond`, `krytonctl` and `kryton-image`.

```bash
bin/kryton-image list
bin/kryton-image validate
bin/kryton-image fetch --image ubuntu-24.04 \
  --dir /var/lib/kryton/images --sha256 "$APPROVED_SHA256"
```

Resolve the expected SHA-256 from the distribution's signed checksum manifest
and verify its signing key before supplying it. Kryton requires an explicit
approved digest; it does not automatically trust a checksum fetched beside the
image. `--url` can select an immutable upstream version or an internal HTTPS
mirror. Moving `current`/`latest` URLs are discovery defaults, not reproducible
release versions. Downloads are limited to 32 GiB and 30 minutes, honor context
cancellation, and refuse HTTP/credential-bearing URLs and downgrade redirects.

Artifacts are named `<sha256>.qcow2`; an atomic `<image-id>.json` manifest records
source, digest, byte count and acquisition time. Publication requires a matching
hash. Existing artifact collisions and provisioning recheck the digest. A base
must be standalone QCOW2 with no external backing chain. Download verification
is not guest boot certification: manifests explicitly keep `bootValidated=false`.

Distribution verification references:

- Ubuntu: https://cloud-images.ubuntu.com/noble/current/SHA256SUMS
- Debian: https://cloud.debian.org/images/cloud/trixie/latest/SHA512SUMS
  (after verifying the published signature and bytes, calculate an approved SHA-256)
- Rocky: https://download.rockylinux.org/pub/rocky/10/images/x86_64/ (signed `CHECKSUM`)
- AlmaLinux: https://wiki.almalinux.org/cloud/Generic-cloud.html
- CentOS Stream: https://cloud.centos.org/centos/10-stream/x86_64/images/ (`SHA256SUM` over HTTPS)
- Fedora: https://fedoraproject.org/security/ (clearsigned `CHECKSUM` per release)

Rocky 10, AlmaLinux 10 and CentOS Stream 10 are built for x86-64-v3, so the
guest CPU must expose AVX2. Kryton's libvirt domains use `host-model` and
KubeVirt passes the host model by default, so this only matters on old hosts or
with a restrictive CPU model. The Fedora URL names a specific compose
(`44-1.7`) because Fedora has no `latest` alias; update it with each release.

## Native libvirt host

Install KVM/libvirt, `virsh`, `qemu-img`, `genisoimage`; enable a libvirt network
with DHCP (default: `default`). Authenticate to local `qemu:///system` or
`qemu:///session`. Remote libvirt URIs are rejected because disk paths are local.
The daemon account must be authorized for libvirt and have write access to its
managed directories. The QEMU account must be able to traverse them and access
disks/seeds; use a shared group and setgid directories as appropriate for the
host. On SELinux/AppArmor hosts, configure labels/profiles for this storage path.
Do not make machine metadata or seed files world-readable.

Example on a Debian/Ubuntu host (adapt group/account names):

```bash
sudo install -d -m 2770 -o kryton -g libvirt-qemu /var/lib/kryton/machines
sudo install -d -m 0755 -o kryton /var/lib/kryton/images
export KRYTON_PROVIDER=libvirt
export KRYTON_LIBVIRT_URI=qemu:///system
export KRYTON_LIBVIRT_DATA_DIR=/var/lib/kryton/machines
export KRYTON_LINUX_IMAGE_DIR=/var/lib/kryton/images
export KRYTON_LIBVIRT_NETWORK=default
export KRYTON_PROJECTS=default,linux-tests
export KRYTON_AUTH_MODE=apikey
export KRYTON_API_KEYS_FILE=/etc/kryton/api-keys.json
bin/krytond
```

Provide API keys using the existing authentication guide. Authentication-disabled
libvirt is rejected unless the explicit existing insecure override is enabled.
This backend is for native host deployment; the current distroless container
has no libvirt CLI or disk tools and is intended for demo/KubeVirt.

```bash
KRYTON_TOKEN="$TOKEN" bin/krytonctl create --image ubuntu-24.04 \
  --cpu 2 --memory 2048 --disk 20 --ssh-key ~/.ssh/id_ed25519.pub linux-dev
```

Each VM gets a separate converted/resized disk and a unique NoCloud seed;
base images are immutable. Stable UUID records persist across daemon restarts.
Stop requests graceful shutdown; Delete forcibly powers off if required,
undefines the managed domain, then removes only its managed storage directory.
Disk data is retained after failed domain start so operators can repair host
permissions and retry. Unknown domain state blocks deletion.

Snapshots, graphical console, live migration, Windows on native libvirt and
multiple writer replicas are not implemented/advertised. Serial console is
available directly through `virsh console kryton-<machine-uuid>`.

## KubeVirt persistent images

Host the acquired digest-named artifact on an operator-controlled HTTPS server.
Export a CDI DataVolume and DataSource, substituting the real published URL:

```bash
bin/kryton-image cdi --image ubuntu-24.04 --namespace kryton-images \
  --storage-class YOUR_CLASS \
  --url "https://artifacts.your-domain/$APPROVED_SHA256.qcow2" > /tmp/linux-cdi.json
kubectl create namespace kryton-images  # omit if it already exists
kubectl apply -f /tmp/linux-cdi.json
kubectl -n kryton-images wait --for=condition=Ready \
  datasource/ubuntu-24.04 --timeout=15m
```

CDI imports the artifact to a persistent PVC; machines clone it through the
existing DataSource flow. Protect the artifact server against replacement: CDI
HTTP import itself does not verify Kryton's approved SHA-256. Replacing an
existing imported DataVolume needs an explicit operator rollout; applying a
changed URL to an existing DataVolume does not rebuild its disk. Promote new
image releases using new catalog IDs/DataSources, and retain old versions for
rollback.

Linux profiles use BIOS by default, VirtIO storage/networking and NoCloud;
Windows retains EFI, TPM and Hyper-V features. Operator catalogs can request
EFI. ARM64 is deliberately rejected until both targets have validated profiles.

## Initialization and UI

Create accepts `initialization.username` and `initialization.sshAuthorizedKeys`.
Linux guests use locked passwords, disabled SSH password authentication,
root login disabled, disk growth, and installation/start of `qemu-guest-agent`.

Cloud-init installs `qemu-guest-agent` from the distribution's package
repositories, so guests need DNS and outbound HTTP(S) on first boot. Without
them cloud-init never finishes, no guest agent reports an IP, and the machine
shows no address. On KubeVirt with Cilium's kube-proxy replacement, VM
masquerade traffic cannot reach the cluster DNS ClusterIP unless Cilium sets
`bpf-lb-sock-hostns-only: true`; see [KUBEVIRT.md](KUBEVIRT.md#troubleshooting).
The configured user receives passwordless sudo. Guest package installation
requires distribution repository access (or a preinstalled agent).
There is no raw user-data/script API or credential injection into annotations.

The UI labels Linux images correctly, filters unsupported providers, accepts
public SSH keys, and offers Copy SSH when an IP is reported. The Windows golden
installer rejects Linux images; acquire them with `kryton-image fetch`.

## Test evidence and the real boot gate

Automated tests cover all eleven Linux profiles and all 12 Windows profiles through
KubeVirt REST creation, all eleven CDI exports and libvirt XMLs, libvirt lifecycle /
restart recovery / project isolation / failure retention, checksums, tampering,
size limits and invalid initialization. Disk-tool integration runs real
QCOW2 conversion/resize and extracts the NoCloud ISO. CI installs disk/schema
tools so these integration tests execute rather than skip.

Generated examples in `examples/linux/<image-id>/` include libvirt XML,
cloud-config and CDI JSON. Example artifact URLs and disk paths are placeholders;
replace them before deployment. These files are configuration examples, not
prebuilt operating-system images.

On each target, run the real guest gate after acquiring/importing the images:

```bash
KRYTON_URL=https://YOUR_KRYTON KRYTON_TOKEN="$TOKEN" \
  KRYTON_PROJECT=linux-tests SSH_KEY="$HOME/.ssh/linux-test" \
  ./scripts/e2e-linux-templates.sh
```

The project must already be configured and authorized. KubeVirt requires
`virtctl`, cluster credentials and `KRYTON_NAMESPACE` when namespace prefixes
are used. Libvirt requires network reachability to guest addresses. The script
creates disposable VMs, checks Running, SSH, cloud-init completion, guest agent,
expanded disk/root filesystem and expected distribution/version, and cleans up
its own machines. It sets a 30-minute TTL as a fallback and uses isolated SSH
known-hosts files. It does not certify images merely from a Running state.

All eleven images pass this gate on a real libvirt/KVM host. On KubeVirt the
guests boot, but the gate has not passed yet because the lab cluster's Cilium
setup blocks VM DNS; see [LINUX-TEST-RESULTS.md](LINUX-TEST-RESULTS.md).
Windows boot regressions remain host acceptance checks.
