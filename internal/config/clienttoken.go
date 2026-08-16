package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ClientTokenFile is the profile-data-dir name of the credential every local
// client presents in client_hello. It sits beside the socket, and like the
// socket it is owner-only: the WebSocket port has no file permissions of its
// own, so this is what stands between another local process — including another
// profile's app — and the full protocol.
const ClientTokenFile = "client-token"

// ClientTokenPath returns the active profile's client-token path.
func ClientTokenPath() string {
	return filepath.Join(attnDir(), ClientTokenFile)
}

// ClientToken returns the token a client should present. Empty when the file is
// absent — the daemon's refusal names the path, which is a better answer than a
// client guessing.
//
// ATTN_CLIENT_TOKEN wins so a harness can spawn a daemon and its clients with
// one value instead of racing the file into existence.
func ClientToken() string {
	if token := strings.TrimSpace(os.Getenv("ATTN_CLIENT_TOKEN")); token != "" {
		return token
	}
	data, err := os.ReadFile(ClientTokenPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// EnsureClientToken returns the token stored under dir, minting one on first
// use. The daemon calls it at startup against its own data root; reusing an
// existing token is what keeps a daemon restart from stranding every client.
func EnsureClientToken(dir string) (string, error) {
	if token := strings.TrimSpace(os.Getenv("ATTN_CLIENT_TOKEN")); token != "" {
		return token, nil
	}
	path := filepath.Join(dir, ClientTokenFile)
	if data, err := os.ReadFile(path); err == nil {
		if token := strings.TrimSpace(string(data)); token != "" {
			return token, nil
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create data directory %s: %w", dir, err)
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate client token: %w", err)
	}
	token := hex.EncodeToString(random)
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		return "", fmt.Errorf("write client token %s: %w", path, err)
	}
	// WriteFile honours the mode only when it creates the file; an existing
	// empty one keeps whatever mode it had.
	if err := os.Chmod(path, 0o600); err != nil {
		return "", fmt.Errorf("secure client token %s: %w", path, err)
	}
	return token, nil
}
