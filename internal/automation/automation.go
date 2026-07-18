package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	if s.Location.Type != "directory" {
		return errors.New("Slice 1 supports only location.type directory")
	}
	if !strings.HasPrefix(s.Location.Path, "/") {
		return errors.New("location.path must be absolute")
	}
	info, err := os.Stat(s.Location.Path)
	if err != nil {
		return fmt.Errorf("location.path: %w", err)
	}
	if !info.IsDir() {
		return errors.New("location.path must be a directory")
	}
	canonicalPath, err := filepath.EvalSymlinks(s.Location.Path)
	if err != nil {
		return fmt.Errorf("canonicalize location.path: %w", err)
	}
	s.Location.Path = filepath.Clean(canonicalPath)
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
type DeliveryResult struct{ TicketID, SessionID, WorkspaceID, Directory, Revision, Mode string }
type Deliverer interface {
	Deliver(context.Context, WorkRequest) (DeliveryResult, error)
}

type Clock interface{ Now() time.Time }
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }
