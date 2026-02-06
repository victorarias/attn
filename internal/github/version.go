package github

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

var ghVersionRe = regexp.MustCompile(`(?m)^gh version ([0-9]+\.[0-9]+\.[0-9]+)`)

// CheckGHVersion runs `gh --version` and returns the parsed version string.
// Note: Daemon ensures PATH is set at startup via pathutil.EnsureGUIPath()
func CheckGHVersion() (string, error) {
	output, err := exec.Command("gh", "--version").Output()
	if err != nil {
		return "", fmt.Errorf("gh --version failed: %w", err)
	}

	version, err := parseGHVersionOutput(string(output))
	if err != nil {
		return "", err
	}
	return version, nil
}

// RequireGHVersion ensures the gh CLI meets the minimum version requirement.
func RequireGHVersion(minVersion string) error {
	version, err := CheckGHVersion()
	if err != nil {
		return err
	}
	cmp, err := compareVersions(version, minVersion)
	if err != nil {
		return err
	}
	if cmp < 0 {
		return fmt.Errorf("GitHub Enterprise support requires gh CLI v%s or later. Current version: %s. Please upgrade: brew upgrade gh", minVersion, version)
	}
	return nil
}

func parseGHVersionOutput(output string) (string, error) {
	matches := ghVersionRe.FindStringSubmatch(output)
	if len(matches) < 2 {
		return "", fmt.Errorf("unable to parse gh version from output")
	}
	return matches[1], nil
}

func compareVersions(a, b string) (int, error) {
	partsA, err := parseVersionParts(a)
	if err != nil {
		return 0, err
	}
	partsB, err := parseVersionParts(b)
	if err != nil {
		return 0, err
	}

	max := len(partsA)
	if len(partsB) > max {
		max = len(partsB)
	}
	for i := 0; i < max; i++ {
		va := 0
		vb := 0
		if i < len(partsA) {
			va = partsA[i]
		}
		if i < len(partsB) {
			vb = partsB[i]
		}
		if va < vb {
			return -1, nil
		}
		if va > vb {
			return 1, nil
		}
	}
	return 0, nil
}

func parseVersionParts(v string) ([]int, error) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if v == "" {
		return nil, fmt.Errorf("empty version")
	}
	base := strings.SplitN(v, "-", 2)[0]
	segments := strings.Split(base, ".")
	parts := make([]int, 0, len(segments))
	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			parts = append(parts, 0)
			continue
		}
		// Strip any trailing non-digit characters.
		digits := seg
		for i, r := range seg {
			if r < '0' || r > '9' {
				digits = seg[:i]
				break
			}
		}
		if digits == "" {
			return nil, fmt.Errorf("invalid version segment: %q", seg)
		}
		val, err := strconv.Atoi(digits)
		if err != nil {
			return nil, fmt.Errorf("invalid version segment: %q", seg)
		}
		parts = append(parts, val)
	}
	return parts, nil
}
