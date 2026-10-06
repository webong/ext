fn main() {
    println!("cargo:rerun-if-env-changed=CTX_ENGINE_LIB_DIR");
    if let Ok(path) = std::env::var("CTX_ENGINE_LIB_DIR") {
        println!("cargo:rustc-link-search=native={path}");
    }
    let library = if std::env::var_os("CARGO_FEATURE_GUEST_ONLY").is_some() {
        "ctx_guest"
    } else {
        "ctx_host"
    };
    if std::env::var_os("CARGO_FEATURE_SHARED").is_some() {
        println!("cargo:rustc-link-lib=dylib={library}");
    } else {
        println!("cargo:rustc-link-lib=static={library}_static");
        if std::env::var("CARGO_CFG_TARGET_FAMILY").as_deref() == Ok("unix") {
            if library == "ctx_host" {
                println!("cargo:rustc-link-lib=pthread");
            }
            println!("cargo:rustc-link-lib=m");
        }
    }
}
