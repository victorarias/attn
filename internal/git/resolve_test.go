package git

import "testing"

func TestOriginOwnerRepo(t *testing.T) {
	cases := []struct {
		name   string
		remote string
		want   string
	}{
		{"scp-like with .git", "git@github.com:owner/name.git", "owner/name"},
		{"scp-like without .git", "git@github.com:owner/name", "owner/name"},
		{"ssh URL", "ssh://git@github.com/owner/name.git", "owner/name"},
		{"https URL", "https://github.com/owner/name.git", "owner/name"},
		{"https URL without .git", "https://github.com/owner/name", "owner/name"},
		{"https URL with trailing slash", "https://github.com/owner/name/", "owner/name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			runGit(t, dir, "init")
			runGit(t, dir, "remote", "add", "origin", tc.remote)

			got := OriginOwnerRepo(dir)
			if got != tc.want {
				t.Errorf("OriginOwnerRepo(%q) = %q, want %q", tc.remote, got, tc.want)
			}
		})
	}
}

func TestOriginOwnerRepo_NotGitRepo(t *testing.T) {
	dir := t.TempDir()
	if got := OriginOwnerRepo(dir); got != "" {
		t.Errorf("OriginOwnerRepo(non-repo) = %q, want empty", got)
	}
}

func TestOriginOwnerRepo_NoOrigin(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	if got := OriginOwnerRepo(dir); got != "" {
		t.Errorf("OriginOwnerRepo(no origin) = %q, want empty", got)
	}
}

func TestOwnerRepoFromRemoteUnparseable(t *testing.T) {
	cases := []string{"", "just-a-name", "https://github.com/"}
	for _, remote := range cases {
		if got := ownerRepoFromRemote(remote); got != "" {
			t.Errorf("ownerRepoFromRemote(%q) = %q, want empty", remote, got)
		}
	}
}
