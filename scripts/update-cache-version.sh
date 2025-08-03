#!/bin/bash

# Manual Cache Version Update Script
# Run this script to manually update the cache version

echo "🚀 Updating Tennis Tournament Finder cache version..."

# Generate timestamp-based version
TIMESTAMP=$(date -u +"%Y%m%d-%H%M%S")
COMMIT_SHORT=$(git rev-parse --short HEAD 2>/dev/null || echo "manual")
NEW_VERSION="v${TIMESTAMP}-${COMMIT_SHORT}"

echo "New cache version: ${NEW_VERSION}"

# Update service-worker.js
if [ -f "service-worker.js" ]; then
    # Update the CACHE_VERSION in service-worker.js
    if [[ "$OSTYPE" == "darwin"* ]]; then
        # macOS
        sed -i '' "s/const CACHE_VERSION = 'v[^']*';/const CACHE_VERSION = '${NEW_VERSION}';/" service-worker.js
    else
        # Linux
        sed -i "s/const CACHE_VERSION = 'v[^']*';/const CACHE_VERSION = '${NEW_VERSION}';/" service-worker.js
    fi
    echo "✅ Updated service-worker.js"
else
    echo "❌ service-worker.js not found"
    exit 1
fi

# Update manifest.json
if [ -f "manifest.json" ]; then
    if [[ "$OSTYPE" == "darwin"* ]]; then
        # macOS
        sed -i '' "s/\"version\": \"[^\"]*\"/\"version\": \"${NEW_VERSION}\"/" manifest.json
    else
        # Linux
        sed -i "s/\"version\": \"[^\"]*\"/\"version\": \"${NEW_VERSION}\"/" manifest.json
    fi
    echo "✅ Updated manifest.json"
fi

echo ""
echo "📄 Current versions:"
grep "const CACHE_VERSION" service-worker.js
grep "\"version\"" manifest.json

echo ""
echo "🎉 Cache version update complete!"
echo "💡 Commit and push these changes to deploy the update."
