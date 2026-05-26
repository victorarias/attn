use std::path::PathBuf;

const PROFILE_ENV: &str = "ATTN_PROFILE";
const AUTOMATION_ENV: &str = "ATTN_AUTOMATION";
const START_EMPTY_ENV: &str = "ATTN_AUTOMATION_START_EMPTY";
const BACKGROUND_WINDOW_ENV: &str = "ATTN_AUTOMATION_BACKGROUND";
const BASE_BUNDLE_ID: &str = "com.attn.native";
const BUILD_PROFILE: Option<&str> = option_env!("ATTN_NATIVE_BUILD_PROFILE");

pub fn profile() -> Option<String> {
    configured_profile(std::env::var(PROFILE_ENV).ok().as_deref(), BUILD_PROFILE)
}

fn configured_profile(runtime: Option<&str>, build: Option<&str>) -> Option<String> {
    let normalized = runtime.or(build)?.trim().to_ascii_lowercase();
    if normalized.is_empty() || normalized == "default" || !is_valid_profile(&normalized) {
        return None;
    }
    Some(normalized)
}

pub fn automation_enabled() -> bool {
    decide_automation_enabled(
        std::env::var(AUTOMATION_ENV).ok().as_deref(),
        profile().as_deref(),
    )
}

pub fn start_empty() -> bool {
    decide_start_empty(std::env::var(START_EMPTY_ENV).ok().as_deref())
}

pub fn background_window() -> bool {
    decide_background_window(
        automation_enabled(),
        std::env::var(BACKGROUND_WINDOW_ENV).ok().as_deref(),
    )
}

fn decide_background_window(enabled: bool, value: Option<&str>) -> bool {
    enabled && value.map(str::trim) == Some("1")
}

fn decide_start_empty(value: Option<&str>) -> bool {
    value.map(str::trim) == Some("1")
}

fn decide_automation_enabled(automation: Option<&str>, profile: Option<&str>) -> bool {
    match automation.map(str::trim) {
        Some("1") => return true,
        Some("0") => return false,
        Some("") | None => {}
        Some(_) => return false,
    }
    profile == Some("dev")
}

pub fn manifest_path() -> PathBuf {
    manifest_path_for(profile().as_deref())
}

fn manifest_path_for(profile: Option<&str>) -> PathBuf {
    let bundle_id = match profile {
        Some(profile) => format!("{BASE_BUNDLE_ID}.{profile}"),
        None => BASE_BUNDLE_ID.to_string(),
    };
    std::env::var_os("HOME")
        .map(PathBuf::from)
        .unwrap_or_else(|| PathBuf::from("/tmp"))
        .join("Library")
        .join("Application Support")
        .join(bundle_id)
        .join("debug")
        .join("ui-automation.json")
}

fn is_valid_profile(value: &str) -> bool {
    let bytes = value.as_bytes();
    !bytes.is_empty()
        && bytes.len() <= 16
        && is_alnum(bytes[0])
        && bytes[1..]
            .iter()
            .all(|byte| is_alnum(*byte) || *byte == b'-')
}

fn is_alnum(byte: u8) -> bool {
    matches!(byte, b'a'..=b'z' | b'0'..=b'9')
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn automation_is_on_by_default_only_for_dev() {
        assert!(decide_automation_enabled(None, Some("dev")));
        assert!(!decide_automation_enabled(None, None));
        assert!(decide_automation_enabled(Some("1"), None));
        assert!(!decide_automation_enabled(Some("0"), Some("dev")));
        assert!(!decide_automation_enabled(Some("yes"), Some("dev")));
    }

    #[test]
    fn runtime_profile_overrides_bundled_default() {
        assert_eq!(
            configured_profile(Some("agenttest"), Some("dev")).as_deref(),
            Some("agenttest")
        );
        assert_eq!(
            configured_profile(None, Some("dev")).as_deref(),
            Some("dev")
        );
    }

    #[test]
    fn dev_manifest_has_distinct_namespace() {
        let path = manifest_path_for(Some("dev"));
        assert!(path.to_string_lossy().contains("com.attn.native.dev"));
        assert!(path.ends_with("debug/ui-automation.json"));
    }

    #[test]
    fn explicit_automation_window_can_start_without_attaching_a_workspace() {
        assert!(decide_start_empty(Some("1")));
        assert!(!decide_start_empty(None));
        assert!(!decide_start_empty(Some("0")));
    }

    #[test]
    fn background_window_requires_automation() {
        assert!(decide_background_window(true, Some("1")));
        assert!(!decide_background_window(false, Some("1")));
        assert!(!decide_background_window(true, Some("0")));
    }
}
