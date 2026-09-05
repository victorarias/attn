package agent

import (
	"context"

	"github.com/victorarias/attn/internal/protocol"
)

type ModelDiscoverer interface {
	DiscoverDelegationModels(context.Context, string, string) ([]protocol.DelegationModel, error)
}

func (c *Claude) DiscoverDelegationModels(ctx context.Context, executable, cwd string) ([]protocol.DelegationModel, error) {
	catalog, err := c.DiscoverModels(ctx, executable, cwd)
	if err != nil {
		return nil, err
	}
	result := make([]protocol.DelegationModel, 0, len(catalog))
	for _, m := range catalog {
		support := protocol.ModelCapabilitySupportUnknown
		if m.SupportsEffort != nil {
			if *m.SupportsEffort {
				support = protocol.ModelCapabilitySupportSupported
			} else {
				support = protocol.ModelCapabilitySupportUnsupported
			}
		}
		if len(m.SupportedEffortLevels) > 0 {
			support = protocol.ModelCapabilitySupportSupported
		}
		result = append(result, protocol.DelegationModel{Harness: "claude", ID: m.Value, Name: m.DisplayName, Description: m.Description, EffortSupport: support, EffortLevels: append([]string{}, m.SupportedEffortLevels...), Access: protocol.ModelCapabilitySupportUnknown})
	}
	return result, nil
}
