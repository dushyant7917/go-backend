#!/bin/bash
project_root="/Users/dushyant7917/D7/go-backend"
input=$(cat)
cmd=$(echo "$input" | jq -r '.tool_input.command // empty')

# Clean bin/
find "$project_root/bin" -maxdepth 1 -type f -delete 2>/dev/null || true

# Without -o, go build drops the binary in the current directory named after the package dir
if ! echo "$cmd" | grep -qE ' -o[ =]'; then
    pkg_path=$(echo "$cmd" | grep -oE '\./[^ ]+' | head -1)
    if [ -n "$pkg_path" ]; then
        binary_name=$(basename "${pkg_path%/...}")
        rm -f "$project_root/$binary_name" 2>/dev/null || true
    fi
fi
