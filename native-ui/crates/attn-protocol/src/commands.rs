use serde::Serialize;

#[derive(Debug, Serialize)]
pub struct QueryMessage {
    pub cmd: String,
}

impl QueryMessage {
    pub fn new() -> Self {
        Self {
            cmd: "query".to_string(),
        }
    }
}
