package daemon

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	agentdriver "github.com/victorarias/attn/internal/agent"
	"github.com/victorarias/attn/internal/delegationprefs"
	"github.com/victorarias/attn/internal/protocol"
)

type delegationModelCatalog struct {
	Models []protocol.DelegationModel `json:"models"`
	Detail string                     `json:"detail"`
}

func (d *Daemon) discoverDelegationModels(ctx context.Context, harness string) (delegationModelCatalog, error) {
	cfg, err := d.store.GetDelegationPreferences()
	if err != nil {
		return delegationModelCatalog{}, err
	}
	if !cfg.Enabled {
		return delegationModelCatalog{}, fmt.Errorf("configure Settings > Delegation before discovering models")
	}
	if err := delegationprefs.ValidateSelection(delegationprefs.Selection{Harness: harness}, true); err != nil {
		return delegationModelCatalog{}, err
	}
	executable := d.store.GetSetting(executableSettingKey(harness))
	// Deduplicate only in-flight calls; the next refresh observes current account settings.
	value, err, _ := d.delegationModelQueries.Do(harness+"\x00"+executable, func() (any, error) {
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		go func() {
			select {
			case <-d.done:
				cancel()
			case <-ctx.Done():
			}
		}()
		result := delegationModelCatalog{Models: []protocol.DelegationModel{}}
		if plugin, ok := d.ensurePluginRegistry().driver(harness); ok {
			if !plugin.Capabilities["model_discovery"] {
				result.Detail = "This harness does not expose model discovery. Add an exact model or use its default."
				return result, nil
			}
			if err := d.callPlugin(ctx, plugin.PluginName, "driver.models", map[string]string{"agent": harness}, &result); err != nil {
				return nil, err
			}
		} else {
			driver := agentdriver.Get(harness)
			if driver == nil {
				return nil, fmt.Errorf("harness %q is not available", harness)
			}
			discoverer, ok := driver.(agentdriver.ModelDiscoverer)
			if !ok {
				result.Detail = "This harness does not expose model discovery. Add an exact model or use its default."
				return result, nil
			}
			cwd, err := os.MkdirTemp("", "attn-model-discovery-")
			if err != nil {
				return nil, err
			}
			defer os.RemoveAll(cwd)
			result.Models, err = discoverer.DiscoverDelegationModels(ctx, executable, cwd)
			if err != nil {
				return nil, err
			}
			result.Detail = "Models reported by this harness. Catalog membership does not confirm account access."
		}
		if result.Models == nil {
			result.Models = []protocol.DelegationModel{}
		}
		for i := range result.Models {
			m := &result.Models[i]
			if m.Harness != harness || strings.TrimSpace(m.ID) == "" {
				return nil, fmt.Errorf("model discovery returned an invalid identity")
			}
			if err := delegationprefs.ValidateSelection(delegationprefs.Selection{Harness: harness, Provider: m.Provider, Model: m.ID}, true); err != nil {
				return nil, err
			}
			if m.EffortSupport != "supported" && m.EffortSupport != "unsupported" {
				m.EffortSupport = protocol.ModelCapabilitySupportUnknown
			}
			if m.Access != "supported" && m.Access != "unsupported" {
				m.Access = protocol.ModelCapabilitySupportUnknown
			}
			if m.EffortLevels == nil {
				m.EffortLevels = []string{}
			}
		}
		sort.SliceStable(result.Models, func(i, j int) bool {
			a, b := result.Models[i], result.Models[j]
			return a.Provider+"/"+a.ID < b.Provider+"/"+b.ID
		})
		return result, nil
	})
	if err != nil {
		return delegationModelCatalog{}, err
	}
	// A settings edit while the query ran must not reveal a disabled configuration.
	latest, err := d.store.GetDelegationPreferences()
	if err != nil {
		return delegationModelCatalog{}, err
	}
	if !latest.Enabled {
		return delegationModelCatalog{}, fmt.Errorf("delegation model discovery is no longer available")
	}
	return value.(delegationModelCatalog), nil
}

func (d *Daemon) handleDelegationModels(client *wsClient, msg *protocol.DelegationModelsMessage) {
	result := protocol.DelegationModelsResultMessage{Event: protocol.EventDelegationModelsResult, RequestID: msg.RequestID, Models: []protocol.DelegationModel{}}
	if strings.TrimSpace(msg.RequestID) == "" {
		result.Error = protocol.Ptr("missing request id")
		d.sendToClient(client, result)
		return
	}
	catalog, err := d.discoverDelegationModels(context.Background(), strings.TrimSpace(msg.Harness))
	if err != nil {
		result.Error = protocol.Ptr(err.Error())
	} else {
		result.Success = true
		result.Models = catalog.Models
		result.Detail = catalog.Detail
	}
	d.sendToClient(client, result)
}
