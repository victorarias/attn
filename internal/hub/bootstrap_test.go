package hub

import "testing"

func TestShouldInstallRemoteBinary(t *testing.T) {
	tests := []struct {
		name              string
		localVersion      string
		remoteVersion     string
		preferSourceBuild bool
		localHash         string
		remoteHash        string
		want              bool
	}{
		{
			name:              "different version always installs",
			localVersion:      "0.3.2",
			remoteVersion:     "0.3.1",
			preferSourceBuild: false,
			want:              true,
		},
		{
			name:              "same version no source build skips install",
			localVersion:      "0.3.2",
			remoteVersion:     "0.3.2",
			preferSourceBuild: false,
			localHash:         "abc",
			remoteHash:        "def",
			want:              false,
		},
		{
			name:              "same version source build installs on hash mismatch",
			localVersion:      "0.3.2",
			remoteVersion:     "0.3.2",
			preferSourceBuild: true,
			localHash:         "abc",
			remoteHash:        "def",
			want:              true,
		},
		{
			name:              "same version source build skips on matching hash",
			localVersion:      "0.3.2",
			remoteVersion:     "0.3.2",
			preferSourceBuild: true,
			localHash:         "abc",
			remoteHash:        "abc",
			want:              false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldInstallRemoteBinary(tt.localVersion, tt.remoteVersion, tt.preferSourceBuild, tt.localHash, tt.remoteHash)
			if got != tt.want {
				t.Fatalf("shouldInstallRemoteBinary(...) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsRemoteHarnessOverridePath(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{value: "", want: false},
		{value: "/home/victor/.attn/attn.sock", want: false},
		{value: "/home/victor/.attn/harness/run-123/attn.sock", want: true},
		{value: "~/.attn/harness/run-123/bin/attn", want: true},
	}

	for _, tt := range tests {
		if got := isRemoteHarnessOverridePath(tt.value); got != tt.want {
			t.Fatalf("isRemoteHarnessOverridePath(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

func TestRemoteHarnessCleanupEnabled(t *testing.T) {
	t.Setenv("ATTN_REMOTE_SOCKET_PATH", "")
	t.Setenv("ATTN_REMOTE_DB_PATH", "")
	t.Setenv("ATTN_REMOTE_ATTN_BIN", "")
	if remoteHarnessCleanupEnabled() {
		t.Fatal("remoteHarnessCleanupEnabled() = true, want false without harness overrides")
	}

	t.Setenv("ATTN_REMOTE_SOCKET_PATH", "/home/victor/.attn/harness/run-456/attn.sock")
	if !remoteHarnessCleanupEnabled() {
		t.Fatal("remoteHarnessCleanupEnabled() = false, want true with harness socket override")
	}
}
