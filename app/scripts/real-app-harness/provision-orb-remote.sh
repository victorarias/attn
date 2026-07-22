#!/usr/bin/env bash
# Provision (or re-provision) the local OrbStack VM that remote-endpoint
# harness scenarios target as attn-remote@orb. Idempotent; safe to re-run.
# One manual step remains after first provisioning: `claude /login` inside
# the VM (macOS keeps Claude credentials in the Keychain, so they cannot be
# copied in).
set -euo pipefail

VM_NAME="${ATTN_ORB_REMOTE_NAME:-attn-remote}"
SSH_TARGET="${VM_NAME}@orb"

echo "==> Checking for orbctl"
if ! command -v orbctl >/dev/null 2>&1; then
  echo "OrbStack is required: https://orbstack.dev" >&2
  exit 1
fi

echo "==> Checking for existing VM '${VM_NAME}'"
vm_list_output="$(orbctl list -f json 2>/dev/null || orbctl list 2>/dev/null || true)"
if ! grep -q "${VM_NAME}" <<<"${vm_list_output}"; then
  echo "==> Creating VM '${VM_NAME}' (ubuntu)"
  orbctl create ubuntu "${VM_NAME}"
else
  echo "==> VM '${VM_NAME}' already exists"
fi

echo "==> Waiting for SSH on ${SSH_TARGET}"
ssh_ready=0
for _ in $(seq 1 30); do
  if ssh -o BatchMode=yes -o ConnectTimeout=5 "${SSH_TARGET}" true 2>/dev/null; then
    ssh_ready=1
    break
  fi
  sleep 2
done
if [[ "${ssh_ready}" -ne 1 ]]; then
  echo "SSH to ${SSH_TARGET} never became ready; check 'orbctl logs ${VM_NAME}'" >&2
  exit 1
fi
echo "==> SSH is ready on ${SSH_TARGET}"

echo "==> Installing base packages (git, nodejs, npm)"
ssh -o BatchMode=yes "${SSH_TARGET}" 'sudo apt-get update -qq && sudo apt-get install -y -qq git nodejs npm'

echo "==> Installing codex and claude CLIs"
ssh -o BatchMode=yes "${SSH_TARGET}" 'sudo npm install -g @openai/codex @anthropic-ai/claude-code'

echo "==> Verifying required tools are on PATH in the VM"
missing_tools="$(ssh -o BatchMode=yes "${SSH_TARGET}" '
  missing=""
  for tool in git python3 ss codex claude; do
    command -v "$tool" >/dev/null 2>&1 || missing="$missing $tool"
  done
  echo "$missing"
')"
if [[ -n "${missing_tools// /}" ]]; then
  echo "Missing required tools in VM:${missing_tools}" >&2
  exit 1
fi
echo "==> All required tools present: git python3 ss codex claude"

echo "==> Checking codex auth"
codex_auth_present=0
if ssh -o BatchMode=yes "${SSH_TARGET}" 'test -f ~/.codex/auth.json' 2>/dev/null; then
  codex_auth_present=1
  echo "==> Codex auth already present in VM"
elif [[ -f "${HOME}/.codex/auth.json" ]]; then
  echo "==> Copying host codex auth into VM"
  ssh -o BatchMode=yes "${SSH_TARGET}" 'mkdir -p ~/.codex'
  ssh -o BatchMode=yes "${SSH_TARGET}" 'cat > ~/.codex/auth.json' < "${HOME}/.codex/auth.json"
  ssh -o BatchMode=yes "${SSH_TARGET}" 'chmod 600 ~/.codex/auth.json'
  codex_auth_present=1
else
  echo "WARNING: no host ~/.codex/auth.json found; log codex in inside the VM (ssh -t ${SSH_TARGET} codex login)" >&2
fi

echo "==> Checking claude auth"
claude_auth_present=0
if ssh -o BatchMode=yes "${SSH_TARGET}" 'test -f ~/.claude/.credentials.json' 2>/dev/null; then
  claude_auth_present=1
  echo "==> Claude auth already present in VM"
else
  echo "Claude needs a one-time interactive login inside the VM: ssh -t ${SSH_TARGET} claude /login"
fi

echo "==> Provisioning summary"
echo "    VM name:     ${VM_NAME}"
echo "    SSH target:  ${SSH_TARGET}"
if [[ "${codex_auth_present}" -eq 1 ]]; then
  echo "    codex auth:  present"
else
  echo "    codex auth:  MISSING"
fi
if [[ "${claude_auth_present}" -eq 1 ]]; then
  echo "    claude auth: present"
else
  echo "    claude auth: MISSING (run: ssh -t ${SSH_TARGET} claude /login)"
fi
