use extism_pdk::*;
use serde::Deserialize;
use serde_json::{json, Map, Value};

#[derive(Deserialize)]
struct Input {
    keys: Vec<String>,
}

#[plugin_fn]
pub fn handle(Json(input): Json<Input>) -> FnResult<Json<Value>> {
    let mut cfg = Map::new();
    for k in input.keys {
        let v = config::get(&k)?.map(Value::String).unwrap_or(Value::Null);
        cfg.insert(k, v);
    }
    Ok(Json(json!({"result": {"config": cfg}, "effects": []})))
}
