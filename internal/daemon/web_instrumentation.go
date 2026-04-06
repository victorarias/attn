package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

const webInstrumentationMaxBodyBytes = 64 << 10

func (d *Daemon) handleWebInstrumentation(w http.ResponseWriter, r *http.Request) {
	setNoStoreHeaders(w.Header())

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, webInstrumentationMaxBodyBytes))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "instrumentation payload too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "failed to read instrumentation payload", http.StatusBadRequest)
		return
	}

	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		http.Error(w, "instrumentation payload required", http.StatusBadRequest)
		return
	}
	if !json.Valid(body) {
		http.Error(w, "instrumentation payload must be valid json", http.StatusBadRequest)
		return
	}

	var compact bytes.Buffer
	if err := json.Compact(&compact, body); err != nil {
		http.Error(w, "failed to compact instrumentation payload", http.StatusBadRequest)
		return
	}

	d.logf("web instrumentation: %s", compact.String())
	w.WriteHeader(http.StatusNoContent)
}
