package github

import "testing"

func TestParseAuthStatusHosts(t *testing.T) {
	jsonInput := []byte(`{
  "hosts": {
    "github.com": [
      {"state": "success", "active": true, "user": "octocat"}
    ],
    "ghe.corp.com": [
      {"state": "failed", "active": false, "user": "bad"},
      {"state": "success", "active": false, "user": {"login": "enterprise"}}
    ]
  }
}`)

	hosts, err := parseAuthStatusHosts(jsonInput)
	if err != nil {
		t.Fatalf("parseAuthStatusHosts error: %v", err)
	}
	if len(hosts) != 2 {
		t.Fatalf("expected 2 hosts, got %d", len(hosts))
	}

	found := make(map[string]HostInfo)
	for _, host := range hosts {
		found[host.Host] = host
	}

	gh, ok := found["github.com"]
	if !ok {
		t.Fatalf("missing github.com host")
	}
	if gh.APIURL != "https://api.github.com" {
		t.Fatalf("github.com api url = %s", gh.APIURL)
	}
	if gh.Login != "octocat" {
		t.Fatalf("github.com login = %s", gh.Login)
	}
	if !gh.Active {
		t.Fatalf("github.com active should be true")
	}

	ghe, ok := found["ghe.corp.com"]
	if !ok {
		t.Fatalf("missing ghe.corp.com host")
	}
	if ghe.APIURL != "https://ghe.corp.com/api/v3" {
		t.Fatalf("ghe api url = %s", ghe.APIURL)
	}
	if ghe.Login != "enterprise" {
		t.Fatalf("ghe login = %s", ghe.Login)
	}
}

func TestParseAuthStatusHostsEmpty(t *testing.T) {
	hosts, err := parseAuthStatusHosts([]byte(`{"hosts": {}}`))
	if err != nil {
		t.Fatalf("parseAuthStatusHosts error: %v", err)
	}
	if len(hosts) != 0 {
		t.Fatalf("expected no hosts, got %d", len(hosts))
	}
}

func TestMapHostToAPIURL(t *testing.T) {
	if got := mapHostToAPIURL("github.com"); got != "https://api.github.com" {
		t.Fatalf("mapHostToAPIURL github.com = %s", got)
	}
	if got := mapHostToAPIURL("ghe.corp.com"); got != "https://ghe.corp.com/api/v3" {
		t.Fatalf("mapHostToAPIURL ghe.corp.com = %s", got)
	}
}
