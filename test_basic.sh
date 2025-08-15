#!/bin/bash

echo "=== Basic sess test ==="

# Clean build
make clean
make build

echo -e "\n1. Testing session creation (run ./sess in a terminal)"
echo "2. Testing session listing:"
./sess ls

echo -e "\n3. To test attach/detach:"
echo "   - Run: ./sess -a 001"
echo "   - Press Ctrl-X to detach"

echo -e "\n4. To kill a session:"
echo "   - Run: ./sess -k 001"

echo -e "\nNote: The tool requires a terminal for interactive sessions."
echo "Run these commands in your terminal to test the full functionality."