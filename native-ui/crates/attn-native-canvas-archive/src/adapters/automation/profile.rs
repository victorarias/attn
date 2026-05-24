/// Profile resolution + automation gating.
///
/// One decision rule is shared across native Rust, Tauri Rust, and the
/// harness JS side; this is the Rust copy. Keep it in sync with:
///   - `app/src-tauri/src/profile.rs`            (Tauri runtime gate)
///   - `app/scripts/real-app-harness/nativeHarnessProfile.mjs`
///   - `internal/config/config.go`               (profile name regex)
///
/// `automation_enabled` rule:
///   - `ATTN_AUTOMATION=1`  → on
///   - `ATTN_AUTOMATION=0`  → off
///   - `ATTN_PROFILE=dev`   → on   (default for dev)
///   - otherwise            → off  (default for prod / unset)
use std::path::PathBuf;

const PROFILE_ENV: &str = "ATTN_PROFILE";
const AUTOMATION_ENV: &str = "ATTN_AUTOMATION";
const BASE_BUNDLE_ID: &str = "com.attn.native";

/// Resolved profile name (lowercased, validated). `None` for default profile
/// or for any input that fails validation — this matches the Go side's
/// permissive read path: bad profile env values silently fall through to
/// default rather than crashing the app.
pub fn profile() -> Option<String> {
    let raw = std::env::var(PROFILE_ENV).ok()?;
    let normalized = raw.trim().to_ascii_lowercase();
    if normalized.is_empty() || normalized == "default" {
        return None;
    }
    if !is_valid_profile(&normalized) {
        return None;
    }
    Some(normalized)
}

/// Decide whether the automation server should run this launch. Single rule
/// for both apps. See module doc.
pub fn automation_enabled() -> bool {
    decide_automation_enabled(
        std::env::var(AUTOMATION_ENV).ok().as_deref(),
        profile().as_deref(),
    )
}

/// Pure decision function so the rule itself is testable without touching
/// process env.
fn decide_automation_enabled(automation: Option<&str>, profile: Option<&str>) -> bool {
    match automation.map(str::trim) {
        Some("1") => return true,
        Some("0") => return false,
        Some("") | None => {}
        // Any other value — be strict here so typos don't silently disable
        // automation in CI. Treat as "explicit off" for safety.
        Some(_) => return false,
    }
    profile == Some("dev")
}

/// Bundle identifier for the manifest path. Prod (default profile) lands at
/// `com.attn.native`; dev/other profiles get a suffix so installs of the
/// same app for different profiles don't collide on disk. Used internally
/// by `manifest_path` and exposed to tests.
fn bundle_identifier_for(profile: Option<&str>) -> String {
    match profile {
        Some(p) if !p.is_empty() => format!("{BASE_BUNDLE_ID}.{p}"),
        _ => BASE_BUNDLE_ID.to_string(),
    }
}

/// Manifest path mirroring the Tauri side
/// (`~/Library/Application Support/<bundle-id>/debug/ui-automation.json`).
/// Falls back to `/tmp` if the home dir can't be read; the manifest is a
/// dev affordance, not a hard product requirement, and crashing the app
/// here would be worse than writing to a less-discoverable path.
pub fn manifest_path() -> PathBuf {
    manifest_path_for(profile().as_deref())
}

pub fn manifest_path_for(profile: Option<&str>) -> PathBuf {
    let base = dirs_home().unwrap_or_else(|| PathBuf::from("/tmp"));
    base.join("Library")
        .join("Application Support")
        .join(bundle_identifier_for(profile))
        .join("debug")
        .join("ui-automation.json")
}

fn dirs_home() -> Option<PathBuf> {
    std::env::var_os("HOME").map(PathBuf::from)
}

fn is_valid_profile(s: &str) -> bool {
    // Mirror Go's `^[a-z0-9][a-z0-9-]{0,15}$`. Manual validation avoids
    // pulling in the `regex` crate just for one tiny pattern.
    let bytes = s.as_bytes();
    if bytes.is_empty() || bytes.len() > 16 {
        return false;
    }
    if !is_alnum_lower(bytes[0]) {
        return false;
    }
    bytes[1..].iter().all(|&b| is_alnum_lower(b) || b == b'-')
}

fn is_alnum_lower(b: u8) -> bool {
    matches!(b, b'a'..=b'z' | b'0'..=b'9')
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn profile_validation_matches_go_regex() {
        for ok in ["dev", "ci", "a", "a-b", "abcdefghijklmnop"] {
            assert!(is_valid_profile(ok), "expected ok: {ok}");
        }
        for bad in [
            "",
            "-dev",
            "Dev",
            "dev_",
            "dev space",
            "abcdefghijklmnopq", // 17 chars
        ] {
            assert!(!is_valid_profile(bad), "expected bad: {bad}");
        }
    }

    #[test]
    fn bundle_identifier_namespace() {
        assert_eq!(bundle_identifier_for(None), "com.attn.native");
        assert_eq!(bundle_identifier_for(Some("dev")), "com.attn.native.dev");
        assert_eq!(bundle_identifier_for(Some("ci")), "com.attn.native.ci");
    }

    #[test]
    fn manifest_path_includes_bundle_namespace() {
        let dev = manifest_path_for(Some("dev"));
        assert!(dev.to_string_lossy().contains("com.attn.native.dev"));
        assert!(dev.ends_with("debug/ui-automation.json"));
    }

    #[test]
    fn automation_decision_rules() {
        // Explicit env wins over everything.
        assert!(decide_automation_enabled(Some("1"), None));
        assert!(decide_automation_enabled(Some("1"), Some("dev")));
        assert!(!decide_automation_enabled(Some("0"), None));
        assert!(!decide_automation_enabled(Some("0"), Some("dev")));
        // Bogus value → strict off.
        assert!(!decide_automation_enabled(Some("yes"), Some("dev")));
        // Unset env → profile decides.
        assert!(decide_automation_enabled(None, Some("dev")));
        assert!(!decide_automation_enabled(None, Some("ci")));
        assert!(!decide_automation_enabled(None, None));
        // Empty / whitespace env value treated as unset → profile decides.
        assert!(decide_automation_enabled(Some(""), Some("dev")));
        assert!(decide_automation_enabled(Some("  "), Some("dev")));
        assert!(!decide_automation_enabled(Some("  "), None));
    }
}
