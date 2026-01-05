module github.com/victorarias/attn

go 1.25.3

require (
	github.com/mattn/go-sqlite3 v1.14.32
	nhooyr.io/websocket v1.8.17
)

require (
	github.com/google/uuid v1.6.0
	github.com/victorarias/claude-agent-sdk-go v0.0.0-00010101000000-000000000000
)

require golang.org/x/time v0.14.0 // indirect

replace github.com/victorarias/claude-agent-sdk-go => ../claude-agent-sdk-go
