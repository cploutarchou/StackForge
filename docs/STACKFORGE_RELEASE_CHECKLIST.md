# StackForge Release Checklist

Do not mark a release production-ready until every required item is checked or a blocker is documented.

- [ ] `gofmt` passed.
- [ ] `go test ./...` passed.
- [ ] `go vet ./...` passed.
- [ ] `golangci-lint` passed or documented unavailable.
- [ ] `go build -o bin/stackforge ./cmd/stackforge` passed.
- [ ] `bin/stackforge --help` passed.
- [ ] Dry-run single-node install passed.
- [ ] Dry-run three-node install passed.
- [ ] Live single-node staging install passed.
- [ ] Live single-node verify passed.
- [ ] Live three-node staging install passed.
- [ ] Live three-node verify passed.
- [ ] Live backup passed.
- [ ] Live restore dry-run passed.
- [ ] Live uninstall dry-run passed.
- [ ] Live uninstall passed.
- [ ] Generated secrets file permissions verified.
- [ ] Remote env permissions verified.
- [ ] Firewall verified.
- [ ] Auth verified.
- [ ] Production config reviewed.
- [ ] Rollback tested.
- [ ] Known limitations documented.

Current Phase 4 status: staging-ready only after the disposable-server runbook has passed. Do not claim production-ready before live validation evidence is attached to this checklist.
