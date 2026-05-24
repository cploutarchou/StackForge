# Development

StackForge is a Go project.

## Repository Layout

```text
cmd/stackforge/main.go                 CLI entry point
internal/stackforge/cli                Cobra commands
internal/stackforge/config             YAML config loading and validation
internal/stackforge/install            Main install planner/runner
internal/stackforge/bootstrap          SSH key bootstrap
internal/stackforge/components         Component helper installs and status
internal/stackforge/firewall           UFW plan/apply helpers
internal/stackforge/inventory          Inventory state and live observation
internal/stackforge/safety             Production safety checks
internal/stackforge/secrets            Generated secrets and env deployment
internal/stackforge/backup             Backup/restore
internal/stackforge/rollback           Rollback records and apply
internal/stackforge/validate           Preflight validation
internal/stackforge/verify             Post-install verification
internal/controlplane                  HTTP API and domain control-plane code
migrations                             SQL schema
examples                               Example YAML configs
scripts                                Release installer
```

## Local Setup

Install Go matching `go.mod`.

The module currently declares:

```text
go 1.25.0
```

Download dependencies:

```bash
go mod download
```

Build:

```bash
go build -o bin/stackforge ./cmd/stackforge
```

Run:

```bash
bin/stackforge --help
```

## Run Tests

```bash
go test ./...
```

Run vet:

```bash
go vet ./...
```

Format:

```bash
gofmt -w $(find . -name '*.go' -not -path './vendor/*')
```

The release workflow runs formatting check, tests, and vet.

## CI/CD

GitHub Actions workflow:

```text
.github/workflows/release.yml
```

It runs on pushes to `master`.

Steps:

1. Checkout.
2. Set up Go from `go.mod`.
3. Check formatting.
4. Run `go test ./...`.
5. Run `go vet ./...`.
6. Build Linux `amd64` and `arm64` artifacts.
7. Write `checksums.txt`.
8. Publish a GitHub release.

Release version format:

```text
v0.1.<GITHUB_RUN_NUMBER>
```

## Add A New CLI Command

Commands are registered in `internal/stackforge/cli/cli.go`.

Basic steps:

1. Add a function that returns `*cobra.Command`.
2. Keep command logic thin.
3. Put service logic in an internal package.
4. Add flags near the command that uses them.
5. Register the command in `newRoot()` or the correct parent command.
6. Add tests for the service logic and any command parsing behavior that is risky.
7. Update `docs/CLI_REFERENCE.md`.

Example shape:

```go
func exampleCmd() *cobra.Command {
    cmd := &cobra.Command{
        Use:   "example",
        Short: "Run example behavior",
        RunE: func(cmd *cobra.Command, args []string) error {
            return example.Run(...)
        },
    }
    return cmd
}
```

Avoid putting complex install or network behavior directly in the CLI package.

## Add A New Service Package

Use the existing package style:

- Define small input `Options` structs.
- Return typed reports.
- Keep command execution behind interfaces when possible.
- Use `remoteexec.Executor` for SSH work.
- Avoid printing from service packages unless the package is explicitly CLI-facing.
- Keep secrets out of returned errors and reports.

Good examples:

- `internal/stackforge/backup`
- `internal/stackforge/bootstrap`
- `internal/stackforge/firewall`
- `internal/stackforge/validate`

## Add A New Install Step

Install steps live in `internal/stackforge/install/install.go`.

Each step should define:

- `ID`
- `Name`
- `Node`
- `Role`
- `DryRunDescription`
- `Check`
- `Apply`
- `Verify`
- `Rollback`
- `FailureRecovery`
- `IdempotencyKey`
- `ChangedFiles`
- `BackupFiles`

Rules:

- Check should detect whether the work is already done.
- Apply should be idempotent where possible.
- Verify should prove the component works after apply.
- Rollback metadata should be clear.
- Dangerous automatic rollback should be marked unsafe by role behavior.
- Use secret redaction for commands that include generated secrets.

## Add Validation Checks

Validation is split:

- `config.Validate`: structural config validation.
- `safety.Check`: production safety policy.
- `validate.RunWithOptions`: preflight checks and live SSH checks.
- `verify.Run`: post-install live verification.

Choose the right layer.

Examples:

- Missing required YAML field belongs in `config.Validate`.
- Dangerous production exposure belongs in `safety.Check`.
- Server capability checks belong in `validate`.
- Installed service health belongs in `verify`.

## Code Structure Rules

Follow the current project style:

- Keep CLI code in `internal/stackforge/cli`.
- Keep control-plane HTTP behavior in `internal/controlplane/http`.
- Keep reusable logic in small internal packages.
- Use typed reports for command outputs.
- Use YAML/JSON structs with explicit tags.
- Use `context.Context` for remote or network operations.
- Use `remoteexec.Executor` interfaces in tests.
- Keep dry-run paths meaningful.
- Refuse unsafe behavior instead of returning fake success.

## Security Guidelines

- Do not print secrets.
- Do not store SSH passwords.
- Do not add password flags.
- Do not weaken safety checks to make examples pass live install.
- Do not expose admin APIs publicly by default.
- Do not add broad `0.0.0.0/0` access except for public HTTP/HTTPS.
- Be careful with shell command generation and quoting.
- Prefer idempotent remote commands.
- Preserve mode `0600` for generated secrets and env files.

## Testing Guidance

Add tests for:

- config validation
- safety failures
- dry-run output reports
- remote command planning
- SSH bootstrap error handling
- secret redaction
- inventory state changes
- domain and DNS validation
- backup/restore safety behavior

Existing test files cover most packages. Use fake executors instead of real SSH in unit tests.

## Contribution Checklist

Before opening a PR:

- Run `gofmt`.
- Run `go test ./...`.
- Run `go vet ./...`.
- Update docs for any CLI, config, env, or behavior changes.
- Add or update examples only when they remain safe as templates.
- Make sure live behavior has dry-run and safety handling.
