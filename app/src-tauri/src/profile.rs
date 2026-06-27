//! Build-profile awareness for the Tauri shell.
//!
//! The `ATTN_BUILD_PROFILE` env var is read at *compile* time and baked
//! into the binary. At startup a profile-baked app makes that profile
//! authoritative for daemon routing: it sets `ATTN_PROFILE` and
//! `ATTN_WS_PORT`, and removes socket/database/config paths inherited from
//! a parent attn terminal. That prevents the dev bundle from reaching the
//! production daemon while being launched from a production session.
//!
//! A named per-profile build additionally bakes `ATTN_BUILD_WS_PORT` and
//! `ATTN_BUILD_BUNDLE_ID`, both resolved by the single authority
//! (`attn profile resolve`) in the Makefile, so this Rust view never re-derives
//! (and never drifts from) the Go side. Default/dev builds leave them unset and
//! use the well-known fallbacks below.

use std::env;
use std::fs::{self, OpenOptions};
use std::io::Write;
use std::os::unix::fs::{OpenOptionsExt, PermissionsExt};
use std::path::PathBuf;

const BUILD_PROFILE: Option<&str> = option_env!("ATTN_BUILD_PROFILE");

// Resources resolved by the single authority (`attn profile resolve`) and baked
// in by the Makefile for a per-profile build. Cargo tracks env vars referenced
// by `option_env!` in its dep-info, so changing the profile recompiles this
// crate. Default/prod builds leave these unset and use the fallbacks below.
const BUILD_WS_PORT: Option<&str> = option_env!("ATTN_BUILD_WS_PORT");
const BUILD_BUNDLE_ID: Option<&str> = option_env!("ATTN_BUILD_BUNDLE_ID");

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

/// Returns the default WS port for the compile-time profile. A per-profile
/// build bakes the authority's resolved port (`ATTN_BUILD_WS_PORT`); default and
/// dev builds, which leave it unset, fall back to their well-known ports.
/// Mirrors `config.WSPortForProfile()` in Go.
pub fn default_port_for_build_profile() -> &'static str {
    if let Some(port) = BUILD_WS_PORT {
        let port = port.trim();
        if !port.is_empty() {
            return port;
        }
    }
    match build_profile() {
        "dev" => "29849",
        _ => "9849",
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

fn data_dir() -> Result<PathBuf, String> {
    let home = dirs::home_dir().ok_or_else(|| "home directory is unavailable".to_string())?;
    let name = match build_profile() {
        "" => ".attn".to_string(),
        profile => format!(".attn-{profile}"),
    };
    Ok(home.join(name))
}

/// Returns the stable per-profile secret used to authenticate the trusted main
/// webview as the daemon's browser host. The token is persisted with owner-only
/// permissions so app restarts can reconnect to a daemon that stayed alive.
pub fn ensure_browser_host_token() -> Result<String, String> {
    let dir = data_dir()?;
    let path = dir.join("browser-host-token");
    if let Ok(token) = fs::read_to_string(&path) {
        let token = token.trim().to_string();
        if token.len() >= 64 {
            return Ok(token);
        }
    }

    fs::create_dir_all(&dir).map_err(|error| format!("create attn data directory: {error}"))?;
    let mut random = [0_u8; 32];
    getrandom::getrandom(&mut random)
        .map_err(|error| format!("generate browser host token: {error}"))?;
    let token = random
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>();
    let mut file = OpenOptions::new()
        .create(true)
        .truncate(true)
        .write(true)
        .mode(0o600)
        .open(&path)
        .map_err(|error| format!("open browser host token: {error}"))?;
    file.write_all(token.as_bytes())
        .map_err(|error| format!("write browser host token: {error}"))?;
    fs::set_permissions(&path, fs::Permissions::from_mode(0o600))
        .map_err(|error| format!("secure browser host token: {error}"))?;
    Ok(token)
}

/// macOS bundle identifier for the running build. A per-profile build bakes the
/// authority's resolved id (`ATTN_BUILD_BUNDLE_ID`), which is the same value the
/// generated Tauri `--config` overlay sets as `identifier`, so the bundle's id
/// and this runtime view can never diverge. Default and dev builds, which leave
/// it unset, fall back to their well-known ids.
pub fn bundle_identifier() -> &'static str {
    if let Some(id) = BUILD_BUNDLE_ID {
        let id = id.trim();
        if !id.is_empty() {
            return id;
        }
    }
    match build_profile() {
        "dev" => "com.attn.manager.dev",
        _ => "com.attn.manager",
    }
}

/// Decide whether the UI automation bridge should run this launch.
///
///   - `ATTN_AUTOMATION=1`                     → on
///   - `ATTN_AUTOMATION=0`                     → off
///   - any non-empty `ATTN_PROFILE`            → on   (dev sibling or any named profile)
///   - otherwise (prod's empty profile / unset) → off
///
/// Every non-empty profile is an isolated, non-prod world (the `dev` sibling or
/// a named profile like `ticketqa`/`agent7`) the real-app harness may attach to.
/// Production is the empty-profile bundle, so it stays off unless an operator
/// opts in with `ATTN_AUTOMATION=1`.
///
/// `apply_build_profile_env` must run first so a profiled build always sees the
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
    // Any non-empty profile is a non-prod, isolated world; production is the
    // empty-profile bundle and stays off (handled above via ATTN_AUTOMATION).
    profile.map(str::trim).is_some_and(|p| !p.is_empty())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn unbaked_build_falls_back_to_prod_resources() {
        // In a plain `cargo test` build none of the ATTN_BUILD_* vars are set,
        // so the fallbacks must resolve to the SAFE prod values — never dev's
        // 29849 (the pre-PR4 unknown-profile collision bug).
        assert_eq!(build_profile(), "");
        assert_eq!(default_port_for_build_profile(), "9849");
        assert_eq!(bundle_identifier(), "com.attn.manager");
    }

    #[test]
    fn automation_decision_rules() {
        // Explicit override wins regardless of profile.
        assert!(decide_automation_enabled(Some("1"), None));
        assert!(decide_automation_enabled(Some("1"), Some("dev")));
        assert!(decide_automation_enabled(Some("1"), Some("")));
        assert!(!decide_automation_enabled(Some("0"), None));
        assert!(!decide_automation_enabled(Some("0"), Some("dev")));
        assert!(!decide_automation_enabled(Some("0"), Some("ticketqa")));
        // Unrecognized override value is strict-off (typo guard).
        assert!(!decide_automation_enabled(Some("yes"), Some("dev")));
        // Any non-empty profile (dev sibling or any named profile) → on.
        assert!(decide_automation_enabled(None, Some("dev")));
        assert!(decide_automation_enabled(None, Some("DEV")));
        assert!(decide_automation_enabled(None, Some("ticketqa")));
        assert!(decide_automation_enabled(None, Some("agent7")));
        assert!(decide_automation_enabled(None, Some("ci")));
        // Production is the empty profile (or unset) → off.
        assert!(!decide_automation_enabled(None, None));
        assert!(!decide_automation_enabled(None, Some("")));
        assert!(!decide_automation_enabled(None, Some("  ")));
        // Blank/whitespace override falls through to the profile rule.
        assert!(decide_automation_enabled(Some(""), Some("dev")));
        assert!(decide_automation_enabled(Some("  "), Some("ticketqa")));
        assert!(!decide_automation_enabled(Some("  "), None));
        assert!(!decide_automation_enabled(Some(""), Some("")));
    }
}
