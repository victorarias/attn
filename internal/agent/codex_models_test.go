package agent

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/protocol"
)

func TestCodexModelDiscoveryPaginatesAndKeepsEffortMetadata(t *testing.T) {
	output := `{"method":"notice"}
{"id":1,"result":{}}
{"id":2,"result":{"data":[{"id":"alias","model":"configured-model","displayName":"Configured model","supportedReasoningEfforts":[{"reasoningEffort":"high"},{"reasoningEffort":"max"}]}],"nextCursor":"page2"}}
{"id":3,"result":{"data":[{"id":"unknown-effort"},{"id":"no-effort","supportedReasoningEfforts":[]}],"nextCursor":null}}
`
	var input bytes.Buffer
	models, err := readCodexModels(strings.NewReader(output), &input)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 3 || models[0].ID != "configured-model" || len(models[0].EffortLevels) != 2 || models[1].EffortSupport != protocol.ModelCapabilitySupportUnknown || models[2].EffortSupport != protocol.ModelCapabilitySupportUnsupported {
		t.Fatalf("models: %+v", models)
	}
	decoder := json.NewDecoder(&input)
	methods := []string{}
	for decoder.More() {
		var request struct {
			Method string `json:"method"`
		}
		if err := decoder.Decode(&request); err != nil {
			t.Fatal(err)
		}
		methods = append(methods, request.Method)
	}
	if strings.Join(methods, ",") != "initialize,initialized,model/list,model/list" {
		t.Fatalf("unexpected calls: %v", methods)
	}
}

func TestCodexModelDiscoveryRefusesBrokenResponses(t *testing.T) {
	for _, output := range []string{
		`{"id":1,"error":{"message":"denied"}}`,
		`{"id":1,"result":{}} {"id":2,"result":{}}`,
		`{"id":1,"result":{}} {"id":2,"result":{"data":[{}]}}`,
		`{"id":1,"result":{}} {"id":2,"result":{"data":[],"nextCursor":"a"}} {"id":3,"result":{"data":[],"nextCursor":"a"}}`,
	} {
		var input bytes.Buffer
		if _, err := readCodexModels(strings.NewReader(output), &input); err == nil {
			t.Fatalf("accepted %s", output)
		}
	}
}
