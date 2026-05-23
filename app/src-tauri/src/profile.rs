//! Build-profile awareness for the Tauri shell.
//!
//! The `ATTN_BUILD_PROFILE` env var is read at *compile* time and baked
//! into the binary. At startup a profile-baked app makes that profile
//! authoritative for daemon routing: it sets `ATTN_PROFILE` and
//! `ATTN_WS_PORT`, and removes socket/database/config paths inherited from
//! a parent attn terminal. That prevents the dev bundle from reaching the
//! production daemon while being launched from a production session.
//!
//! Keep the dev port in sync with `internal/config/config.go::WSPort`.

use std::env;

const BUILD_PROFILE: Option<&str> = option_env!("ATTN_BUILD_PROFILE");

/// Returns the compile-time profile name (empty string for default).
pub fn build_profile() -> &'static str {
    BUILD_PROFILE.unwrap_or("").trim()
}

/// Returns a human-readable profile label ("default" when empty).
pub fn build_profile_label() -> &'static str {
    let p = build_profile();
    if p.is_empty() {
        "default"
    } else {
        p
    }
}

/// Returns the default WS port for the compile-time profile.
/// Mirrors `config.WSPort()` in Go.
pub fn default_port_for_build_profile() -> &'static str {
    match build_profile() {
        "" => "9849",
        "dev" => "29849",
        // For any other named build, we require ATTN_WS_PORT to be set
        // explicitly at build time. Fall back to the dev port so we
        // never silently collide with prod.
        _ => "29849",
    }
}

/// Applies the build-time profile to the process env, so spawned daemon
/// subprocesses inherit it and any subsequent env lookups in the shell
/// itself (e.g. `daemon_http_port`) see the expected isolated endpoint.
///
/// Must be called before any function that reads `ATTN_PROFILE` or
/// `ATTN_WS_PORT` (directly or via a spawned subprocess).
pub fn apply_build_profile_env() {
    let profile = build_profile();
    if !profile.is_empty() {
        for key in ["ATTN_SOCKET_PATH", "ATTN_DB_PATH", "ATTN_CONFIG_PATH"] {
            env::remove_var(key);
        }
        env::set_var("ATTN_PROFILE", profile);
        env::set_var("ATTN_WS_PORT", default_port_for_build_profile());
    }
}

/// macOS bundle identifier for the running build. Must stay in sync with
/// the `identifier` field in `tauri.conf.json` (default build) and
/// `tauri.dev.conf.json` (dev build overlay).
pub fn bundle_identifier() -> &'static str {
    match build_profile() {
        "dev" => "com.attn.manager.dev",
        _ => "com.attn.manager",
    }
}

/// Decide whether the UI automation bridge should run this launch. Single
/// rule shared with the native canvas app — keep it in sync with
/// `native-ui/crates/attn-native-app/src/automation/profile.rs` and
/// `app/scripts/real-app-harness/nativeHarnessProfile.mjs`.
///
///   - `ATTN_AUTOMATION=1`  → on
///   - `ATTN_AUTOMATION=0`  → off
///   - `ATTN_PROFILE=dev`   → on   (default for dev)
///   - otherwise            → off  (default for prod / unset)
///
/// `apply_build_profile_env` must run first so a dev build always sees the
/// right profile name when this is consulted.
pub fn automation_enabled() -> bool {
    let automation = env::var("ATTN_AUTOMATION").ok();
    let profile = env::var("ATTN_PROFILE").ok();
    decide_automation_enabled(automation.as_deref(), profile.as_deref())
}

fn decide_automation_enabled(automation: Option<&str>, profile: Option<&str>) -> bool {
    match automation.map(str::trim) {
        Some("1") => return true,
        Some("0") => return false,
        Some("") | None => {}
        // Strict-off on any other value so typos don't silently disable
        // automation in CI.
        Some(_) => return false,
    }
    profile
        .map(str::trim)
        .map(|p| p.to_ascii_lowercase())
        .as_deref()
        == Some("dev")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn automation_decision_rules() {
        assert!(decide_automation_enabled(Some("1"), None));
        assert!(decide_automation_enabled(Some("1"), Some("dev")));
        assert!(!decide_automation_enabled(Some("0"), None));
        assert!(!decide_automation_enabled(Some("0"), Some("dev")));
        assert!(!decide_automation_enabled(Some("yes"), Some("dev")));
        assert!(decide_automation_enabled(None, Some("dev")));
        assert!(!decide_automation_enabled(None, Some("ci")));
        assert!(!decide_automation_enabled(None, None));
        assert!(decide_automation_enabled(Some(""), Some("dev")));
        assert!(decide_automation_enabled(Some("  "), Some("dev")));
        assert!(!decide_automation_enabled(Some("  "), None));
        // Profile case-insensitive (Go side accepts only lowercase but
        // mixed case here would mean the parent env wasn't normalized;
        // handle defensively).
        assert!(decide_automation_enabled(None, Some("DEV")));
    }
}
