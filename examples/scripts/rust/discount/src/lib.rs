use extism_pdk::*;
use serde::{Deserialize, Serialize};

#[derive(Deserialize)]
struct Customer {
    lifetime_spend: f64,
}

#[derive(Deserialize)]
struct Input {
    #[allow(dead_code)]
    cart_total: f64,
    customer: Customer,
}

#[derive(Serialize)]
struct Discount {
    discount_percent: u32,
    reason: &'static str,
}

#[derive(Serialize)]
struct Output {
    result: Discount,
    effects: Vec<serde_json::Value>,
}

fn cfg_f64(key: &str, default: f64) -> Result<f64, Error> {
    Ok(match config::get(key)? {
        Some(v) => v
            .parse::<f64>()
            .map_err(|e| Error::msg(format!("config {key}: {e}")))?,
        None => default,
    })
}

#[plugin_fn]
pub fn handle(Json(input): Json<Input>) -> FnResult<Json<Output>> {
    let threshold = cfg_f64("threshold", 1000.0)?;
    let percent = cfg_f64("percent", 10.0)? as u32;
    let result = if input.customer.lifetime_spend >= threshold {
        Discount {
            discount_percent: percent,
            reason: "loyal_customer",
        }
    } else {
        Discount {
            discount_percent: 0,
            reason: "none",
        }
    };
    info!("discount computed: {}", result.discount_percent);
    Ok(Json(Output {
        result,
        effects: vec![],
    }))
}
