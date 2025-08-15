Here is the complete specification for sess - a minimal session persistence tool implemented in Go.

⸻

Name:
	•	sess

Purpose:
	•	Provide minimal, robust terminal session persistence: detach and reattach interactive terminal processes, with zero multiplexing or extra features.
	•	Sessions persist across terminal closures and system disconnections.

⸻

Features

	1.	Create new session
	•	sess
	•	Creates a new session, launches the user's shell (from $SHELL or fallback to /bin/sh).
	•	Sessions are numbered sequentially (e.g., 001, 002, 003…) with no re-use or filling of gaps.
	•	Immediately attaches you to the new session.
	•	Outputs the session number and start time to the user.
	•	Prevents creating new sessions from within existing sessions.

	2.	List sessions
	•	sess ls
	•	Lists all active sessions, showing:
		•	Session number
		•	Start time (readable format)
		•	PID of attached process
		•	Process name or command
		•	Current session indicator (*) when run from within a session
	•	Automatically removes dead or stale sessions from the list.
	•	Cleans up stale current session tracking.

	3.	Attach to session
	•	sess -a <number>
	•	Attaches to the given session by number.
	•	If the session is dead or socket is missing, prints a clear error and deletes the metadata/socket.
	•	Prevents attaching to the same session you're already in (deadlock prevention).
	•	Sets current session tracking while attached.

	4.	Attach or create session
	•	sess -A <number>
	•	If session <number> exists and is alive, attaches.
	•	If not, creates a new session with that number.
	•	Prevents creating new sessions from within existing sessions.
	•	Prevents self-attachment when session already exists.

	5.	Detach from session
	•	sess -x
	•	Detaches from the currently attached session (only works when in a session).
	•	Session continues running in background.
	•	Clears current session tracking.
	•	Shows error if not attached to any session.

	6.	Kill session
	•	sess -k [number]
	•	Without number: kills current session (only when attached).
	•	With number: kills specified session by number (works from anywhere).
	•	Uses SIGTERM first, then SIGKILL if needed.
	•	Cleans up session files and current session tracking.
	•	Shows error if session doesn't exist or is already dead.

	7.	Help
	•	sess -h, --help
	•	Shows concise usage information.

⸻

Session Storage
	•	All session data and sockets are stored in ~/.sess/
	•	Session files:
		•	session-<number>.sock - Unix socket for attach/detach
		•	session-<number>.meta - JSON file containing:
			•	Creation time (RFC3339)
			•	PID of child process
			•	Command started
			•	Session number
		•	.current_session - tracks which session you're currently attached to
	•	Directory permissions: 0700
	•	Socket and meta file permissions: 0600

⸻

Process Handling
	•	On creation, spawns a daemon process that:
		•	Launches a child shell (from $SHELL or fallback to /bin/sh)
		•	Creates a Unix socket server for client connections
		•	Manages PTY for the shell process
		•	Properly daemonizes (detaches from creating terminal)
	•	Daemon stores child PID in metadata
	•	When listing or attaching, checks that PID is alive
	•	If process is dead, cleans up socket and meta files automatically
	•	Only allows one active connection per session at a time

⸻

Security
	•	Only the creating user can access session directory and files
	•	No network access, sockets are Unix domain only
	•	Sessions are isolated per user account

⸻

Failure Modes and Handling
	•	If attach fails due to session death, prints clear error and cleans up
	•	If unable to create new session (e.g., PTY limit), fails gracefully with actionable error
	•	If trying to create session from within existing session, shows helpful error message
	•	If trying to attach to current session, prevents deadlock with clear error
	•	If directory or files have incorrect permissions, operations fail with clear errors
	•	Automatic cleanup of dead sessions and stale tracking files

⸻

Usability
	•	Session numbers are strictly increasing, never reused
	•	New session numbers are allocated as max(existing numbers) + 1
	•	Gaps in numbering are not filled
	•	Clear, consistent output messages:
		•	On creation: "Created session 007 at 2025-07-26 15:22"
		•	On attach: "Attaching to session 007"
		•	On detach: "Detached from session 007"
		•	On kill: "Killed session 007"
	•	List format with current session indicator:

SESSION  CREATED              PID     CMD
*  001   2025-07-26 15:22     41256   /bin/bash
   002   2025-07-26 16:00     41422   /bin/bash

* indicates current session (001)

⸻

Session Persistence
	•	Sessions survive terminal closures and disconnections
	•	Daemon processes properly detach from creating terminal
	•	Sessions can be attached from any new terminal
	•	Current session tracking persists across terminal sessions
	•	Automatic cleanup prevents resource leaks

⸻

Edge Case Prevention
	•	Prevents nested session creation
	•	Prevents self-attachment deadlocks  
	•	Validates session existence and liveness
	•	Handles daemon process cleanup properly
	•	Manages concurrent connection attempts
	•	Cleans up stale session tracking

⸻

Architecture
	•	Client-daemon model with Unix domain sockets
	•	Each session runs as an independent daemon process
	•	PTY management for proper terminal behavior
	•	Robust connection handling with proper cleanup
	•	File-based session metadata and tracking

⸻

Assumptions and Limitations
	•	Target: Linux/Unix (macOS possible, but not tested)
	•	Only interactive shells or processes are supported (no script execution)
	•	The user is responsible for cleaning up very old sessions if they accumulate
	•	Not designed for team sharing, no networked sessions
	•	Sessions are numbered, not named (v1 limitation)