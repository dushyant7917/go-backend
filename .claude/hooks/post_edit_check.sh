#!/bin/bash
set -euo pipefail

cd /Users/dushyant7917/D7/go-backend

INPUT=$(cat)
FILE=$(echo "$INPUT" | jq -r '.tool_input.file_path // empty' 2>/dev/null)

# Only process Go files
if ! echo "$FILE" | grep -qE '\.go$'; then
    exit 0
fi

# Run go build to check for compilation errors
BUILD_OUT=$(go build ./... 2>&1 || true)

# Get git diff for the changed file
DIFF_OUT=$(git diff -- "$FILE" 2>/dev/null | head -150)

# Build context for Claude to review
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

# Inject context back to Claude as additionalContext
printf '%s' "$CONTEXT" | jq -Rs '{hookSpecificOutput: {hookEventName: "PostToolUse", additionalContext: .}}'
