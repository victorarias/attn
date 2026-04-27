/// Wire types for the UI automation TCP protocol. Newline-delimited JSON;
/// one request per line, one response per line. Format must stay compatible
/// with `app/scripts/real-app-harness/uiAutomationClient.mjs`.
use serde::{Deserialize, Serialize};
use serde_json::Value;

#[derive(Debug, Deserialize)]
pub struct Request {
    /// Optional client-supplied id. If absent the server assigns one and
    /// echoes it back in the response so callers can correlate.
    #[serde(default)]
    pub id: Option<String>,
    pub token: String,
    pub action: String,
    #[serde(default)]
    pub payload: Option<Value>,
}

#[derive(Debug, Serialize)]
pub struct Response {
    pub id: String,
    pub ok: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub result: Option<Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<String>,
}

impl Response {
    pub fn ok(id: String, result: Value) -> Self {
        Self {
            id,
            ok: true,
            result: Some(result),
            error: None,
        }
    }

    pub fn err(id: String, message: impl Into<String>) -> Self {
        Self {
            id,
            ok: false,
            result: None,
            error: Some(message.into()),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn request_payload_optional() {
        let parsed: Request = serde_json::from_str(
            r#"{"id":"a","token":"t","action":"ping"}"#,
        )
        .unwrap();
        assert_eq!(parsed.action, "ping");
        assert!(parsed.payload.is_none());
    }

    #[test]
    fn response_omits_unused_fields() {
        let body = serde_json::to_string(&Response::ok(
            "1".into(),
            serde_json::json!({"pong": true}),
        ))
        .unwrap();
        assert!(body.contains("\"ok\":true"));
        assert!(body.contains("\"result\""));
        assert!(!body.contains("\"error\""));

        let body = serde_json::to_string(&Response::err("2".into(), "nope")).unwrap();
        assert!(body.contains("\"ok\":false"));
        assert!(body.contains("\"error\":\"nope\""));
        assert!(!body.contains("\"result\""));
    }
}
