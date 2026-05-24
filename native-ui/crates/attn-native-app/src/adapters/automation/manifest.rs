use serde::Serialize;
use std::{fs, io, path::Path};

#[derive(Debug, Serialize)]
pub struct Manifest {
    pub enabled: bool,
    pub port: u16,
    pub token: String,
    pub pid: u32,
    pub started_at: String,
}

pub fn generate_token() -> String {
    let mut bytes = [0_u8; 32];
    getrandom::getrandom(&mut bytes).expect("automation token randomness unavailable");
    bytes.iter().map(|byte| format!("{byte:02x}")).collect()
}

pub fn write(path: &Path, manifest: &Manifest) -> io::Result<()> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)?;
    }
    let body = serde_json::to_string_pretty(manifest)
        .map_err(|error| io::Error::new(io::ErrorKind::InvalidData, error))?;
    fs::write(path, format!("{body}\n"))
}

pub fn delete(path: &Path) -> io::Result<()> {
    match fs::remove_file(path) {
        Ok(()) => Ok(()),
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(()),
        Err(error) => Err(error),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn tokens_are_random_hex() {
        let first = generate_token();
        let second = generate_token();
        assert_eq!(first.len(), 64);
        assert!(first.chars().all(|char| char.is_ascii_hexdigit()));
        assert_ne!(first, second);
    }
}
