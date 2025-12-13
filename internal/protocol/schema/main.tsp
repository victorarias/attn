import "@typespec/json-schema";

using TypeSpec.JsonSchema;

@jsonSchema
namespace Protocol;

// Enums
enum SessionState {
  working,
  waiting_input,
  idle,
}

enum PRRole {
  author,
  reviewer,
}

enum HeatState {
  cold,
  warm,
  hot,
}

// Core entities
model Session {
  id: string;
  label: string;
  directory: string;
  branch?: string;
  is_worktree?: boolean;
  main_repo?: string;
  state: SessionState;
  state_since?: string;
  todos?: string[];
  last_seen?: string;
  muted?: boolean;
}

model PR {
  id: string;
  repo: string;
  number: int32;
  title: string;
  url: string;
  role: PRRole;
  state: string;
  reason?: string;
  muted?: boolean;
  ci_status?: string;
  review_status?: string;
  mergeable?: boolean;
  head_sha?: string;
  approved_by_me?: boolean;
  has_new_changes?: boolean;
  heat_state?: HeatState;
}

model Worktree {
  path: string;
  branch: string;
  main_repo: string;
  created_at?: string;
}

// A simple message to test
model RegisterMessage {
  cmd: "register";
  id: string;
  label?: string;
  directory: string;
}
