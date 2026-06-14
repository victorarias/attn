package workflow

import (
	"context"
	"strings"
	"testing"
	"time"
)

func newTestEngine(opts ...func(*Config)) *Engine {
	cfg := Config{WatchdogTimeout: 5 * time.Second}
	for _, o := range opts {
		o(&cfg)
	}
	return New(cfg)
}

// runScript is a small helper that runs a script and returns the result.
func runScript(t *testing.T, eng *Engine, script string, args any) RunResult {
	t.Helper()
	res, _ := eng.Run(context.Background(), script, args)
	return res
}

func TestDeterminismBans(t *testing.T) {
	cases := []struct {
		name       string
		script     string
		wantErr    bool
		wantSubstr []string // substrings that must appear in the actionable error
	}{
		{
			name:       "Date.now throws",
			script:     `return Date.now();`,
			wantErr:    true,
			wantSubstr: []string{"Date.now()", "banned", "args"},
		},
		{
			name:       "Math.random throws",
			script:     `return Math.random();`,
			wantErr:    true,
			wantSubstr: []string{"Math.random()", "banned"},
		},
		{
			name:       "argless new Date throws",
			script:     `return new Date().getTime();`,
			wantErr:    true,
			wantSubstr: []string{"new Date()", "explicit argument"},
		},
		{
			// Date() as a plain function call (no `new`) ALWAYS reads the wall clock
			// and ignores its args per spec, so it must throw with or without args —
			// new.target distinguishes it from a deterministic new Date(arg).
			name:       "argless Date() function call throws",
			script:     `return Date();`,
			wantErr:    true,
			wantSubstr: []string{"new Date()", "explicit argument"},
		},
		{
			name:       "Date(arg) function call throws (args ignored, reads clock)",
			script:     `return Date(1234);`,
			wantErr:    true,
			wantSubstr: []string{"new Date()", "explicit argument"},
		},
		{
			name:    "new Date(arg) allowed",
			script:  `return new Date(1234).getTime();`,
			wantErr: false,
		},
		{
			name:    "new Date(string) allowed",
			script:  `return new Date("2020-01-01T00:00:00Z").getTime();`,
			wantErr: false,
		},
		{
			name:    "Date.parse allowed (deterministic)",
			script:  `return Date.parse("2020-01-01T00:00:00Z");`,
			wantErr: false,
		},
		{
			name:    "Math.max still works",
			script:  `return Math.max(1, 7, 3);`,
			wantErr: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := newTestEngine()
			res := runScript(t, eng, tc.script, nil)
			if tc.wantErr {
				if res.Status != StatusErrored {
					t.Fatalf("want errored, got status=%s value=%#v", res.Status, res.Value)
				}
				msg := res.Err.Error()
				for _, s := range tc.wantSubstr {
					if !strings.Contains(msg, s) {
						t.Errorf("error %q missing actionable substring %q", msg, s)
					}
				}
			} else {
				if res.Status != StatusCompleted {
					t.Fatalf("want completed, got status=%s err=%v", res.Status, res.Err)
				}
			}
		})
	}
}

func TestRealmDenyByDefault(t *testing.T) {
	// goja ships none of these; confirm the realm exposes only our surface.
	absent := []string{"fs", "require", "setTimeout", "setInterval", "process", "console", "global", "performance", "crypto", "module"}
	for _, name := range absent {
		t.Run(name+"_undefined", func(t *testing.T) {
			eng := newTestEngine()
			res := runScript(t, eng, "return typeof "+name+";", nil)
			if res.Status != StatusCompleted {
				t.Fatalf("status=%s err=%v", res.Status, res.Err)
			}
			if res.Value != "undefined" {
				t.Errorf("%s should be undefined in the realm, got %v", name, res.Value)
			}
		})
	}

	// The host surface IS present.
	present := []string{"agent", "parallel", "pipeline", "phase", "log", "args", "workflow"}
	for _, name := range present {
		t.Run(name+"_present", func(t *testing.T) {
			eng := newTestEngine()
			res := runScript(t, eng, "return typeof "+name+";", nil)
			if res.Status != StatusCompleted {
				t.Fatalf("status=%s err=%v", res.Status, res.Err)
			}
			if res.Value == "undefined" {
				t.Errorf("%s should be present in the realm", name)
			}
		})
	}
}

