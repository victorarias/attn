# Contributing to attn

Thanks for your interest in contributing! Before you start, please read through this guide.

## Before You Start

**Open an issue first.** Before investing time in code, please open an issue to discuss your proposed change. This helps:

- Ensure the change aligns with the project's direction
- Avoid duplicate work
- Get early feedback on your approach

Not all contributions will be accepted. Opening an issue first sets expectations and saves everyone time.

## Getting Started

1. Fork the repository
2. Clone your fork
3. Install dependencies: Go 1.21+, Rust, Node.js, pnpm
4. Build: `make build-app`

## Development Workflow

```bash
make install              # Fast iteration on daemon (~2s build)
cd app && pnpm run dev:all  # Frontend with hot reload
make test-all             # Run all tests before submitting
```

## Pull Request Process

1. **Open an issue first** to discuss your proposed change
2. Create a branch from `main`
3. Make your changes with clear commit messages
4. Ensure tests pass: `make test-all`
5. Submit a PR referencing the issue

## Code Style

- Go: `gofmt` (enforced)
- TypeScript: Prettier (`pnpm format`)
- Commits: Conventional commits (`feat:`, `fix:`, `refactor:`, `docs:`, `chore:`)

## Architecture

See [CLAUDE.md](CLAUDE.md) for detailed architecture documentation.

## Questions?

Open an issue with your question. We're happy to help.
