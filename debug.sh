#!/bin/bash

echo "=== Testing daemon startup ==="

# Clean up any existing sessions
rm -rf ~/.sess

# Build
make clean
make build

# Start a session with debug output
echo "Starting session..."
./sess &
SESS_PID=$!

sleep 2

echo -e "\n=== Checking process ==="
ps aux | grep sess | grep -v grep

echo -e "\n=== Checking session files ==="
ls -la ~/.sess/ 2>/dev/null || echo "No session directory"

echo -e "\n=== Checking for socket ==="
ls -la ~/.sess/*.sock 2>/dev/null || echo "No socket files"

echo -e "\n=== Listing sessions ==="
./sess ls

echo -e "\n=== Cleanup ==="
killall sess 2>/dev/null || true