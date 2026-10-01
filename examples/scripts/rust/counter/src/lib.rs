use extism_pdk::*;
use std::sync::atomic::{AtomicU64, Ordering};

static COUNT: AtomicU64 = AtomicU64::new(0);

#[plugin_fn]
pub fn handle(_input: Vec<u8>) -> FnResult<String> {
    let n = COUNT.fetch_add(1, Ordering::SeqCst) + 1;
    Ok(format!(r#"{{"result":{{"count":{n}}},"effects":[]}}"#))
}
