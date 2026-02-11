package ptyworker

import (
	"encoding/json"
	"testing"
)

func TestRequestEnvelopeRoundTrip(t *testing.T) {
	orig := RequestEnvelope{
		Type:   "req",
		ID:     "r1",
		Method: MethodHello,
		Params: mustRawJSON(t, HelloParams{
			RPCMajor:         RPCMajor,
			RPCMinor:         RPCMinor,
			DaemonInstanceID: "d-123",
			ControlToken:     "tok",
		}),
	}

	payload, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal request envelope: %v", err)
	}

	var decoded RequestEnvelope
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal request envelope: %v", err)
	}
	if decoded.Type != orig.Type || decoded.ID != orig.ID || decoded.Method != orig.Method {
		t.Fatalf("decoded envelope mismatch: got=%+v want=%+v", decoded, orig)
	}

	var params HelloParams
	if err := json.Unmarshal(decoded.Params, &params); err != nil {
		t.Fatalf("unmarshal hello params: %v", err)
	}
	if params.DaemonInstanceID != "d-123" {
		t.Fatalf("daemon_instance_id = %q, want %q", params.DaemonInstanceID, "d-123")
	}
}

func TestResponseEnvelopeErrorRoundTrip(t *testing.T) {
	orig := ResponseEnvelope{
		Type: "res",
		ID:   "r2",
		OK:   false,
		Error: &RPCError{
			Code:    ErrUnauthorized,
			Message: "token mismatch",
		},
	}

	payload, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal response envelope: %v", err)
	}

	var decoded ResponseEnvelope
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal response envelope: %v", err)
	}
	if decoded.OK {
		t.Fatal("decoded response unexpectedly ok=true")
	}
	if decoded.Error == nil || decoded.Error.Code != ErrUnauthorized {
		t.Fatalf("decoded error = %+v, want code=%s", decoded.Error, ErrUnauthorized)
	}
}

func TestIsCompatibleVersion(t *testing.T) {
	tests := []struct {
		name      string
		major     int
		minor     int
		wantValid bool
	}{
		{name: "exact current version", major: RPCMajor, minor: RPCMinor, wantValid: true},
		{name: "minimum compatible", major: RPCMajor, minor: MinCompatibleRPCMinor, wantValid: true},
		{name: "major mismatch", major: RPCMajor + 1, minor: RPCMinor, wantValid: false},
		{name: "minor below window", major: RPCMajor, minor: MinCompatibleRPCMinor - 1, wantValid: false},
		{name: "minor above current", major: RPCMajor, minor: RPCMinor + 1, wantValid: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsCompatibleVersion(tc.major, tc.minor)
			if got != tc.wantValid {
				t.Fatalf("IsCompatibleVersion(%d, %d) = %v, want %v", tc.major, tc.minor, got, tc.wantValid)
			}
		})
	}
}

func mustRawJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal raw json: %v", err)
	}
	return payload
}
