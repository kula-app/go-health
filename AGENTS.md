# Agent Instructions

## Package Manager

- **Go** (`go 1.26.1`) with Makefile
- Run `make help` to discover all available commands
- Always use Makefile targets — never raw `go` or other tool commands

## Commit Attribution

- Do NOT include `Co-Authored-By`, AI assistant names, or `Generated-with` in commits, PRs, or code comments

## Commit Messages

- [Conventional Commits 1.0.0](https://www.conventionalcommits.org/)
- Format: `<type>[optional scope]: <description>`
- Types: `feat`, `fix`, `build`, `chore`, `ci`, `docs`, `style`, `refactor`, `perf`, `test`
- Breaking changes: `feat!:` or `BREAKING CHANGE:` footer

## PR Workflow

- No stacked PRs — always branch from `main`
- Feature branches preferred; multi-feature PRs must use rebase merge
- Branch prefixes: `feature/`, `fix/`, `refactor/`, `docs/`, `chore/`
- Use `gh pr create --body-file` — avoid inline `--body` with backticks

## Public API surface

- Every package outside `internal/` (none today) is public API. Adding exports is non-breaking; changing signatures, removing exports, or changing semantics is.
- Breaking changes require a major version bump.

## Dependencies

- `core/` is stdlib-only. Do not add external imports.
- Each `checks/<name>/` subpackage owns its own SDK deps. Do not cross-import between checks.
- Each `adapters/<name>/` subpackage owns its own transport dep (e.g. `httprouter`). Same rule.
- Any new top-level direct dependency needs justification in the PR description — the library's promise is "pay only for what you import."

## Documentation

- Every exported identifier has a godoc comment.
- The README is for users; godoc is for the API; `docs/architecture.md` is for future maintainers (design rationale and trade-offs).

## Testing

- Standard library `testing` only. No `testify` or other assertion frameworks.
- Table-driven where it fits.
- Tests live next to the code: `foo.go` → `foo_test.go`.
- New check subpackages follow the `dbcheck` / `s3check` template: define a narrow unexported interface for the SDK call, stub it in tests instead of standing up a fake server.

## Invariants

- `system:time` is a reserved check name. `Register*` panics on collision. Preserve.
- On a `pass` result, the engine zeros `ComponentStatus.Output` before serialization. If you add a new field with an RFC "SHOULD be omitted on pass" rule, apply the same suppression.
- `Register*` takes a value copy. Mutating a `Check` after registration must remain a no-op.
- `core/` must not import `net/http` or any SDK. The package layout depends on it.

## Key Conventions

### Logging

- The engine uses the `*slog.Logger` passed via `core.WithLogger(...)`, falling back to `slog.Default()`.
- Anything new that logs follows the same caller-injected pattern. Never use the bare default at package init.

### Import Organization

- Group: stdlib, external deps, internal packages (separated by blank lines)

### Formatting & Code Quality

- `make format` before committing
- `make analyze` for static analysis + security checks