func TestWorkflowNotImplemented(t *testing.T) {
	eng := newTestEngine()
	res := runScript(t, eng, `return await workflow("child", {});`, nil)
	if res.Status != StatusErrored {
		t.Fatalf("want errored, got %s", res.Status)
	}
	if !strings.Contains(res.Err.Error(), "not implemented in E1") {
		t.Errorf("want not-implemented message, got %q", res.Err.Error())
	}
}

func TestMetaParse(t *testing.T) {
	cases := []struct {
		name        string
		script      string
		wantErr     bool
		errSubstr   string
		wantName    string
		wantDesc    string
		wantPhases  []string
		wantWhen    string
		wantModel   string
		wantNilMeta bool
	}{
		{
			name: "valid literal with export",
			script: `export const meta = {
				name: "demo",
				description: "a demo workflow",
				whenToUse: "for demos",
				phases: ["plan", "build"],
				model: "opus",
			};
			return args;`,
			wantName:   "demo",
			wantDesc:   "a demo workflow",
			wantWhen:   "for demos",
			wantPhases: []string{"plan", "build"},
			wantModel:  "opus",
		},
		{
			name:     "valid literal without export keyword",
			script:   `const meta = { name: "x", description: "y" }; return 1;`,
			wantName: "x",
			wantDesc: "y",
		},
		{
			name:        "no meta is allowed",
			script:      `return 42;`,
			wantNilMeta: true,
		},
		{
			name:      "computed key rejected",
			script:    `const meta = { ["na"+"me"]: "x" }; return 1;`,
			wantErr:   true,
			errSubstr: "computed",
		},
		{
			name:      "call-expression value rejected",
			script:    `const meta = { name: foo() }; return 1;`,
			wantErr:   true,
			errSubstr: "must be a string literal",
		},
		{
			name:      "non-first meta rejected",
			script:    `log("hi"); const meta = { name: "x" }; return 1;`,
			wantErr:   true,
			errSubstr: "first statement",
		},
		{
			name:      "non-object meta rejected",
			script:    `const meta = "not an object"; return 1;`,
			wantErr:   true,
			errSubstr: "object literal",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			eng := newTestEngine()
			res := runScript(t, eng, tc.script, "hello")
			if tc.wantErr {
				if res.Status != StatusErrored {
					t.Fatalf("want errored, got %s", res.Status)
				}
				if tc.errSubstr != "" && !strings.Contains(res.Err.Error(), tc.errSubstr) {
					t.Errorf("error %q missing %q", res.Err.Error(), tc.errSubstr)
				}
				return
			}
			if res.Status != StatusCompleted {
				t.Fatalf("want completed, got %s err=%v", res.Status, res.Err)
			}
			if tc.wantNilMeta {
				if res.Meta != nil {
					t.Errorf("want nil meta, got %#v", res.Meta)
				}
				return
			}
			if res.Meta == nil {
				t.Fatalf("meta not parsed")
			}
			if res.Meta.Name != tc.wantName {
				t.Errorf("name = %q, want %q", res.Meta.Name, tc.wantName)
			}
			if res.Meta.Description != tc.wantDesc {
				t.Errorf("description = %q, want %q", res.Meta.Description, tc.wantDesc)
			}
			if tc.wantWhen != "" && res.Meta.WhenToUse != tc.wantWhen {
				t.Errorf("whenToUse = %q, want %q", res.Meta.WhenToUse, tc.wantWhen)
			}
			if tc.wantModel != "" && res.Meta.Model != tc.wantModel {
				t.Errorf("model = %q, want %q", res.Meta.Model, tc.wantModel)
			}
			if len(tc.wantPhases) > 0 {
				if strings.Join(res.Meta.Phases, ",") != strings.Join(tc.wantPhases, ",") {
					t.Errorf("phases = %v, want %v", res.Meta.Phases, tc.wantPhases)
				}
			}
		})
	}
}
