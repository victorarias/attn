package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const APIVersion = "attn.dev/automations/v1alpha1"

type DefinitionSpec struct {
	APIVersion string       `yaml:"api_version" json:"api_version"`
	ID         string       `yaml:"id" json:"id"`
	Name       string       `yaml:"name" json:"name"`
	Enabled    bool         `yaml:"enabled" json:"enabled"`
	Trigger    TriggerSpec  `yaml:"trigger" json:"trigger"`
	Prompt     string       `yaml:"prompt" json:"prompt"`
	Launch     LaunchSpec   `yaml:"launch" json:"launch"`
	Location   LocationSpec `yaml:"location" json:"location"`
	Policy     PolicySpec   `yaml:"policy" json:"policy"`
}
type TriggerSpec struct {
	Type string `yaml:"type" json:"type"`
}
type LaunchSpec struct {
	Driver     string `yaml:"driver" json:"driver"`
	Model      string `yaml:"model,omitempty" json:"model,omitempty"`
	Effort     string `yaml:"effort,omitempty" json:"effort,omitempty"`
	Executable string `yaml:"executable,omitempty" json:"executable,omitempty"`
}
type LocationSpec struct {
	Type              string            `yaml:"type" json:"type"`
	Path              string            `yaml:"path,omitempty" json:"path,omitempty"`
	RepositorySources RepositorySources `yaml:"repository_sources,omitempty" json:"repository_sources,omitempty"`
}
type RepositorySources struct {
	Default   RepositorySource            `yaml:"default" json:"default"`
	Overrides map[string]RepositorySource `yaml:"overrides,omitempty" json:"overrides,omitempty"`
}
type RepositorySource struct {
	Type string `yaml:"type" json:"type"`
	Path string `yaml:"path,omitempty" json:"path,omitempty"`
}
type PolicySpec struct {
	Continuity string `yaml:"continuity" json:"continuity"`
	CatchUp    string `yaml:"catch_up,omitempty" json:"catch_up,omitempty"`
	Overlap    string `yaml:"overlap,omitempty" json:"overlap,omitempty"`
}
type EffectiveLaunch struct {
	LaunchSpec
	ApprovalProductMode string `json:"approval_product_mode"`
	ApprovalDriverMode  string `json:"approval_driver_mode"`
}
type Snapshot struct {
	APIVersion         string          `json:"api_version"`
	DefinitionRevision int             `json:"definition_revision,omitempty"`
	Prompt             string          `json:"prompt"`
	Launch             EffectiveLaunch `json:"launch"`
	Location           LocationSpec    `json:"location"`
	Policy             PolicySpec      `json:"policy"`
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func ParseDefinitionYAML(data []byte) (DefinitionSpec, []byte, error) {
	var spec DefinitionSpec
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&spec); err != nil {
		return spec, nil, fmt.Errorf("parse definition: %w", err)
	}
	if err := ValidateDefinition(&spec); err != nil {
		return spec, nil, err
	}
	canonical, err := json.Marshal(spec)
	return spec, canonical, err
}

