# sess - Resilient Architecture Design

## Core Principles for Deadlock Prevention

1. **Non-blocking I/O everywhere**
   - All socket operations use timeouts
   - PTY I/O uses select/poll with timeouts
   - Never block indefinitely on reads or writes

2. **Clear ownership and lifecycle**
   - Daemon owns the PTY and child process
   - Client connects temporarily and disconnects cleanly
   - Resources are cleaned up immediately on disconnect

3. **Atomic operations**
   - File operations use atomic rename
   - Lock files prevent concurrent modifications
   - Session state changes are atomic

4. **Graceful degradation**
   - If operation fails, system remains in consistent state
   - Partial failures don't corrupt session state
   - Always have escape hatches (timeouts, forced cleanup)

## Architecture Components

### 1. Session Daemon (`internal/daemon/`)
- One daemon per session
- Manages PTY and child process
- Handles client connections via Unix socket
- Key features:
  - Signal handling (SIGCHLD, SIGTERM, etc.)
  - Non-blocking I/O loop with select/poll
  - Connection state machine
  - Automatic cleanup on child exit

### 2. Client (`internal/client/`)
- Connects to daemon via Unix socket
- Handles terminal setup/restore
- Key features:
  - Connection timeout (5 seconds)
  - Graceful disconnect on signals
  - Raw terminal mode management
  - Non-blocking I/O

### 3. Session Manager (`internal/session/`)
- Manages session metadata
- Provides atomic operations
- Key features:
  - File locking for concurrent access
  - Atomic file updates
  - Session validation
  - Automatic cleanup of dead sessions

### 4. Communication Protocol
- Simple, stateful protocol over Unix socket
- Messages:
  - CONNECT: Client initiates connection
  - READY: Daemon accepts connection
  - DATA: Bidirectional data transfer
  - RESIZE: Terminal resize events
  - DISCONNECT: Clean disconnect
  - PING/PONG: Keep-alive mechanism

### 5. Error Recovery
- All operations have timeouts
- Broken connections are detected quickly
- Resources are cleaned up on any error
- No operation can block indefinitely

## Key Design Decisions

1. **Single active connection per session**
   - Prevents complexity of multiplexing
   - Clear ownership model
   - Easy to reason about state

2. **File-based session tracking**
   - Simple and reliable
   - Survives crashes
   - Easy to debug
   - Atomic updates prevent corruption

3. **Separate daemon per session**
   - Process isolation
   - Simple lifecycle
   - Easy cleanup
   - No shared state between sessions

4. **Timeout-based failure detection**
   - All I/O operations have timeouts
   - Keep-alive mechanism detects hung connections
   - Automatic cleanup on timeout

## Deadlock Prevention Strategies

1. **No circular dependencies**
   - Clear hierarchy: client -> daemon -> child
   - No callbacks from daemon to client
   - Unidirectional control flow

2. **Non-blocking everything**
   - Socket operations use O_NONBLOCK
   - PTY I/O uses select with timeout
   - No synchronous waits

3. **Resource ordering**
   - Always acquire locks in same order
   - Release resources in reverse order
   - Use defer for cleanup

4. **Timeout escape hatches**
   - Every blocking operation has a timeout
   - Timeouts trigger cleanup and recovery
   - No infinite waits

## Implementation Flow

1. **Session Creation**
   ```
   Client -> Create daemon process
   Daemon -> Fork child with PTY
   Daemon -> Create socket and listen
   Daemon -> Write metadata file
   Client -> Connect to daemon
   ```

2. **Attach Flow**
   ```
   Client -> Read metadata
   Client -> Connect to socket (with timeout)
   Client -> Enter raw terminal mode
   Client <-> Daemon: Bidirectional data flow
   ```

3. **Detach Flow**
   ```
   Client -> Send DISCONNECT
   Client -> Restore terminal
   Daemon -> Close client connection
   Daemon -> Continue managing child
   ```

4. **Cleanup Flow**
   ```
   Child exits -> Daemon receives SIGCHLD
   Daemon -> Cleanup and exit
   OR
   Kill command -> Send SIGTERM to daemon
   Daemon -> Kill child, cleanup, exit
   ```