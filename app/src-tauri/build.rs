use std::path::Path;

fn main() {
    track_frontend_dist();
    tauri_build::build()
}

/// Teach cargo that the built frontend is an input to this crate.
///
/// `tauri::generate_context!()` embeds `build.frontendDist` when the proc macro
/// expands, but `tauri_build::build()` only emits a `rerun-if-changed` for that
/// directory on the opt-in codegen path (`CodegenContext`, which we do not use:
/// it resolves the config at build-script time and so would not see the
/// `--config` overlay that named-profile builds pass). With the plain macro,
/// cargo never learns `app/dist` is an input — a frontend-only change leaves the
/// crate "fresh", the crate is not recompiled, and the packaged app ships the
/// previously embedded assets.
///
/// That staleness is why installing used to require
/// `touch app/src-tauri/src/lib.rs` first. Emitting the dependency here can only
/// cause more rebuilds, never a wrong one.
///
/// Note what it does NOT buy. `make dev` runs vite first, and vite's
/// `emptyOutDir` default wipes and rewrites `dist` every time, so the directory
/// mtime always advances and this crate recompiles on every `make dev` even with
/// no frontend change (measured back to back with no edits: 27.7s then 29.1s of
/// a ~50s build). That is exactly what the `touch lib.rs` workaround already
/// cost, so it is not a regression against how this was actually built — it buys
/// correctness, not speed. Go-only changes stay fast via
/// `make install-daemon-dev`, which never builds Tauri at all.
fn track_frontend_dist() {
    let Ok(manifest_dir) = std::env::var("CARGO_MANIFEST_DIR") else {
        return;
    };
    let manifest_dir = Path::new(&manifest_dir);

    let Ok(raw) = std::fs::read_to_string(manifest_dir.join("tauri.conf.json")) else {
        return;
    };
    let Ok(config) = serde_json::from_str::<serde_json::Value>(&raw) else {
        return;
    };
    let Some(dist) = config
        .pointer("/build/frontendDist")
        .and_then(serde_json::Value::as_str)
    else {
        return;
    };

    // `frontendDist` may also be a dev-server URL or an explicit file list;
    // only a directory path is meaningful to track here.
    if dist.starts_with("http://") || dist.starts_with("https://") {
        return;
    }

    let dist_dir = manifest_dir.join(dist);
    if dist_dir.is_dir() {
        // Track the directory rather than its files: cargo rescans it on each
        // build, so additions and deletions register too. Vite emits
        // content-hashed filenames, so any frontend change alters the set.
        println!("cargo:rerun-if-changed={}", dist_dir.display());
    }
}
