package github

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// HostInfo represents an authenticated GitHub host.
type HostInfo struct {
	Host   string // "github.com" or "ghe.corp.com"
	APIURL string // "https://api.github.com" or "https://ghe.corp.com/api/v3"
	Login  string // username
	Active bool
}

// DiscoverHosts discovers authenticated hosts from `gh auth status --json hosts`.
func DiscoverHosts() ([]HostInfo, error) {
	output, err := ghAuthStatusHosts()
	if err != nil {
		return nil, err
	}
	return parseAuthStatusHosts(output)
}

// GetTokenForHost fetches a token for the given host using `gh auth token -h`.
// Note: Daemon ensures PATH is set at startup via pathutil.EnsureGUIPath()
func GetTokenForHost(host string) (string, error) {
	cmd := exec.Command("gh", "auth", "token", "-h", host)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gh auth token -h %s failed: %w", host, err)
	}
	return strings.TrimSpace(string(output)), nil
}

func ghAuthStatusHosts() ([]byte, error) {
	cmd := exec.Command("gh", "auth", "status", "--json", "hosts")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh auth status --json hosts failed: %w", err)
	}
	return output, nil
}

func parseAuthStatusHosts(output []byte) ([]HostInfo, error) {
	var payload struct {
		Hosts map[string][]map[string]interface{} `json:"hosts"`
	}
	if err := json.Unmarshal(output, &payload); err != nil {
		return nil, fmt.Errorf("parse gh auth status output: %w", err)
	}

	if len(payload.Hosts) == 0 {
		return nil, nil
	}

	var results []HostInfo
	for host, entries := range payload.Hosts {
		var selected *HostInfo
		for _, entry := range entries {
			state, _ := entry["state"].(string)
			if state != "success" {
				continue
			}
			active := false
			if rawActive, ok := entry["active"].(bool); ok {
				active = rawActive
			}

			login := ""
			if user, ok := entry["user"].(string); ok {
				login = user
			} else if loginVal, ok := entry["login"].(string); ok {
				login = loginVal
			} else if userObj, ok := entry["user"].(map[string]interface{}); ok {
				if loginVal, ok := userObj["login"].(string); ok {
					login = loginVal
				}
			}

			info := &HostInfo{
				Host:   host,
				APIURL: mapHostToAPIURL(host),
				Login:  login,
				Active: active,
			}

			if selected == nil || (active && !selected.Active) {
				selected = info
			}
		}
		if selected != nil {
			results = append(results, *selected)
		}
	}

	return results, nil
}

func mapHostToAPIURL(host string) string {
	if host == "github.com" {
		return "https://api.github.com"
	}
	return "https://" + host + "/api/v3"
}
