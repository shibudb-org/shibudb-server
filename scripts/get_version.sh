#!/bin/bash

# Script to extract current version from CHANGELOG.txt
# Usage: ./scripts/get_version.sh

CHANGELOG_FILE="CHANGELOG.txt"

if [ ! -f "$CHANGELOG_FILE" ]; then
    echo "Error: $CHANGELOG_FILE not found" >&2
    exit 1
fi

# Extract the current version from the last line that starts with "## Current Version:"
VERSION=$(grep "^## Current Version:" "$CHANGELOG_FILE" | tail -1 | sed 's/^## Current Version: //')

if [ -z "$VERSION" ]; then
    echo "Error: Could not find current version in $CHANGELOG_FILE" >&2
    exit 1
fi

echo "$VERSION" 