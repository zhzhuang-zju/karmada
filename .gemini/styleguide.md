**Purpose:** This document outlines the specific code style and quality rules for the Karmada project. When reviewing code or generating PR descriptions, Gemini should enforce these rules to ensure consistency and maintainability.

> **Note:** These rules supplement the official Go style guide. Where this guide is silent, standard Go best practices apply.

## code review

Please follow the coding guidelines defined in `CODE_STYLE_GUIDE.md` file located in the root of this repository.

## pr descriptions review

Please follow the pull request template defined in `.github/PULL_REQUEST_TEMPLATE.md`.

When reviewing a PR description, please perform the following checks:

1.  **PR Type Validation:**
    *   If the labels are missing or seem incorrect based on the PR's content, suggest the correct labels.

2.  **What this PR does / why we need it section:**
    *   Verify that the "What this PR does / why we need it" section is not empty.
    *   If this section is empty, suggest that the author provide a clear and concise description of the PR's purpose and the problem it solves.

3.  **Release Note Requirement:**
    *   Check the "Does this PR introduce a user-facing change?" section.
    *   If the answer is "yes" or if you determine that the PR introduces a change that will be noticeable to end-users (e.g., UI changes, API modifications, behavior changes), then a release note is required.
    *   Verify that the `release-note` block contains a clear and concise description of the change.
    *   If the `release-note` block is empty or contains "NONE", and you believe a release note is necessary, you should:
        *   Point out that the PR appears to have a user-facing change.
        *   Suggest a well-written release note that accurately describes the change. The release note should follow the examples provided in the PR template.
