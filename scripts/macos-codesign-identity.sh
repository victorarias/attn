#!/usr/bin/env bash
set -euo pipefail

LOCAL_IDENTITY_NAME="${ATTN_LOCAL_CODESIGN_IDENTITY:-attn Local Development Code Signing}"
LOCAL_IDENTITY_PKCS12_PASSWORD="${ATTN_LOCAL_CODESIGN_PKCS12_PASSWORD:-attn-local-dev}"
COMMAND="${1:-find}"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "-"
  exit 0
fi

find_identity_matching() {
  local pattern="$1"
  security find-identity -v -p codesigning 2>/dev/null \
    | awk -v pat="${pattern}" '$0 ~ pat { print $2 }' \
    | LC_ALL=C sort \
    | head -1
}

find_identity() {
  local identity pattern
  # Prefer real Apple-issued identities. Their designated requirement is
  # Apple-anchored and Team-based, so macOS privacy grants (Screen Recording,
  # Accessibility, etc.) persist across rebuilds. Fall back to the stable
  # self-signed local identity, then ad-hoc. Developer ID is preferred over
  # Apple Development because it is not tied to a provisioning profile/device.
  for pattern in \
    '"Developer ID Application:' \
    '"Apple Development:' \
    "\"${LOCAL_IDENTITY_NAME}\""; do
    identity="$(find_identity_matching "${pattern}")"
    if [[ -n "${identity}" ]]; then
      echo "${identity}"
      return 0
    fi
  done

  echo "-"
}

create_local_identity() {
  if [[ "$(find_identity)" != "-" ]]; then
    find_identity
    return 0
  fi

  if ! command -v openssl >/dev/null 2>&1; then
    echo "openssl is required to create ${LOCAL_IDENTITY_NAME}" >&2
    exit 1
  fi

  local keychain
  keychain="$(security default-keychain -d user | tr -d '"' | xargs)"
  if [[ -z "${keychain}" ]]; then
    echo "Could not resolve default user keychain" >&2
    exit 1
  fi

  local tmpdir
  tmpdir="$(mktemp -d)"
  trap "rm -rf '${tmpdir}'" EXIT

  cat >"${tmpdir}/codesign.cnf" <<EOF
[req]
distinguished_name = subject
x509_extensions = extensions
prompt = no

[subject]
CN = ${LOCAL_IDENTITY_NAME}

[extensions]
basicConstraints = critical,CA:true
keyUsage = critical,digitalSignature,keyCertSign
extendedKeyUsage = critical,codeSigning
subjectKeyIdentifier = hash
EOF

  openssl req \
    -newkey rsa:2048 \
    -nodes \
    -keyout "${tmpdir}/identity.key" \
    -x509 \
    -days 3650 \
    -out "${tmpdir}/identity.crt" \
    -config "${tmpdir}/codesign.cnf" \
    >/dev/null 2>&1

  openssl pkcs12 \
    -export \
    -legacy \
    -out "${tmpdir}/identity.p12" \
    -inkey "${tmpdir}/identity.key" \
    -in "${tmpdir}/identity.crt" \
    -name "${LOCAL_IDENTITY_NAME}" \
    -passout "pass:${LOCAL_IDENTITY_PKCS12_PASSWORD}" \
    >/dev/null 2>&1

  security import "${tmpdir}/identity.p12" \
    -k "${keychain}" \
    -P "${LOCAL_IDENTITY_PKCS12_PASSWORD}" \
    -T /usr/bin/codesign \
    >/dev/null

  security add-trusted-cert \
    -r trustRoot \
    -p codeSign \
    -k "${keychain}" \
    "${tmpdir}/identity.crt" \
    >/dev/null

  local created
  created="$(find_identity)"
  if [[ "${created}" == "-" ]]; then
    echo "Created ${LOCAL_IDENTITY_NAME}, but macOS does not report it as a valid code-signing identity" >&2
    exit 1
  fi
  echo "${created}"
}

case "${COMMAND}" in
  find)
    find_identity
    ;;
  ensure)
    create_local_identity
    ;;
  *)
    echo "Usage: $0 [find|ensure]" >&2
    exit 2
    ;;
esac
