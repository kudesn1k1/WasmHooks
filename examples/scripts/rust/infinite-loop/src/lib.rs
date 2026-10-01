use extism_pdk::*;

#[plugin_fn]
pub fn handle(_input: Vec<u8>) -> FnResult<Vec<u8>> {
    let mut i: u64 = 0;
    loop {
        i = std::hint::black_box(i.wrapping_add(1));
    }
}
