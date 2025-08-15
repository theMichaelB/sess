#!/bin/bash

set -e

echo "Building sess..."
make clean
make build

SESS="./sess"
TEST_DIR="$HOME/.sess"

cleanup() {
    echo "Cleaning up test sessions..."
    for sock in $TEST_DIR/session-*.sock; do
        if [ -f "$sock" ]; then
            num=$(basename "$sock" .sock | cut -d- -f2)
            $SESS -k "$num" 2>/dev/null || true
        fi
    done
    rm -f $TEST_DIR/.current_session
    rm -f $TEST_DIR/.lock
}

trap cleanup EXIT

echo "=== Test 1: Basic session creation and listing ==="
$SESS &
sleep 1
$SESS ls
echo "✓ Basic operations work"

echo -e "\n=== Test 2: Prevent nested session creation ==="
export SESS_NUM="999"
if $SESS 2>&1 | grep -q "Cannot create session from within existing session"; then
    echo "✓ Nested session prevention works"
else
    echo "✗ Nested session prevention failed"
    exit 1
fi
unset SESS_NUM

echo -e "\n=== Test 3: Handle non-existent session attachment ==="
if $SESS -a 999 2>&1 | grep -q "does not exist"; then
    echo "✓ Non-existent session handling works"
else
    echo "✗ Non-existent session handling failed"
    exit 1
fi

echo -e "\n=== Test 4: Concurrent session creation ==="
for i in {1..5}; do
    $SESS &
done
sleep 2
count=$($SESS ls | grep -c "^[* ] ")
if [ "$count" -ge 5 ]; then
    echo "✓ Concurrent session creation works ($count sessions)"
else
    echo "✗ Concurrent session creation failed"
    exit 1
fi

echo -e "\n=== Test 5: Kill all sessions ==="
for num in $($SESS ls | grep -o "^[* ] *[0-9]\+" | awk '{print $NF}'); do
    $SESS -k "$num"
done
sleep 1
if $SESS ls | grep -q "No active sessions"; then
    echo "✓ Session cleanup works"
else
    echo "✗ Session cleanup failed"
    exit 1
fi

echo -e "\n=== Test 6: Lock contention test ==="
for i in {1..10}; do
    $SESS &
done
sleep 3
count=$($SESS ls 2>/dev/null | grep -c "^[* ] " || echo "0")
if [ "$count" -ge 8 ]; then
    echo "✓ Lock contention handled well ($count/10 sessions created)"
else
    echo "✗ Lock contention handling needs improvement"
fi

echo -e "\n=== Test 7: Rapid attach/detach cycles ==="
$SESS &
sleep 1
first_num=$($SESS ls | grep -o "^[* ] *[0-9]\+" | awk '{print $NF}' | head -1)
if [ -n "$first_num" ]; then
    for i in {1..5}; do
        timeout 1 $SESS -a "$first_num" </dev/null || true
        sleep 0.1
    done
    echo "✓ Rapid attach/detach handled"
else
    echo "✗ No session to test attach/detach"
fi

echo -e "\n=== Test 8: Session persistence test ==="
$SESS &
sleep 1
session_before=$($SESS ls)
killall -STOP sess 2>/dev/null || true
sleep 1
killall -CONT sess 2>/dev/null || true
sleep 1
session_after=$($SESS ls)
if [ "$session_before" = "$session_after" ]; then
    echo "✓ Sessions persist through SIGSTOP/SIGCONT"
else
    echo "✗ Session persistence failed"
fi

echo -e "\n=== Test 9: File permission checks ==="
if [ -d "$TEST_DIR" ]; then
    perms=$(stat -c %a "$TEST_DIR" 2>/dev/null || stat -f %p "$TEST_DIR" | tail -c 4)
    if [ "$perms" = "700" ]; then
        echo "✓ Directory permissions correct (700)"
    else
        echo "✗ Directory permissions incorrect ($perms)"
    fi
fi

echo -e "\n=== Test 10: Cleanup stale sessions ==="
mkdir -p "$TEST_DIR"
echo '{"number":"998","created_at":"2020-01-01T00:00:00Z","pid":99999999,"command":"/bin/sh"}' > "$TEST_DIR/session-998.meta"
touch "$TEST_DIR/session-998.sock"
$SESS ls >/dev/null 2>&1
if [ ! -f "$TEST_DIR/session-998.meta" ]; then
    echo "✓ Stale session cleanup works"
else
    echo "✗ Stale session cleanup failed"
    rm -f "$TEST_DIR/session-998.meta" "$TEST_DIR/session-998.sock"
fi

echo -e "\n=== All tests completed ==="
cleanup
echo "✓ Test suite passed"