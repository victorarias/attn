package daemon

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/delegationprefs"
	"github.com/victorarias/attn/internal/prompts"
	"github.com/victorarias/attn/internal/protocol"
)

func (d *Daemon) delegationHarnesses() []protocol.DelegationHarness {
	result := []protocol.DelegationHarness{}
	for _, name := range agentdriver.List() {
		driver := agentdriver.Get(name)
		caps := agentdriver.EffectiveCapabilities(driver)
		if !caps.HasInitialPrompt {
			continue
		}
		result = append(result, protocol.DelegationHarness{ID: name, Name: driver.DisplayName(), Available: isAgentExecutableAvailable(driver.ResolveExecutable(d.store.GetSetting(executableSettingKey(name))), driver.DefaultExecutable()), ModelPin: caps.HasModelPin, EffortPin: caps.HasEffortPin, Discovery: supportsModelDiscovery(driver)})
	}
	for _, driver := range d.ensurePluginRegistry().registeredDrivers() {
		if !driver.Capabilities["initial_prompt"] {
			continue
		}
		plugin := d.ensurePluginRegistry().get(driver.PluginName)
		if plugin == nil {
			continue
		}
		health, _, _ := plugin.healthSnapshot()
		result = append(result, protocol.DelegationHarness{ID: driver.Agent, Name: driver.Agent, Available: health != agentdriver.HealthUnhealthy, ModelPin: driver.Capabilities["model_pin"], EffortPin: driver.Capabilities["effort_pin"], Discovery: driver.Capabilities["model_discovery"]})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (d *Daemon) handleDelegationPreferencesGet(client *wsClient, msg *protocol.DelegationPreferencesGetMessage) {
	result := protocol.DelegationPreferencesResultMessage{Event: protocol.EventDelegationPreferencesResult, RequestID: msg.RequestID}
	if strings.TrimSpace(msg.RequestID) == "" {
		result.Error = protocol.Ptr("missing request id")
		d.sendToClient(client, result)
		return
	}
	cfg, err := d.store.GetDelegationPreferences()
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else {
		result.Success = true
		result.Preferences = &cfg
		result.Harnesses = d.delegationHarnesses()
		result.Templates = prompts.DelegationRoleTemplates()
	}
	d.sendToClient(client, result)
}

func (d *Daemon) handleDelegationPreferencesSave(client *wsClient, msg *protocol.DelegationPreferencesSaveMessage) {
	result := protocol.DelegationPreferencesResultMessage{Event: protocol.EventDelegationPreferencesResult, RequestID: msg.RequestID}
	if strings.TrimSpace(msg.RequestID) == "" {
		result.Error = protocol.Ptr("missing request id")
		d.sendToClient(client, result)
		return
	}
	cfg, err := d.store.SaveDelegationPreferences(msg.Preferences)
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else {
		result.Success = true
		result.Preferences = &cfg
		result.Harnesses = d.delegationHarnesses()
		result.Templates = prompts.DelegationRoleTemplates()
		d.publishFact(FactDelegationPreferencesChanged, "preferences", nil)
	}
	d.sendToClient(client, result)
}

func (d *Daemon) projectDelegationPreferencesChanged() {
	d.projectSnapshot(protocol.EventDelegationPreferencesChanged, func() {
		cfg, err := d.store.GetDelegationPreferences()
		if err != nil {
			d.logf("delegation preferences snapshot: %v", err)
			return
		}
		d.broadcastMessage(protocol.DelegationPreferencesChangedMessage{Event: protocol.EventDelegationPreferencesChanged, Revision: cfg.Revision})
	})
}

func supportsModelDiscovery(driver agentdriver.Driver) bool {
	_, ok := driver.(agentdriver.ModelDiscoverer)
	return ok
}

func usesDelegationPreferences(msg *protocol.DelegateMessage) bool {
	return msg.Role != nil || msg.Choice != nil || protocol.Deref(msg.Fallback) || msg.PreferencesRevision != nil
}

func (d *Daemon) resolveDelegationPreferences(msg *protocol.DelegateMessage) (*delegationprefs.Resolved, error) {
	if !usesDelegationPreferences(msg) {
		if msg.Provider != nil {
			return nil, fmt.Errorf("--provider requires --role or --fallback; direct plugin delegation uses provider/model")
		}
		return nil, nil
	}
	cfg, err := d.store.GetDelegationPreferences()
	if err != nil {
		return nil, err
	}
	resolved, err := delegationprefs.Resolve(cfg, delegationprefs.Request{Role: protocol.Deref(msg.Role), Choice: protocol.Deref(msg.Choice), Fallback: protocol.Deref(msg.Fallback), Revision: msg.PreferencesRevision, Harness: msg.Agent, Provider: msg.Provider, Model: msg.Model, Effort: msg.Effort})
	if err != nil {
		return nil, err
	}
	s := resolved.Selection
	if _, err := d.resolveDelegationAgent("", &s.Harness); err != nil {
		return nil, err
	}
	if s.Provider != "" {
		if _, ok := d.ensurePluginRegistry().driver(s.Harness); !ok {
			return nil, fmt.Errorf("harness %q uses its configured provider; provider selection is supported by plugin harnesses", s.Harness)
		}
	}
	if err := d.validateDelegationModelEffort(s.Harness, s.Model, s.Effort); err != nil {
		return nil, err
	}
	if s.Model != "" {
		catalog, err := d.discoverDelegationModels(context.Background(), s.Harness)
		if err != nil {
			return nil, fmt.Errorf("validate selected model: %w", err)
		}
		for _, m := range catalog.Models {
			if m.ID != s.Model || m.Provider != s.Provider {
				continue
			}
			if m.Access == protocol.ModelCapabilitySupportUnsupported {
				return nil, fmt.Errorf("model %q is unavailable: %s", s.Model, m.Detail)
			}
			if s.Effort != "" && (m.EffortSupport == protocol.ModelCapabilitySupportUnsupported || (len(m.EffortLevels) > 0 && !slices.Contains(m.EffortLevels, s.Effort))) {
				return nil, fmt.Errorf("model %q does not support effort %q", s.Model, s.Effort)
			}
			break
		}
	}
	latest, err := d.store.GetDelegationPreferences()
	if err != nil {
		return nil, err
	}
	if !latest.Enabled || latest.Revision != cfg.Revision {
		return nil, delegationprefs.ErrConflict
	}
	return &resolved, nil
}
