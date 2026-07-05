package git

import "testing"

func TestOriginHostOwnerRepo(t *testing.T) {
	cases := []struct {
		name     string
		remote   string
		wantHost string
		wantSlug string
	}{
		{"scp-like with .git", "git@github.com:owner/name.git", "github.com", "owner/name"},
		{"scp-like without .git", "git@github.com:owner/name", "github.com", "owner/name"},
		{"ssh URL", "ssh://git@github.com/owner/name.git", "github.com", "owner/name"},
		{"ssh URL enterprise host", "ssh://git@ghe.corp/owner/name", "ghe.corp", "owner/name"},
		{"https URL", "https://github.com/owner/name.git", "github.com", "owner/name"},
		{"https URL without .git", "https://github.com/owner/name", "github.com", "owner/name"},
		{"https URL with trailing slash", "https://github.com/owner/name/", "github.com", "owner/name"},
		{"ssh URL with port", "ssh://git@github.com:2222/owner/name.git", "github.com", "owner/name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			runGit(t, dir, "init")
			runGit(t, dir, "remote", "add", "origin", tc.remote)

			host, slug := OriginHostOwnerRepo(dir)
			if host != tc.wantHost || slug != tc.wantSlug {
				t.Errorf("OriginHostOwnerRepo(%q) = (%q, %q), want (%q, %q)",
					tc.remote, host, slug, tc.wantHost, tc.wantSlug)
			}
			if got := OriginOwnerRepo(dir); got != tc.wantSlug {
				t.Errorf("OriginOwnerRepo(%q) = %q, want %q", tc.remote, got, tc.wantSlug)
			}
		})
	}
}

func TestOriginHostOwnerRepo_NotGitRepo(t *testing.T) {
	dir := t.TempDir()
	if host, slug := OriginHostOwnerRepo(dir); host != "" || slug != "" {
		t.Errorf("OriginHostOwnerRepo(non-repo) = (%q, %q), want empty", host, slug)
	}
}

func TestOriginHostOwnerRepo_NoOrigin(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	if host, slug := OriginHostOwnerRepo(dir); host != "" || slug != "" {
		t.Errorf("OriginHostOwnerRepo(no origin) = (%q, %q), want empty", host, slug)
	}
}

func TestHostOwnerRepoFromRemoteUnparseable(t *testing.T) {
	cases := []string{"", "just-a-name", "https://github.com/", "relative/path/only"}
	for _, remote := range cases {
		if host, slug := hostOwnerRepoFromRemote(remote); host != "" || slug != "" {
			t.Errorf("hostOwnerRepoFromRemote(%q) = (%q, %q), want empty", remote, host, slug)
		}
	}
}
