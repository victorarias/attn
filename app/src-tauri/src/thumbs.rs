use regex::Regex;
use serde::Serialize;
use std::collections::HashSet;
use std::process::Command;
use std::sync::LazyLock;

#[derive(Serialize, Clone)]
pub struct PatternMatch {
    pub pattern_type: String,
    pub value: String,
    pub hint: String,
}

// Compiled regexes - lazily initialized once
static URL_REGEX: LazyLock<Regex> = LazyLock::new(|| {
    // From tmux-thumbs: covers http(s), git, ssh, ftp, file protocols
    Regex::new(r#"(https?://|git@|git://|ssh://|ftp://|file:///)[^\s<>"'\)\]]+"#).unwrap()
});

static MARKDOWN_URL_REGEX: LazyLock<Regex> = LazyLock::new(|| {
    // Markdown links: [text](url)
    Regex::new(r"\[[^\]]*\]\(([^)]+)\)").unwrap()
});

static PATH_REGEX: LazyLock<Regex> = LazyLock::new(|| {
    // From tmux-thumbs: handles absolute and relative paths
    // ([.\w\-@$~\[\]]+)?(/[.\w\-@$\[\]]+)+
    Regex::new(r"([.\w\-@$~\[\]]+)?(/[.\w\-@$\[\]]+)+").unwrap()
});

static IP_PORT_REGEX: LazyLock<Regex> = LazyLock::new(|| {
    // IPv4 with port
    Regex::new(r"\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}:\d+").unwrap()
});

static LOCALHOST_REGEX: LazyLock<Regex> = LazyLock::new(|| {
    // localhost with optional port and path
    Regex::new(r#"localhost(:\d+)?(/[^\s<>"'\)\]]*)?"#).unwrap()
});

// ANSI escape code stripper
static ANSI_REGEX: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"\x1b\[[0-9;]*[a-zA-Z]|\x1b\][^\x07]*\x07").unwrap());

fn strip_ansi(text: &str) -> String {
    ANSI_REGEX.replace_all(text, "").to_string()
}

/// Maximum number of hints we can generate: 26 single-letter (a-z) + 676 two-letter (aa-zz)
const MAX_HINTS: usize = 702;

fn generate_hint(index: usize) -> String {
    if index >= MAX_HINTS {
        // Beyond our hint capacity, return empty string
        return String::new();
    }
    if index < 26 {
        // a-z for first 26
        char::from(b'a' + index as u8).to_string()
    } else {
        // aa, ab, ac... for additional (indices 26-701)
        let first = char::from(b'a' + ((index - 26) / 26) as u8);
        let second = char::from(b'a' + ((index - 26) % 26) as u8);
        format!("{}{}", first, second)
    }
}

#[tauri::command]
pub fn extract_patterns(text: String) -> Vec<PatternMatch> {
    let clean_text = strip_ansi(&text);
    let mut seen: HashSet<String> = HashSet::new();
    let mut matches: Vec<PatternMatch> = Vec::new();

    // Extract URLs (highest priority)
    for cap in URL_REGEX.find_iter(&clean_text) {
        let value = cap.as_str().to_string();
        // Clean trailing punctuation that might have been captured
        let value = value.trim_end_matches(|c| c == '.' || c == ',' || c == ';' || c == ':');
        if !seen.contains(value) {
            seen.insert(value.to_string());
            matches.push(PatternMatch {
                pattern_type: "url".to_string(),
                value: value.to_string(),
                hint: String::new(), // Will be assigned later
            });
        }
    }

    // Extract markdown URLs
    for cap in MARKDOWN_URL_REGEX.captures_iter(&clean_text) {
        if let Some(url) = cap.get(1) {
            let value = url.as_str().to_string();
            if !seen.contains(&value) {
                seen.insert(value.clone());
                matches.push(PatternMatch {
                    pattern_type: "url".to_string(),
                    value,
                    hint: String::new(),
                });
            }
        }
    }

    // Extract localhost URLs
    for cap in LOCALHOST_REGEX.find_iter(&clean_text) {
        let value = cap.as_str().to_string();
        if !seen.contains(&value) {
            seen.insert(value.clone());
            matches.push(PatternMatch {
                pattern_type: "url".to_string(),
                value,
                hint: String::new(),
            });
        }
    }

    // Extract IP:port
    for cap in IP_PORT_REGEX.find_iter(&clean_text) {
        let value = cap.as_str().to_string();
        if !seen.contains(&value) {
            seen.insert(value.clone());
            matches.push(PatternMatch {
                pattern_type: "ip_port".to_string(),
                value,
                hint: String::new(),
            });
        }
    }

    // Extract paths (lower priority - often overlap with URLs)
    for cap in PATH_REGEX.find_iter(&clean_text) {
        let value = cap.as_str().to_string();
        // Skip if it looks like part of a URL we already captured
        if seen.contains(&value) {
            continue;
        }
        // Skip very short paths (likely false positives)
        if value.len() < 3 {
            continue;
        }
        // Skip if this is a substring of an existing match (URL path)
        let is_substring = seen.iter().any(|existing| existing.contains(&value));
        if is_substring {
            continue;
        }
        seen.insert(value.clone());
        matches.push(PatternMatch {
            pattern_type: "path".to_string(),
            value,
            hint: String::new(),
        });
    }

    // Reverse so most recent matches appear first (user more likely to want recent paths)
    matches.reverse();

    // Assign hints in order
    for (i, m) in matches.iter_mut().enumerate() {
        m.hint = generate_hint(i);
    }

    matches
}

