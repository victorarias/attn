/// Discovery manifest written to disk on automation server start so external
/// test scripts can find the live port + token. Mirrors the Tauri side's
/// shape so `uiAutomationClient.mjs` works against either app.
use serde::Serialize;
use std::fs;
use std::io::{self, Write};
use std::path::Path;

#[derive(Debug, Serialize)]
pub struct Manifest {
    pub enabled: bool,
    pub port: u16,
    pub token: String,
    pub pid: u32,
    pub started_at: String,
}

/// Generate a 32-byte random token, hex-encoded. Uses OS randomness via
/// `getrandom` so the token is not predictable from pid + start time
/// (the Tauri side's current scheme — see #12).
pub fn generate_token() -> String {
    let mut buf = [0u8; 32];
    getrandom::getrandom(&mut buf).expect("OS RNG must be available");
    let mut hex = String::with_capacity(buf.len() * 2);
    for byte in buf {
        // Hand-rolled hex to avoid pulling in a dep just for this.
        hex.push(nibble(byte >> 4));
        hex.push(nibble(byte & 0x0f));
    }
    hex
}

fn nibble(n: u8) -> char {
    match n {
        0..=9 => (b'0' + n) as char,
        10..=15 => (b'a' + n - 10) as char,
        _ => unreachable!(),
    }
}

pub fn write(path: &Path, manifest: &Manifest) -> io::Result<()> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)?;
    }
    let body = serde_json::to_string_pretty(manifest)
        .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;
    let mut file = fs::File::create(path)?;
    file.write_all(body.as_bytes())?;
    file.write_all(b"\n")?;
    Ok(())
}

pub fn delete(path: &Path) -> io::Result<()> {
    match fs::remove_file(path) {
        Ok(()) => Ok(()),
        Err(e) if e.kind() == io::ErrorKind::NotFound => Ok(()),
        Err(e) => Err(e),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn token_is_64_hex_chars() {
        let t = generate_token();
        assert_eq!(t.len(), 64);
        assert!(t.chars().all(|c| c.is_ascii_hexdigit() && !c.is_uppercase()));
    }

    #[test]
    fn tokens_differ() {
        // Two independent calls should never collide.
        assert_ne!(generate_token(), generate_token());
    }

    #[test]
    fn write_then_read_roundtrips() {
        let dir = std::env::temp_dir().join(format!(
            "attn-automation-test-{}",
            std::process::id()
        ));
        let path = dir.join("ui-automation.json");
        let _ = fs::remove_dir_all(&dir);

        let manifest = Manifest {
            enabled: true,
            port: 12345,
            token: "deadbeef".into(),
            pid: 9999,
            started_at: "1234".into(),
        };
        write(&path, &manifest).unwrap();

        let body = fs::read_to_string(&path).unwrap();
        assert!(body.contains("\"port\": 12345"));
        assert!(body.contains("\"token\": \"deadbeef\""));

        delete(&path).unwrap();
        assert!(!path.exists());
        // Second delete is idempotent.
        delete(&path).unwrap();

        let _ = fs::remove_dir_all(&dir);
    }
}
