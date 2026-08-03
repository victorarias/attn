package pty

import "testing"

// The override is a diagnostic surface, so the only thing it must never do is
// take a session down or turn images on by accident. Unset and unparseable both
// mean the shipping default: ghostty refuses every transmission.
func TestKittyStorageLimitFromEnvironment(t *testing.T) {
	cases := []struct {
		name  string
		value string
		set   bool
		want  uint64
	}{
		{name: "unset is the shipping default", want: 0},
		{name: "empty is the shipping default", value: "", set: true, want: 0},
		{name: "a byte count is the limit", value: "16777216", set: true, want: 16777216},
		{name: "surrounding space is tolerated", value: "  4096  ", set: true, want: 4096},
		{name: "a size suffix is not a byte count", value: "64MB", set: true, want: 0},
		{name: "a negative value is not a byte count", value: "-1", set: true, want: 0},
		{name: "words are not a byte count", value: "yes", set: true, want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(kittyStorageLimitEnv, "")
			if tc.set {
				t.Setenv(kittyStorageLimitEnv, tc.value)
			}
			var logged int
			got := kittyStorageLimit(func(string, ...interface{}) { logged++ })
			if got != tc.want {
				t.Errorf("kittyStorageLimit() = %d, want %d", got, tc.want)
			}
			// A value that was meant to enable images and did not must say so:
			// silently dark is the failure nobody can debug.
			wantLog := tc.set && tc.value != "" && tc.want == 0
			if wantLog && logged == 0 {
				t.Errorf("%s=%q was ignored without a word about it", kittyStorageLimitEnv, tc.value)
			}
			if !wantLog && logged != 0 {
				t.Errorf("%s=%q logged %d times, want silence", kittyStorageLimitEnv, tc.value, logged)
			}
		})
	}
}
