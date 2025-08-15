#!/bin/bash
set -euo pipefail

SESS_BIN="./sess"
SESS_DIR="$HOME/.sess"

log() { echo "[USABILITY] $*"; }
fail() { echo "[USABILITY][FAIL] $*"; exit 1; }

cleanup_all() {
  rm -f "$SESS_DIR"/session-*.sock "$SESS_DIR"/session-*.meta "$SESS_DIR"/.current_session "$SESS_DIR"/.lock 2>/dev/null || true
}

extract_first_session_num() {
  "$SESS_BIN" ls | awk 'NR>1 && $2!="STATUS" {print $2,$3}' | awk '{print $1}' | head -n1
}

build() {
  log "Building sess binary"
  go build -ldflags="-s -w" -o sess ./cmd/main.go
}

assert_attached() {
  local num="$1"
  "$SESS_BIN" ls | grep -E "^\* +$num" >/dev/null || fail "Session $num not marked attached"
}

assert_detached() {
  local num="$1"
  "$SESS_BIN" ls | grep -E "^[ ] +$num +detached" >/dev/null || fail "Session $num not marked detached"
}

assert_no_current() {
  "$SESS_BIN" ls | grep -q "^\*" && fail "A session is still marked current" || true
}

main() {
  cleanup_all
  build

  log "1) Create a session and ensure it stays attached for a bit"
  script -qfec "$SESS_BIN" /dev/null &
  script_pid=$!
  sleep 1
  # Verify the client is still running and a session is attached
  if ! kill -0 "$script_pid" 2>/dev/null; then
    fail "Client exited too early (immediate detach bug?)"
  fi
  num=$(extract_first_session_num)
  [ -n "$num" ] || fail "No session number found after create"
  assert_attached "$num"

  log "2) Detach using sess -x"
  "$SESS_BIN" -x
  # Wait a bit for client to exit
  timeout 3 bash -c "while kill -0 $script_pid 2>/dev/null; do sleep 0.1; done" || true
  assert_detached "$num"
  assert_no_current

  log "3) Attach to the same session number, then Ctrl-X to detach"
  printf '\x18' | script -qfec "$SESS_BIN -a $num" /dev/null
  assert_detached "$num"

  log "4) Kill the session"
  "$SESS_BIN" -k "$num"
  if "$SESS_BIN" ls | grep -q "^[* ] *$num "; then
    fail "Session $num still listed after kill"
  fi

  log "All usability checks passed"
}

main "$@"

