package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/victorarias/attn/internal/enrollment"
)

const (
	testDaemonID = "d-cccccccccccccccccccccccccccccccc"
	testHomeID   = "d-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func TestEnrollmentStatus_HomeSaysItOwnsTheGarden(t *testing.T) {
	var out bytes.Buffer
	writeEnrollmentStatus(&out, enrollment.Status{DaemonID: testDaemonID, HomeDaemonID: testDaemonID})

	rendered := out.String()
	for _, want := range []string{testDaemonID, "home", "this daemon owns them"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("status does not say %q:\n%s", want, rendered)
		}
	}
}

func TestEnrollmentStatus_OutpostShowsTheRefusalItWouldGive(t *testing.T) {
	var out bytes.Buffer
	writeEnrollmentStatus(&out, enrollment.Status{DaemonID: testDaemonID, HomeDaemonID: testHomeID})

	rendered := out.String()
	// The point of showing the fence's own words here is that whoever runs a
	// garden command next has already read what it will say, and how to undo it.
	for _, want := range []string{
		"outpost of " + testHomeID,
		"refused here",
		testDaemonID,
		"attn enrollment leave",
		enrollment.PlanPath,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("status does not say %q:\n%s", want, rendered)
		}
	}
}

func TestEmitEnrollmentResult_RefusalGoesToStderrAndJSONToStdout(t *testing.T) {
	result := enrollment.Result{
		Status:       "refused",
		DaemonID:     testDaemonID,
		HomeDaemonID: testHomeID,
		Message:      "already an outpost of " + testHomeID,
	}

	var out, errOut bytes.Buffer
	emitEnrollmentResult(&out, &errOut, result, true)

	if !strings.Contains(errOut.String(), result.Message) {
		t.Fatalf("refusal wording is not on stderr, where the hub reads it:\n%s", errOut.String())
	}
	var decoded enrollment.Result
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("decode stdout %q: %v", out.String(), err)
	}
	if decoded.Status != "refused" || decoded.HomeDaemonID != testHomeID {
		t.Fatalf("stdout JSON = %+v, want the refusal with its current home", decoded)
	}
}

func TestEmitEnrollmentResult_HumanSuccessGoesToStdoutOnce(t *testing.T) {
	result := enrollment.Result{
		Status:       "enrolled",
		DaemonID:     testDaemonID,
		HomeDaemonID: testHomeID,
		Message:      "enrolled as an outpost of " + testHomeID,
	}

	var out, errOut bytes.Buffer
	emitEnrollmentResult(&out, &errOut, result, false)

	if strings.TrimSpace(out.String()) != result.Message {
		t.Fatalf("stdout = %q, want %q", out.String(), result.Message)
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want nothing on success", errOut.String())
	}
}

func TestEnrollmentHelpNamesTheWayOut(t *testing.T) {
	var out bytes.Buffer
	writeEnrollmentHelp(&out)
	for _, want := range []string{"status", "enroll --home", "leave"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("help does not name %q:\n%s", want, out.String())
		}
	}
}
