package main

import (
	"testing"

	"github.com/victorarias/attn/internal/config"
)

func TestResolveWSURL(t *testing.T) {
	cases := []struct {
		name        string
		explicitURL string
		profile     string
		want        string
	}{
		{"explicit URL wins over profile", "ws://localhost:9849/ws", "mdclick1", "ws://localhost:9849/ws"},
		{"no env falls back to dev", "", "", defaultWSURL},
		{"blank profile falls back to dev", "", "   ", defaultWSURL},
		{"default profile falls back to dev, never prod", "", "default", defaultWSURL},
		{"dev profile resolves to dev port", "", "dev", "ws://localhost:29849/ws"},
		{"named profile resolves to its derived port", "", "mdclick1", "ws://localhost:" + config.WSPortForProfile("mdclick1") + "/ws"},
		{"profile name is case-insensitive", "", "MDCLICK1", "ws://localhost:" + config.WSPortForProfile("mdclick1") + "/ws"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveWSURL(tc.explicitURL, tc.profile); got != tc.want {
				t.Fatalf("resolveWSURL(%q, %q) = %q, want %q", tc.explicitURL, tc.profile, got, tc.want)
			}
		})
	}
}

func TestResolveWSURLNeverImplicitProd(t *testing.T) {
	prod := "ws://localhost:" + config.WSPortForProfile("") + "/ws"
	for _, profile := range []string{"", "default", "DEFAULT", " ", "dev", "mdclick1"} {
		if got := resolveWSURL("", profile); got == prod {
			t.Fatalf("resolveWSURL(\"\", %q) resolved to prod %q; prod must require an explicit ATTN_WS_URL", profile, got)
		}
	}
}
