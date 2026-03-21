# NOFX Agent Notes

## Environment

- Do not assume `go` is on `PATH`.
- Before any Go command, use one of these:
  - `source ./scripts/go_env.sh`
  - `./scripts/with_go_env.sh <command> ...`
- `scripts/go_env.sh` auto-detects the repo-required Go toolchain from `go.mod`, prefers the cached toolchain under `$HOME/go/pkg/mod/...`, falls back to `/usr/local/go/bin/go`, and exports `GOCACHE=/tmp/nofx-go-build` by default.
- Long-running services must be started with `nohup` plus `disown`; otherwise this environment may terminate them when the parent task/session exits.

## Preferred Commands

- Backend build: `./scripts/with_go_env.sh go build -o nofx .`
- Backend test: `./scripts/with_go_env.sh go test ./...`
- Formatting: `./scripts/with_go_env.sh gofmt -w <files>`
- Full-stack restart: `make dev`
