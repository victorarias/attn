#!/usr/bin/env bash
#
# Build the packaged attn .app for a single profile, deriving ALL bundle
# metadata (productName, bundle id, ws port, deep-link scheme) from the single
# authority: `attn profile resolve`. The default/prod profile builds from the
# committed tauri.conf.json; every NAMED profile gets a generated, gitignored
# `tauri.<appName>.gen.conf.json` overlay (from `attn profile tauri-config`) so
# no per-profile bundle metadata is ever hand-maintained.
#
# Inputs (env):
#   PROFILE                  profile name ("" = default/prod)
#   ATTN_BIN                 path to the freshly built attn binary (the authority + sidecar)
#   VERSION SOURCE_FINGERPRINT GIT_COMMIT BUILD_TIME   build-identity stamp
#   MACOS_CODESIGN_IDENTITY  optional; else discovered, else ad-hoc ("-")
#
# Not using `set -u`: macOS ships bash 3.2 where empty-array/var expansion under
# `set -u` is fragile. Required vars are validated explicitly.
set -eo pipefail

profile="${PROFILE:-}"
attn="${ATTN_BIN:?ATTN_BIN (path to built attn binary) is required}"
: "${VERSION:?VERSION is required}"
: "${SOURCE_FINGERPRINT:?SOURCE_FINGERPRINT is required}"
: "${GIT_COMMIT:?GIT_COMMIT is required}"
: "${BUILD_TIME:?BUILD_TIME is required}"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

# Resolve every resource from the one authority.
app_name="$("$attn" profile resolve --profile "$profile" --field appName)"
bundle_id="$("$attn" profile resolve --profile "$profile" --field bundleId)"
ws_port="$("$attn" profile resolve --profile "$profile" --field wsPort)"
label="$("$attn" profile resolve --profile "$profile" --field label)"

echo ">>> Building $label app: $app_name.app (id=$bundle_id, port=$ws_port)"

# Stage the Go daemon as the Tauri sidecar.
mkdir -p app/src-tauri/binaries
cp "$attn" "app/src-tauri/binaries/attn-aarch64-apple-darwin"

# Compile first-party plugins before Tauri collects resources. Bundled means
# available in the catalog; the daemon still requires a per-profile opt-in.
bash ./scripts/build-bundled-plugins.sh

cd app
pnpm install

if [ -n "$profile" ]; then
  # Named profile: generate the bundle-metadata overlay from the authority and
  # bake the resolved port + bundle id so the Rust runtime view can never drift.
  gen_rel="src-tauri/${app_name}.gen.conf.json"
  "$attn" profile tauri-config --profile "$profile" > "$gen_rel"
  echo ">>> Generated Tauri overlay $gen_rel"
  ATTN_BUILD_PROFILE="$profile" \
  VITE_ATTN_BUILD_PROFILE="$profile" \
  ATTN_BUILD_WS_PORT="$ws_port" \
  ATTN_BUILD_BUNDLE_ID="$bundle_id" \
  VITE_DAEMON_PORT="$ws_port" \
  VITE_INSTALL_CHANNEL=source \
  VITE_ATTN_BUILD_VERSION="$VERSION" \
  VITE_ATTN_SOURCE_FINGERPRINT="$SOURCE_FINGERPRINT" \
  VITE_ATTN_GIT_COMMIT="$GIT_COMMIT" \
  VITE_ATTN_BUILD_TIME="$BUILD_TIME" \
  pnpm tauri build --bundles app --config "$gen_rel"
else
  # Default/prod build: committed tauri.conf.json, no baked profile env. This is
  # byte-for-byte the historical `make build-app` command.
  VITE_INSTALL_CHANNEL=source \
  VITE_ATTN_BUILD_VERSION="$VERSION" \
  VITE_ATTN_SOURCE_FINGERPRINT="$SOURCE_FINGERPRINT" \
  VITE_ATTN_GIT_COMMIT="$GIT_COMMIT" \
  VITE_ATTN_BUILD_TIME="$BUILD_TIME" \
  pnpm tauri build --bundles app
fi
cd "$repo_root"

bundle_dir="app/src-tauri/target/release/bundle/macos/${app_name}.app"

if [ "$(uname -s)" = "Darwin" ]; then
  mkdir -p "${bundle_dir}/Contents/Resources"
  printf '{\n  "version": "%s",\n  "sourceFingerprint": "%s",\n  "gitCommit": "%s",\n  "buildTime": "%s"\n}\n' \
    "$VERSION" "$SOURCE_FINGERPRINT" "$GIT_COMMIT" "$BUILD_TIME" \
    > "${bundle_dir}/Contents/Resources/build-identity.json"

  # Sign the sidecar first, then the enclosing app bundle, so macOS privacy
  # grants attach to a stable signed identity across source rebuilds. One
  # machine-wide identity signs every profile's bundle (grants are keyed by
  # bundle id, so each profile grants once on first launch).
  identity="${MACOS_CODESIGN_IDENTITY:-}"
  if [ -z "$identity" ]; then identity="$(bash ./scripts/macos-codesign-identity.sh find)"; fi
  if [ -z "$identity" ]; then identity="-"; fi
  while IFS= read -r executable; do
    codesign --force --sign "$identity" "$executable"
  done < <(find "${bundle_dir}/Contents/Resources/plugins" -type f -perm -111 2>/dev/null | sort)
  codesign --force --sign "$identity" "${bundle_dir}/Contents/MacOS/attn"
  codesign --force --sign "$identity" "${bundle_dir}"
fi

echo ">>> Built $bundle_dir"
