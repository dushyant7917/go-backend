#!/bin/bash
set -euo pipefail

cd /Users/dushyant7917/D7/go-backend

# Get all modified Go files (staged + unstaged)
GO_FILES=$(git diff --name-only HEAD 2>/dev/null | grep '\.go$' || true)

if [ -z "$GO_FILES" ]; then
    exit 0
fi

# Run go build to check for compilation errors
BUILD_OUT=$(go build ./... 2>&1 || true)

# Get git diff for all changed Go files (capped at 300 lines)
DIFF_OUT=$(git diff HEAD -- $GO_FILES 2>/dev/null | head -300)

CONTEXT=""
if [ -n "$BUILD_OUT" ]; then
    CONTEXT="=== BUILD ERRORS (fix these) ===\n${BUILD_OUT}\n\n"
fi
if [ -n "$DIFF_OUT" ]; then
    CONTEXT="${CONTEXT}=== CODE CHANGES (review for correctness, style, regression and issues) ===\n${DIFF_OUT}\n\nAlso check the diff for modularity issues: duplicated logic that should be extracted (DRY), large functions doing too many things, hardcoded values that should be parameterised, and abstractions that make the code harder to extend or change in future."
fi

if [ -z "$CONTEXT" ]; then
    exit 0
fi

printf '%s' "$CONTEXT" | jq -Rs '{hookSpecificOutput: {hookEventName: "Stop", additionalContext: .}}'
