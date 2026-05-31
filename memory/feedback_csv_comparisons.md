---
name: feedback-csv-comparisons
description: Always trim and lowercase string values before comparisons when reading CSV data
metadata:
  type: feedback
---

Always use `strings.TrimSpace(strings.ToLower(...))` before comparing string values read from CSV cells (e.g. boolean-like fields such as "true"/"True"/"TRUE", status flags, etc.).

**Why:** User confirmed this is the right approach — keeps comparisons robust against inconsistent casing and whitespace in CSV input.

**How to apply:** Any time a CSV cell value is compared against a string literal, normalize it first. Write canonical lowercase values back out (e.g. `"true"` not `"True"` or `"TRUE"`).
