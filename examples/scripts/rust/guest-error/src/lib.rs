use extism_pdk::*;

#[plugin_fn]
pub fn handle(_input: Vec<u8>) -> FnResult<Vec<u8>> {
    Err(Error::msg("guest failure: boom").into())
}
