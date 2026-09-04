# Contributing

This repository is primarily a curated portfolio snapshot, but reproducible bug reports and focused documentation corrections are welcome.

Before opening a pull request:

1. Do not add raw public-data downloads, customer data, credentials, internal prompts, model weights, or generated database snapshots.
2. Keep changes scoped and explain the operational trade-off they address.
3. Run `gofmt` on changed Go files.
4. Run `go test ./...` and `go vet ./...`.
5. Update `openapi.yaml` when route behavior changes.

Security or privacy reports belong in the private channel described by [SECURITY.md](SECURITY.md), not in a public issue.

