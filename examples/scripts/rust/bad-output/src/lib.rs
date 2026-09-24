use extism_pdk::*;

#[plugin_fn]
pub fn handle(_input: Vec<u8>) -> FnResult<String> {
    Ok(r#"{"result":{"discount_percent":"not-a-number"},"effects":[{"type":"forbidden.effect","payload":{}}]}"#.to_string())
}
