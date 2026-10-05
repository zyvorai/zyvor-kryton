#!/usr/bin/env bash
# Copyright 2026 Zyvor AI Labs · https://zyvor.dev
# SPDX-License-Identifier: Apache-2.0
# Real guest boot gate. Requires acquired libvirt images or ready CDI DataSources.
set -euo pipefail
: "${KRYTON_TOKEN:?Set KRYTON_TOKEN for the test project}"
: "${SSH_KEY:?Set SSH_KEY to a test private key (matching .pub required)}"
KRYTON_URL="${KRYTON_URL:-http://127.0.0.1:8080}"
KRYTON_PROJECT="${KRYTON_PROJECT:-linux-tests}"
KRYTON_NAMESPACE="${KRYTON_NAMESPACE:-$KRYTON_PROJECT}"
read -r -a images <<< "${KRYTON_E2E_IMAGES:-ubuntu-22.04 ubuntu-24.04 debian-12 debian-13 rocky-9 almalinux-9}"
for tool in curl python3 ssh timeout; do command -v "$tool" >/dev/null; done
scratch="$(mktemp -d)"
ids=()
api() { curl --fail --silent --show-error -H "Authorization: Bearer $KRYTON_TOKEN" -H 'Content-Type: application/json' "$@"; }
cleanup() {
  for id in "${ids[@]}"; do api -X DELETE "$KRYTON_URL/api/v1/machines/$id?project=$KRYTON_PROJECT" >/dev/null || true; done
  rm -rf "$scratch"
}
trap cleanup EXIT
provider="$(api "$KRYTON_URL/api/v1/capabilities" | python3 -c 'import json,sys; print(json.load(sys.stdin)["provider"])')"
[[ "$provider" == libvirt || "$provider" == kubevirt ]] || { echo 'Real libvirt or KubeVirt provider required'; exit 1; }
if [[ "$provider" == kubevirt ]]; then command -v virtctl >/dev/null; fi
api "$KRYTON_URL/api/v1/images" > "$scratch/images.json"
for image in "${images[@]}"; do
  name="linux-test-${image//./-}-$(date +%s)"
  python3 - "$scratch/images.json" "$image" "$name" "$KRYTON_PROJECT" "$SSH_KEY.pub" > "$scratch/request.json" <<'PY'
import json,sys
items=json.load(open(sys.argv[1]))['items']
i=next(v for v in items if v['id']==sys.argv[2])
if not i['ready']: raise SystemExit('Image is not ready: '+i['id'])
json.dump({'project':sys.argv[4],'name':sys.argv[3],'image':i['id'],'compute':{'cpu':2,'memoryMiB':2048},'disk':{'sizeGiB':20},'ttlMinutes':30,'initialization':{'username':'krytontest','sshAuthorizedKeys':[open(sys.argv[5]).read().strip()]}},sys.stdout)
PY
  machine="$(api -X POST --data-binary "@$scratch/request.json" "$KRYTON_URL/api/v1/machines")"
  id="$(printf '%s' "$machine" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')"
  ids+=("$id")
  ready=0
  for ((attempt=0;attempt<120;attempt++)); do
    api "$KRYTON_URL/api/v1/machines/$id?project=$KRYTON_PROJECT" > "$scratch/machine.json"
    state="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["state"])' "$scratch/machine.json")"
    if [[ "$state" == running ]]; then ready=1; break; fi
    [[ "$state" != failed ]] || { cat "$scratch/machine.json"; exit 1; }
    sleep 5
  done
  [[ "$ready" == 1 ]] || { echo "$image: boot timeout"; exit 1; }
  sshargs=(-i "$SSH_KEY" -o BatchMode=yes -o ConnectTimeout=5 -o StrictHostKeyChecking=accept-new -o "UserKnownHostsFile=$scratch/$image-known-hosts")
  # Filesystem-mode PVCs lose a few percent to filesystem overhead.
  min_disk=21474836480
  if [[ "$provider" == kubevirt ]]; then
    # A long-lived port-forward stops accepting connections after the first
    # failed dial (guest not up yet), so tunnel each SSH attempt separately.
    host="$name"
    sshargs+=(-o "ProxyCommand=virtctl port-forward --stdio=true -n $KRYTON_NAMESPACE vm/$name 22")
    min_disk=20401094656
  else
    host="$(python3 -c 'import json,sys; print(next(iter(json.load(open(sys.argv[1])).get("ipAddresses",[])),""))' "$scratch/machine.json")"
    for ((attempt=0;attempt<60 && ${#host}==0;attempt++)); do
      sleep 5
      host="$(api "$KRYTON_URL/api/v1/machines/$id?project=$KRYTON_PROJECT" | python3 -c 'import json,sys; print(next(iter(json.load(sys.stdin).get("ipAddresses",[])),""))')"
    done
    [[ -n "$host" ]] || { echo "$image: no guest-agent IP"; exit 1; }
  fi
  connected=0
  for ((attempt=0;attempt<120;attempt++)); do
    if ssh "${sshargs[@]}" "krytontest@$host" true 2>/dev/null; then connected=1;break;fi
    sleep 5
  done
  [[ "$connected" == 1 ]] || { echo "$image: SSH timeout"; exit 1; }
  timeout 600 ssh "${sshargs[@]}" "krytontest@$host" 'set -e; sudo cloud-init status --wait; sudo systemctl is-active qemu-guest-agent; test "$(lsblk -bdn -o SIZE /dev/vda)" -ge '"$min_disk"'; test "$(df -B1 --output=size / | tail -n1)" -ge 16106127360; cat /etc/os-release' > "$scratch/os-release"
  expected_os="${image%-*}";expected_version="${image##*-}"
  grep -q "^ID=\"\?$expected_os\"\?$" "$scratch/os-release"
  # Rocky and AlmaLinux report a point release ("9.6") for the "9" template.
  grep -Eq "^VERSION_ID=\"?${expected_version//./\\.}(\.[0-9]+)*\"?$" "$scratch/os-release"
  printf '%s: boot, SSH, cloud-init, guest agent, disk expansion passed\n' "$image"
  api -X DELETE "$KRYTON_URL/api/v1/machines/$id?project=$KRYTON_PROJECT" >/dev/null
  unset 'ids[${#ids[@]}-1]'
 done
