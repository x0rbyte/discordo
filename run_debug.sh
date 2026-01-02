#!/bin/bash
# Run discordo with debug logging enabled

echo "Starting discordo with debug logging..."
echo "Debug logs will be written to: ~/.cache/discordo/logs.txt"
echo ""

# Run the app with debug log level
# Pass token if provided as argument
if [ -n "$1" ]; then
    ./discordo --log-level=debug --token="$1"
else
    ./discordo --log-level=debug
fi

# After app exits, show the log location
echo ""
echo "App closed. View debug logs with:"
echo "  cat ~/.cache/discordo/logs.txt"
