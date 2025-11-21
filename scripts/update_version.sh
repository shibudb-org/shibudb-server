#!/bin/bash

# Script to update version in CHANGELOG.txt
# Usage: ./scripts/update_version.sh <new_version>
# Example: ./scripts/update_version.sh 1.1.0

CHANGELOG_FILE="CHANGELOG.txt"

if [ $# -ne 1 ]; then
    echo "Usage: $0 <new_version>"
    echo "Example: $0 1.1.0"
    exit 1
fi

NEW_VERSION="$1"

# Validate version format (simple check for x.y.z format)
if ! [[ $NEW_VERSION =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "Error: Version must be in format x.y.z (e.g., 1.1.0)" >&2
    exit 1
fi

if [ ! -f "$CHANGELOG_FILE" ]; then
    echo "Error: $CHANGELOG_FILE not found" >&2
    exit 1
fi

# Get current version
CURRENT_VERSION=$(./scripts/get_version.sh)

echo "Current version: $CURRENT_VERSION"
echo "New version: $NEW_VERSION"
echo ""

# Update the "Current Version:" line
sed -i.bak "s/^## Current Version: .*/## Current Version: $NEW_VERSION/" "$CHANGELOG_FILE"

# Remove backup file
rm -f "${CHANGELOG_FILE}.bak"

echo "✅ Version updated from $CURRENT_VERSION to $NEW_VERSION in $CHANGELOG_FILE"
echo ""
echo "Don't forget to:"
echo "1. Add a new changelog entry for version $NEW_VERSION"
echo "2. Commit your changes"
echo "3. Tag the release: git tag v$NEW_VERSION" 