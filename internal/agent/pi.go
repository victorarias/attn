package agent

import "os/exec"

// Pi implements Driver for the pi coding agent.
// This initial driver is intentionally minimal so pi can be integrated without
// transcript/hook/classifier coupling.
type Pi struct{}

var _ Driver = (*Pi)(nil)

func init() {
	Register(&Pi{})
}

func (p *Pi) Name() string              { return "pi" }
func (p *Pi) DisplayName() string       { return "Pi" }
func (p *Pi) DefaultExecutable() string { return "pi" }
func (p *Pi) ExecutableEnvVar() string  { return "ATTN_PI_EXECUTABLE" }

func (p *Pi) ResolveExecutable(configured string) string {
	return resolveExec(p.ExecutableEnvVar(), configured, p.DefaultExecutable())
}

func (p *Pi) Capabilities() Capabilities {
	return Capabilities{
		HasHooks:             false,
		HasTranscript:        false,
		HasTranscriptWatcher: false,
		HasClassifier:        false,
		HasStateDetector:     false,
		HasResume:            false,
		HasFork:              false,
	}
}

func (p *Pi) BuildCommand(opts SpawnOpts) *exec.Cmd {
	args := append([]string(nil), opts.AgentArgs...)
	return exec.Command(opts.Executable, args...)
}

func (p *Pi) BuildEnv(opts SpawnOpts) []string {
	if opts.Executable != "" && opts.Executable != p.DefaultExecutable() {
		return []string{p.ExecutableEnvVar() + "=" + opts.Executable}
	}
	return nil
}
