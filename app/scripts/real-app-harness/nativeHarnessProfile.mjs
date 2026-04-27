import os from 'node:os';
import path from 'node:path';

/**
 * Native-app sibling of harnessProfile.mjs. The native GPUI app and the
 * Tauri app are evolving on different timelines and will diverge — this
 * file owns the native side so the Tauri helper isn't paramaterized by
 * an `app` argument that hides their growing differences.
 *
 * Profile + automation gating must stay in sync with the Rust copy at
 *   - native-ui/crates/attn-native-app/src/automation/profile.rs
 * and the Tauri runtime gate at
 *   - app/src-tauri/src/profile.rs
 * and the Go regex at
 *   - internal/config/config.go (profileNamePattern)
 */

const BASE_BUNDLE_ID = 'com.attn.native';
const PROFILE_REGEX = /^[a-z0-9][a-z0-9-]{0,15}$/;

export function currentNativeProfile() {
  const raw = (process.env.ATTN_PROFILE || '').trim().toLowerCase();
  if (!raw || raw === 'default') return '';
  return PROFILE_REGEX.test(raw) ? raw : '';
}

export function bundleIdentifierForNativeProfile(profile = currentNativeProfile()) {
  return profile ? `${BASE_BUNDLE_ID}.${profile}` : BASE_BUNDLE_ID;
}

export function manifestPathForNativeProfile(profile = currentNativeProfile()) {
  return path.join(
    os.homedir(),
    'Library',
    'Application Support',
    bundleIdentifierForNativeProfile(profile),
    'debug',
    'ui-automation.json',
  );
}

/**
 * Mirror of the Rust `automation_enabled()` rule. Used by harness scripts
 * that need to predict whether the app under test will have automation on
 * (e.g. before launching, to fail fast with a clear message).
 *
 * Rule:
 *   ATTN_AUTOMATION=1 → on
 *   ATTN_AUTOMATION=0 → off
 *   ATTN_PROFILE=dev  → on  (default for dev)
 *   else              → off
 */
export function automationEnabledForNativeProfile() {
  const automation = (process.env.ATTN_AUTOMATION || '').trim();
  if (automation === '1') return true;
  if (automation === '0') return false;
  // Any other non-empty value → strict off (typo-safety, mirrors Rust).
  if (automation !== '') return false;
  return currentNativeProfile() === 'dev';
}
