# CLAUDE.md

Guidance for agents and contributors working in this repository.

`device-gateway` is a Go plugin-framework TCP gateway for IoT/GPS devices — one or
more unit-type plugins (howen, fleetiger, cathexis) hosted in a single process —
with a Next.js admin BFF. See [README.md](README.md) for the architecture.

## Branch convention

Start every task on its own branch off `main`. Never commit new work directly to
`main`, and don't pile unrelated work onto an existing branch.

- **Feature / addition** → `feature/<short-kebab-name>` (e.g. `feature/clip-export`)
- **Bug fix** → `bug/<short-kebab-name>` (e.g. `bug/idle-timeout-hang`)

Land the branch on `main` (merge or PR) when the task is done.

## Before you commit

- Go must be `gofmt`-clean and pass `go vet ./...` and `go test ./...`. CI enforces
  all three, plus `go test -race ./...` and a short fuzz pass over the decoders.
- The Go toolchain runs in Docker, so no local Go is required: `make test` /
  `make build` (or `go test ./...` if you have Go installed).
- Admin (`admin/`): keep `npx tsc --noEmit` and `npm run build` green.

## Notes

- The universal message format is the invariant — `internal/core/message` is byte-for-byte
  golden-tested against the original JS adapter. Don't change its output without
  regenerating the golden file intentionally.
- Binary decoders parse untrusted device bytes; keep them panic-free (fuzz targets
  guard this) and never invoke `ffmpeg` via a shell.
