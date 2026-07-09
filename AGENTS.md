# Kitebroker — Agent Instructions

## Build & Run

- **No `go.mod`** — this is a GOPATH project. Build from `github.com/cmcoffee/kitebroker`.
- `make build` — builds to `build/kitebroker` (current platform).
- `make cross` — builds for linux, windows, darwin (amd64/arm64).
- `make clean` — removes `build/`.
- For quick compile check: `go build -o /tmp/kitebroker .`
- Run tests from `/tmp/` so generated data/db/config files don't pollute the source tree.

## Architecture

Single binary, CLI tool. Three source files in root wire everything:

| File | Role |
|------|------|
| `kitebroker.go` | `main()` — flags, config load, task dispatch |
| `menu.go` | Command menu — registers, selects, and executes tasks |
| `tasks.go` | Blank imports that trigger `init()` registration for all task packages |
| `config.go` | Interactive `--setup` wizard; API auth; encrypted config storage |
| `custom_tasks.go` | Custom tasks behind `//go:build custom` tag — NOT compiled by default |

### Task Registration

Tasks self-register via `init()` using one of three functions:
- `RegisterTask(task)` — universal (all auth modes)
- `RegisterAdminTask(task)` — admin-only (JWT/Signature auth required)
- `RegisterMigrationTask(task)` — migration tasks

Each task implements the `Task` interface (`core/common.go`):
```go
type Task interface {
    Get() *KiteBrokerTask
    Name() string
    Desc() string
    Init() error
    Main() error
}
```

Task structs embed `KiteBrokerTask` as the **last field**. User input goes in an `input` struct. See `extras/task_example.go` for the template.

### Package Layout

```
core/           — types, interfaces, Kiteworks API clients, utils, aliases for snugforge
tasks/admin/    — admin-only tasks (users, files_and_folders)
tasks/user/     — universal user tasks (upload, download, ls, mail, etc.)
tasks/migration/ — migration tasks (box, kiteworks, quatrix)
extras/         — task template example
```

### Task Packages

Tasks are grouped by sub-package. Each sub-package has a `init()` in one file that registers all tasks in that package. Blank imports in `tasks.go` pull them in:
```
tasks/admin/files_and_folders  — folder_report, cleaner, demote_perms, etc.
tasks/admin/users              — user_report, csv_onboard, user_remover, etc.
tasks/user                     — upload, download, ls, send_file, mail, etc.
tasks/migration/box            — Box.com migration
tasks/migration/kiteworks      — Kiteworks-to-Kiteworks migration
tasks/migration/quatrix        — Quatrix migration
```

## Conventions

- **snake_case** for variables, fields, functions, task names. UPPER for constants.
- Receiver name: `T` for tasks, `m` for menu, `d` for database.
- Custom errors: `type Error string` with `(e Error) Error() string`.
- `Critical(err)` for unrecoverable errors (logs + exits). `Err(input)` for recoverable (increments counter).
- `NONE = ""` constant used as empty-string sentinel throughout.
- `Desc()` returns `"Category: Description"` format — the part before `:` becomes a submenu group in help output.

## Dependencies

Relies on `github.com/cmcoffee/snugforge` sub-packages (nfo, cfg, eflag, kvlite, xsync, swapreader). Type aliases in `core/common.go` expose them. Third-party deps beyond snugforge are minimal.

## Config & Data

- Config: `kitebroker.ini` next to the binary. Sensitive API credentials stored AES-encrypted in `[do_not_modify]` section.
- Database: `data/kitebroker.db` — kvlite embedded DB, hardware-locked via MAC address.
- Logs: `logs/kitebroker.log` — rotating log file.
- The `[do_not_modify]` section with `api_cfg_0`/`api_cfg_1` is required. Missing it triggers a fatal error on API init.

## Auth Modes

- `JWT_AUTH` (default, recommended) — JWT-based auth with RSA private key
- `SIGNATURE_AUTH` — OAuth signature-based (deprecated, phasing out 2026)
- Password auth is no longer supported
- Auth mode set via `auth_flow` key in config; controls which tasks are visible

## Custom Build Tag

`custom_tasks.go` is guarded by `//go:build custom`. Custom tasks (JSONCSVTask, FolderUpdateTask, SFTPGWTask, BulkDownloaderTask) are NOT included in default builds. Add `-tags custom` to `go build` to include them.
