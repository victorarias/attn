use std::{env, path::PathBuf};

fn main() {
    if env::var("CARGO_CFG_TARGET_OS").as_deref() != Ok("macos") {
        return;
    }

    let ghostty_dir = env::var_os("ATTN_GHOSTTY_DIR")
        .map(PathBuf::from)
        .unwrap_or_else(|| {
            PathBuf::from(env!("CARGO_MANIFEST_DIR"))
                .join("../../../../ghostty")
                .canonicalize()
                .expect("resolve sibling Ghostty fork at ../../../../ghostty")
        });
    let library_dir = ghostty_dir.join("macos/GhosttyKit.xcframework/macos-arm64");
    let library = library_dir.join("libghostty-internal-fat.a");
    if !library.is_file() {
        panic!(
            "missing {}; run `make build-native-ghostty-kit` from the attn repository",
            library.display()
        );
    }

    println!("cargo:rerun-if-env-changed=ATTN_GHOSTTY_DIR");
    println!("cargo:rerun-if-changed={}", library.display());
    println!("cargo:rustc-link-search=native={}", library_dir.display());
    println!("cargo:rustc-link-lib=static=ghostty-internal-fat");
    println!("cargo:rustc-link-lib=c++");
    for framework in [
        "AppKit",
        "Carbon",
        "CoreFoundation",
        "CoreGraphics",
        "CoreText",
        "CoreVideo",
        "Foundation",
        "IOSurface",
        "Metal",
        "QuartzCore",
        "Security",
    ] {
        println!("cargo:rustc-link-lib=framework={framework}");
    }
}
