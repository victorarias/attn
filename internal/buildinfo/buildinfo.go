package buildinfo

var (
	Version           = "dev"
	BuildTime         = "unknown"
	SourceFingerprint = "unknown"
	GitCommit         = "unknown"
	// SnapshotFormat identifies the terminal-snapshot wire format this build
	// encodes, so a client that cannot decode it can say so instead of trying.
	// scripts/snapshot-format.sh derives it; "unknown" (a build that skipped
	// the ldflag) never matches a client and costs restore, never correctness.
	SnapshotFormat = "unknown"
)
