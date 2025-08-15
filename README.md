# sess — Minimal Session Persistence for the Terminal

`sess` creates a shell inside a daemon-managed PTY that you can attach/detach from. It aims to be small, fast, and robust for everyday use.

## Highlights

- One daemon per session, one active client at a time
- Safe file-based tracking with a lock file (`~/.sess` with 0700 perms)
- Unix socket per session (`0600`), metadata (`0600`), automatic stale cleanup
- Signal-aware: handles SIGWINCH, SIGCHLD, SIGTERM, SIGINT, SIGUSR1
- PTY size set on start and on attach; immediate width/height sync

## Features

- Create a new session and attach immediately
- Attach to an existing session by number
- Detach via `sess -x` or Ctrl-X while attached
- Kill a session by number, or kill all sessions
- `sess ls` shows a STATUS column and marks current with `*`

## Requirements

- Go 1.21+
- Linux or other Unix-like OS. Uses `golang.org/x/sys/unix` and `github.com/creack/pty`.

## Build and Install

```bash
make build                      # builds ./sess with default version
make VERSION=v1.2.3 build       # builds with semver injected via ldflags
sudo make install               # installs to /usr/local/bin/sess
```

## Usage

```bash
sess                  # Create and attach to a new session
sess ls               # List sessions (STATUS: attached/detached)
sess -a 001           # Attach to session 001
sess -A 002           # Attach or create session 002
  sess -x               # Detach current client (or press Ctrl-X while attached)
  sess -C               # Disable Ctrl-X detach for this attachment
  sess --no-ctrlx       # Same as -C
  sess -k 001           # Kill session 001
  sess -k               # Kill current session
  sess -K               # Kill all sessions
  sess -v, --version    # Show version
```

Notes:
- `sess` keeps its data under `~/.sess/`.
- During an active attachment, `~/.sess/.current_session` tracks the client PID and session number.
- Set `SESS_DEBUG=1` to enable terse client/daemon debug logs on stderr.

## Testing

Interactive behavior is exercised via PTY-backed scripts.

```bash
./test_usability.sh     # Create/attach/detach/kill flows
./test_edge_cases.sh    # Concurrency and edge scenarios
```

## Architecture

See `ARCHITECTURE.md` for a deeper dive. At a glance:

- `cmd/main.go` — CLI entrypoint and subcommand handling
- `internal/daemon` — session daemon: PTY management, socket, IO loops
- `internal/client` — attach client: raw TTY, signal handling, data path
- `internal/session` — session manager: files, locking, metadata
- `internal/protocol` — minimal helpers for raw messaging

## Known Limitations

- Single active client per session (by design); a second attach attempt is rejected.
- Linux-focused; other Unix-like systems may work but aren’t primary targets.
- No persistence of scrollback/buffer; this is a live PTY, not a multiplexer.

## Security Considerations

- Socket files are `0600`; session dir is `0700`.
- Metadata contains PID and command; no sensitive environment is persisted.
- The daemon drops stdio to `/dev/null` after setup and runs the shell in its own session with controlling TTY.

## Contributing

- Run `make fmt vet` before sending changes.
- Please include a brief note justifying any dependency changes.

## Licensing

Licensed under the MIT License. See `LICENSE` for details.
