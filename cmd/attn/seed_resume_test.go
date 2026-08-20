package main

import "testing"

func TestSeedResumeIdentityFlagsParseAroundTheSeedID(t *testing.T) {
	f := newSeedFlags("set-resume")
	positionals := f.parse("set-resume", []string{
		"--resume-session-id", "native-1", "s-7k3f9m", "--cwd", "/tmp/work", "--agent", "copilot",
	})
	if len(positionals) != 1 || positionals[0] != "s-7k3f9m" {
		t.Fatalf("positionals = %v", positionals)
	}
	if *f.resumeID != "native-1" || *f.cwd != "/tmp/work" || *f.agent != "copilot" || *f.clear {
		t.Fatalf("resume flags = id=%q cwd=%q agent=%q clear=%v", *f.resumeID, *f.cwd, *f.agent, *f.clear)
	}
}

func TestSeedResumeIdentityClearFlag(t *testing.T) {
	f := newSeedFlags("set-resume")
	positionals := f.parse("set-resume", []string{"s-7k3f9m", "--clear"})
	if len(positionals) != 1 || positionals[0] != "s-7k3f9m" || !*f.clear {
		t.Fatalf("positionals=%v clear=%v", positionals, *f.clear)
	}
}
