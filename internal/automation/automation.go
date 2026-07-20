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

	"github.com/victorarias/attn/internal/launchcontract"
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
	Type         string               `yaml:"type" json:"type"`
	Repositories RepositoryFilterSpec `yaml:"repositories,omitempty" json:"repositories,omitempty"`
	Schedule     ScheduleSpec         `yaml:"schedule,omitempty" json:"schedule,omitempty"`
}
type ScheduleSpec struct {
	Cron     string `yaml:"cron,omitempty" json:"cron,omitempty"`
	TimeZone string `yaml:"time_zone,omitempty" json:"time_zone,omitempty"`
}
type RepositoryFilterSpec struct {
	Mode    string   `yaml:"mode,omitempty" json:"mode,omitempty"`
	Include []string `yaml:"include,omitempty" json:"include,omitempty"`
	Exclude []string `yaml:"exclude,omitempty" json:"exclude,omitempty"`
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
type EffectiveLaunch = launchcontract.UnattendedLaunchSpec
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
	switch s.Trigger.Type {
	case "manual":
		if s.Trigger.Repositories.Mode != "" || len(s.Trigger.Repositories.Include) > 0 || len(s.Trigger.Repositories.Exclude) > 0 {
			return errors.New("manual trigger cannot configure repositories")
		}
		if s.Trigger.Schedule != (ScheduleSpec{}) {
			return errors.New("manual trigger cannot configure schedule")
		}
	case "github_review_requested":
		if err := validateRepositoryFilter(&s.Trigger.Repositories); err != nil {
			return err
		}
		if s.Trigger.Schedule != (ScheduleSpec{}) {
			return errors.New("github_review_requested trigger cannot configure schedule")
		}
	case "scheduled":
		if s.Trigger.Repositories.Mode != "" || len(s.Trigger.Repositories.Include) > 0 || len(s.Trigger.Repositories.Exclude) > 0 {
			return errors.New("scheduled trigger cannot configure repositories")
		}
		if _, err := CompileSchedule(s.Trigger.Schedule); err != nil {
			return err
		}
	default:
		return errors.New("trigger.type must be manual, github_review_requested, or scheduled")
	}
	if strings.TrimSpace(s.Prompt) == "" {
		return errors.New("prompt is required")
	}
	s.Launch.Driver = strings.TrimSpace(strings.ToLower(s.Launch.Driver))
	if s.Launch.Driver == "" {
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
	if s.Policy.Overlap != "" && s.Policy.Overlap != "coalesce" {
		return errors.New("policy.overlap must be coalesce")
	}
	switch s.Trigger.Type {
	case "github_review_requested":
		if s.Location.Type != "repository_worktree" {
			return errors.New("github_review_requested trigger requires repository_worktree location")
		}
		if s.Policy.Continuity != "per_subject" {
			return errors.New("github_review_requested trigger requires policy.continuity per_subject")
		}
		if s.Policy.CatchUp != "latest" {
			return errors.New("github_review_requested trigger requires policy.catch_up latest")
		}
	case "scheduled":
		if s.Location.Type != "directory" {
			return errors.New("scheduled trigger requires directory location")
		}
		if s.Policy.Continuity != "fresh" && s.Policy.Continuity != "singleton" {
			return errors.New("scheduled trigger requires policy.continuity fresh or singleton")
		}
		if s.Policy.CatchUp != "skip" && s.Policy.CatchUp != "latest" {
			return errors.New("scheduled trigger requires policy.catch_up skip or latest")
		}
	default:
		if s.Policy.Continuity != "fresh" {
			return errors.New("manual trigger supports only policy.continuity fresh")
		}
		if s.Policy.CatchUp != "" {
			return errors.New("manual trigger cannot configure policy.catch_up")
		}
	}
	return nil
}

func validateRepositoryFilter(filter *RepositoryFilterSpec) error {
	if filter.Mode == "" {
		filter.Mode = "all_accessible"
	}
	if filter.Mode != "all_accessible" {
		return errors.New("trigger.repositories.mode must be all_accessible")
	}
	canonicalize := func(field string, values []string) ([]string, error) {
		seen := make(map[string]bool, len(values))
		out := make([]string, 0, len(values))
		for _, value := range values {
			identity, err := CanonicalRepositoryIdentity(value)
			if err != nil {
				return nil, fmt.Errorf("trigger.repositories.%s entry %q: %w", field, value, err)
			}
			if seen[identity] {
				return nil, fmt.Errorf("duplicate trigger.repositories.%s entry %q", field, identity)
			}
			seen[identity] = true
			out = append(out, identity)
		}
		return out, nil
	}
	var err error
	if filter.Include, err = canonicalize("include", filter.Include); err != nil {
		return err
	}
	if filter.Exclude, err = canonicalize("exclude", filter.Exclude); err != nil {
		return err
	}
	excluded := make(map[string]bool, len(filter.Exclude))
	for _, identity := range filter.Exclude {
		excluded[identity] = true
	}
	for _, identity := range filter.Include {
		if excluded[identity] {
			return fmt.Errorf("repository %q cannot be both included and excluded", identity)
		}
	}
	return nil
}

func (filter RepositoryFilterSpec) Matches(identity string) bool {
	canonical, err := CanonicalRepositoryIdentity(identity)
	if err != nil {
		return false
	}
	for _, excluded := range filter.Exclude {
		if excluded == canonical {
			return false
		}
	}
	if len(filter.Include) == 0 {
		return true
	}
	for _, included := range filter.Include {
		if included == canonical {
			return true
		}
	}
	return false
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
	mode := launchcontract.ApprovalAuto
	switch strings.ToLower(spec.Launch.Driver) {
	case "codex":
		mode = launchcontract.ApprovalAutoReview
	}
	launch := EffectiveLaunch{
		Agent:               spec.Launch.Driver,
		Model:               spec.Launch.Model,
		Effort:              spec.Launch.Effort,
		Executable:          spec.Launch.Executable,
		ApprovalProductMode: launchcontract.ApprovalAuto,
		ApprovalDriverMode:  mode,
		DirectoryTrust:      launchcontract.TrustConfiguredDirectory,
		Recovery:            launchcontract.RecoveryAdoptOrRestartFresh,
	}
	if err := launch.Validate(); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{APIVersion: APIVersion, DefinitionRevision: revision, Prompt: spec.Prompt, Launch: launch, Location: spec.Location, Policy: spec.Policy}, nil
}

type DeliveryIDs struct{ TicketID, SessionID, WorkspaceID, PaneID string }
type WorkRequest struct {
	RunID, DefinitionID, SubjectKey, ContinuityKey, Provider string
	Prompt                                                   string
	Context                                                  json.RawMessage
	Launch                                                   EffectiveLaunch
	Location                                                 LocationSpec
	IDs                                                      DeliveryIDs
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
