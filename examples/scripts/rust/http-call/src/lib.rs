use extism_pdk::*;

#[plugin_fn]
pub fn handle(_input: Vec<u8>) -> FnResult<String> {
    let req = HttpRequest::new("https://example.com");
    let res = http::request::<()>(&req, None)?;
    Ok(format!(
        r#"{{"result":{{"status":{}}},"effects":[]}}"#,
        res.status_code()
    ))
}