/// Reveal a file or directory in Finder (macOS)
#[tauri::command]
pub fn reveal_in_finder(path: String) -> Result<(), String> {
    // Use `open -R` to reveal in Finder
    Command::new("open")
        .arg("-R")
        .arg(&path)
        .spawn()
        .map_err(|e| format!("Failed to reveal in Finder: {}", e))?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_extract_urls() {
        let text = "Check out https://github.com/foo/bar and http://example.com";
        let matches = extract_patterns(text.to_string());
        assert!(matches
            .iter()
            .any(|m| m.value == "https://github.com/foo/bar"));
        assert!(matches.iter().any(|m| m.value == "http://example.com"));
    }

    #[test]
    fn test_extract_paths() {
        let text = "Edit /Users/victor/project/src/main.rs or ./config.json";
        let matches = extract_patterns(text.to_string());
        assert!(matches.iter().any(|m| m.value.contains("/Users/victor")));
        assert!(matches.iter().any(|m| m.value.contains("./config.json")));
    }

    #[test]
    fn test_extract_ip_port() {
        let text = "Server at 192.168.1.1:8080 and localhost:3000";
        let matches = extract_patterns(text.to_string());
        assert!(matches.iter().any(|m| m.value == "192.168.1.1:8080"));
        assert!(matches.iter().any(|m| m.value == "localhost:3000"));
    }

    #[test]
    fn test_hint_generation() {
        // Single letter hints (0-25)
        assert_eq!(generate_hint(0), "a");
        assert_eq!(generate_hint(25), "z");

        // Two letter hints start at index 26
        assert_eq!(generate_hint(26), "aa");
        assert_eq!(generate_hint(27), "ab");
        assert_eq!(generate_hint(51), "az"); // index 26 + 25 = 51
        assert_eq!(generate_hint(52), "ba"); // index 26 + 26 = 52

        // Last valid two-letter hint is "zz" at index 701
        // index 26 + (25 * 26) + 25 = 26 + 650 + 25 = 701
        assert_eq!(generate_hint(701), "zz");

        // Beyond capacity returns empty string
        assert_eq!(generate_hint(702), "");
        assert_eq!(generate_hint(1000), "");
        assert_eq!(generate_hint(usize::MAX), "");
    }

    #[test]
    fn test_deduplication() {
        let text = "https://github.com https://github.com https://github.com";
        let matches = extract_patterns(text.to_string());
        assert_eq!(
            matches
                .iter()
                .filter(|m| m.value == "https://github.com")
                .count(),
            1
        );
    }

    #[test]
    fn test_ansi_stripping() {
        let text = "\x1b[32mhttps://github.com\x1b[0m";
        let matches = extract_patterns(text.to_string());
        assert!(matches.iter().any(|m| m.value == "https://github.com"));
    }
}
