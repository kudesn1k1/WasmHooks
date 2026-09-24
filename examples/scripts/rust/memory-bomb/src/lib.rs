use extism_pdk::*;

#[plugin_fn]
pub fn handle(_input: Vec<u8>) -> FnResult<Vec<u8>> {
    let mut hog: Vec<Vec<u8>> = Vec::new();
    loop {
        hog.push(std::hint::black_box(vec![1u8; 1 << 20]));
    }
}
