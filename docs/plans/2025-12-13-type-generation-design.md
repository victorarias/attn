# Type Generation Pipeline Design

**Date:** 2025-12-13
**Status:** Approved

## Summary

Single source of truth for protocol types using TypeSpec → JSON Schema → quicktype → Go/TypeScript.

## Pipeline

```
main.tsp (nice syntax)
    ↓ tsp compile
tsp-output/json-schema/*.json (intermediate)
    ↓ quicktype
generated.go + generated.ts
```

## File Structure

```
internal/protocol/
├── schema/
│   ├── main.tsp              # Source of truth
│   ├── tspconfig.yaml        # TypeSpec config
│   ├── package.json          # pnpm dependencies
│   └── tsp-output/           # Generated JSON Schema (gitignored)
├── generated.go              # Generated Go types
├── types.go                  # Deprecated, removed after migration
└── parse.go                  # Hand-written parsing logic

app/src/types/
└── generated.ts              # Generated TypeScript types
```

## TypeSpec Format

```typespec
import "@typespec/json-schema";
using TypeSpec.JsonSchema;

@jsonSchema
namespace Protocol;

enum SessionState { working, waiting_input, idle }

model Session {
  id: string;
  label: string;
  directory: string;
  state: SessionState;
  muted?: boolean;
}

model RegisterMessage {
  cmd: "register";
  id: string;
  directory: string;
}
```

## Build Integration

```makefile
generate-types:
	cd internal/protocol/schema && pnpm exec tsp compile .
	npx quicktype \
	    --src internal/protocol/schema/tsp-output/json-schema/*.json \
	    --src-lang schema --lang go --package protocol \
	    -o internal/protocol/generated.go
	npx quicktype \
	    --src internal/protocol/schema/tsp-output/json-schema/*.json \
	    --src-lang schema --lang typescript \
	    -o app/src/types/generated.ts

check-types: generate-types
	git diff --exit-code internal/protocol/generated.go app/src/types/generated.ts
```

## Migration Strategy

1. **Setup** - TypeSpec files, generation pipeline, Makefile targets
2. **Parallel types** - Generated types alongside existing, migrate consumers one by one
3. **Switchover** - Delete `types.go`, update imports
4. **CI enforcement** - `check-types` fails if generated files are stale

## What Gets Committed

- `*.tsp` files - source of truth
- `tspconfig.yaml` - config
- `generated.go` - for Go module compatibility
- `generated.ts` - for frontend build
- `tsp-output/` - NOT committed (gitignored)

## Proof of Concept

Tested with Session, PR, Worktree, RegisterMessage types. Pipeline produces clean Go structs with proper JSON tags and TypeScript interfaces with enums.