func ValidateDefinition(s *DefinitionSpec) error {
	if s == nil {
		return errors.New("definition is required")
	}
	if s.APIVersion != APIVersion {
		return fmt.Errorf("api_version must be %q", APIVersion)
	}
	if !idPattern.MatchString(s.ID) {
		return errors.New("id must be a lowercase slug")
	}
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("name is required")
	}
	if s.Trigger.Type != "manual" {
		return errors.New("Slice 1 supports only trigger.type manual")
	}
	if strings.TrimSpace(s.Prompt) == "" {
		return errors.New("prompt is required")
	}
	if strings.TrimSpace(s.Launch.Driver) == "" {
		return errors.New("launch.driver is required")
	}
	switch s.Location.Type {
	case "directory":
		if err := canonicalizeDirectory(&s.Location.Path, "location.path"); err != nil {
			return err
		}
		if s.Location.RepositorySources.Default.Type != "" || len(s.Location.RepositorySources.Overrides) > 0 {
			return errors.New("directory location cannot configure repository_sources")
		}
	case "repository_worktree":
		if strings.TrimSpace(s.Location.Path) != "" {
			return errors.New("repository_worktree location cannot configure path")
		}
		if s.Location.RepositorySources.Default.Type != "managed_cache" {
			return errors.New("repository_sources.default.type must be managed_cache")
		}
		if strings.TrimSpace(s.Location.RepositorySources.Default.Path) != "" {
			return errors.New("managed_cache source cannot configure path")
		}
		canonicalOverrides := make(map[string]RepositorySource, len(s.Location.RepositorySources.Overrides))
		for identity, source := range s.Location.RepositorySources.Overrides {
			canonicalIdentity, err := CanonicalRepositoryIdentity(identity)
			if err != nil {
				return fmt.Errorf("repository_sources.overrides[%q]: %w", identity, err)
			}
			if source.Type != "local_clone" {
				return fmt.Errorf("repository_sources.overrides[%q].type must be local_clone", identity)
			}
			if err := canonicalizeDirectory(&source.Path, fmt.Sprintf("repository_sources.overrides[%q].path", identity)); err != nil {
				return err
			}
			if _, exists := canonicalOverrides[canonicalIdentity]; exists {
				return fmt.Errorf("duplicate repository override %q", canonicalIdentity)
			}
			canonicalOverrides[canonicalIdentity] = source
		}
		s.Location.RepositorySources.Overrides = canonicalOverrides
	default:
		return errors.New("location.type must be directory or repository_worktree")
	}
	if s.Policy.Continuity == "" {
		s.Policy.Continuity = "fresh"
	}
	if s.Policy.Continuity != "fresh" {
		return errors.New("Slice 1 supports only policy.continuity fresh")
	}
	if s.Policy.Overlap != "" && s.Policy.Overlap != "coalesce" {
		return errors.New("policy.overlap must be coalesce")
	}
	return nil
}

func canonicalizeDirectory(path *string, field string) error {
	if path == nil || !filepath.IsAbs(*path) {
		return fmt.Errorf("%s must be absolute", field)
	}
	info, err := os.Stat(*path)
	if err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s must be a directory", field)
	}
	canonicalPath, err := filepath.EvalSymlinks(*path)
	if err != nil {
		return fmt.Errorf("canonicalize %s: %w", field, err)
	}
	*path = filepath.Clean(canonicalPath)
	return nil
}

var repositoryHostPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$`)
var repositoryComponentPattern = regexp.MustCompile(`^[a-z0-9_.-]+$`)

func CanonicalRepositoryIdentity(identity string) (string, error) {
	identity = strings.ToLower(strings.TrimSpace(identity))
	parts := strings.Split(identity, "/")
	unsafeComponent := func(value string) bool {
		return value == "." || value == ".." || !repositoryComponentPattern.MatchString(value)
	}
	if len(parts) != 3 || !repositoryHostPattern.MatchString(parts[0]) || unsafeComponent(parts[1]) || unsafeComponent(parts[2]) {
		return "", errors.New("repository identity must be host/owner/repository")
	}
	return identity, nil
}

type PullRequestInput struct {
	Provider       string `json:"provider"`
	Host           string `json:"host"`
	Owner          string `json:"owner"`
	Repository     string `json:"repository"`
	Number         int    `json:"number"`
	URL            string `json:"url"`
	Title          string `json:"title,omitempty"`
	Body           string `json:"body,omitempty"`
	Author         string `json:"author,omitempty"`
	Draft          bool   `json:"draft"`
	State          string `json:"state"`
	HeadSHA        string `json:"head_sha"`
	HeadRef        string `json:"head_ref,omitempty"`
	HeadRepository string `json:"head_repository,omitempty"`
	BaseSHA        string `json:"base_sha,omitempty"`
	BaseRef        string `json:"base_ref,omitempty"`
}

func ParsePullRequestInput(raw json.RawMessage) (PullRequestInput, error) {
	var input PullRequestInput
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil {
		return input, fmt.Errorf("parse pull request input: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return input, errors.New("parse pull request input: expected one JSON object")
	}
	if input.Provider != "github" {
		return input, errors.New("pull request input provider must be github")
	}
	identity, err := CanonicalRepositoryIdentity(input.Host + "/" + input.Owner + "/" + input.Repository)
	if err != nil {
		return input, fmt.Errorf("pull request repository: %w", err)
	}
	parts := strings.Split(identity, "/")
	input.Host, input.Owner, input.Repository = parts[0], parts[1], parts[2]
	if input.Number <= 0 {
		return input, errors.New("pull request number must be positive")
	}
	if strings.TrimSpace(input.URL) == "" {
		return input, errors.New("pull request url is required")
	}
	urlHost, urlOwner, urlRepository, urlNumber, err := ParsePullRequestURL(input.URL)
	if err != nil {
		return input, err
	}
	if urlHost != input.Host || urlOwner != input.Owner || urlRepository != input.Repository || urlNumber != input.Number {
		return input, errors.New("pull request url does not match repository identity and number")
	}
	if matched, _ := regexp.MatchString(`^[0-9a-fA-F]{40}$`, input.HeadSHA); !matched {
		return input, errors.New("pull request head_sha must be a full commit SHA")
	}
	input.HeadSHA = strings.ToLower(input.HeadSHA)
	return input, nil
}

func (input PullRequestInput) RepositoryIdentity() string {
	return input.Host + "/" + input.Owner + "/" + input.Repository
}

func (input PullRequestInput) SubjectKey() string {
	return input.RepositoryIdentity() + "#" + strconv.Itoa(input.Number)
}

func ParsePullRequestURL(raw string) (host, owner, repository string, number int, err error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", "", "", 0, fmt.Errorf("parse pull request url: %w", err)
	}
	if parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Hostname() == "" || parsed.Port() != "" {
		return "", "", "", 0, errors.New("pull request url must be an https repository pull URL")
	}
	parts := strings.Split(strings.Trim(strings.TrimSpace(parsed.EscapedPath()), "/"), "/")
	if len(parts) != 4 || parts[2] != "pull" {
		return "", "", "", 0, errors.New("pull request url path must be /owner/repository/pull/number")
	}
	for i := range parts {
		decoded, decodeErr := url.PathUnescape(parts[i])
		if decodeErr != nil || decoded != parts[i] || strings.TrimSpace(decoded) == "" {
			return "", "", "", 0, errors.New("pull request url contains an invalid path component")
		}
	}
	number, err = strconv.Atoi(parts[3])
	if err != nil || number <= 0 {
		return "", "", "", 0, errors.New("pull request url number must be positive")
	}
	identity, err := CanonicalRepositoryIdentity(parsed.Hostname() + "/" + parts[0] + "/" + parts[1])
	if err != nil {
		return "", "", "", 0, err
	}
	identityParts := strings.Split(identity, "/")
	return identityParts[0], identityParts[1], identityParts[2], number, nil
}

func Effective(spec DefinitionSpec, revision int) (Snapshot, error) {
	mode := ""
	switch strings.ToLower(spec.Launch.Driver) {
	case "codex":
		mode = "auto_review"
	case "claude":
		mode = "auto"
	default:
		mode = "auto"
	}
	return Snapshot{APIVersion: APIVersion, DefinitionRevision: revision, Prompt: spec.Prompt, Launch: EffectiveLaunch{LaunchSpec: spec.Launch, ApprovalProductMode: "auto", ApprovalDriverMode: mode}, Location: spec.Location, Policy: spec.Policy}, nil
}

type DeliveryIDs struct{ TicketID, SessionID, WorkspaceID, PaneID string }
type WorkRequest struct {
	RunID, DefinitionID, SubjectKey, ContinuityKey string
	Prompt                                         string
	Context                                        json.RawMessage
	Launch                                         EffectiveLaunch
	Location                                       LocationSpec
	IDs                                            DeliveryIDs
}
type PreparedLocation struct {
	Directory string          `json:"directory"`
	Revision  string          `json:"revision,omitempty"`
	Resolved  json.RawMessage `json:"resolved"`
}
type ResolvedLocation struct {
	Type             string           `json:"type"`
	Path             string           `json:"path,omitempty"`
	Repository       string           `json:"repository,omitempty"`
	ConfiguredSource RepositorySource `json:"configured_source,omitempty"`
	MainRepository   string           `json:"main_repository,omitempty"`
	Worktree         string           `json:"worktree,omitempty"`
	Revision         string           `json:"revision,omitempty"`
	ProviderRef      string           `json:"provider_ref,omitempty"`
}
type DeliveryResult struct {
	TicketID, SessionID, WorkspaceID, Directory, Revision, Mode string
	Resolved                                                    json.RawMessage
}
type Deliverer interface {
	Deliver(context.Context, WorkRequest) (DeliveryResult, error)
}

type Clock interface{ Now() time.Time }
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }
