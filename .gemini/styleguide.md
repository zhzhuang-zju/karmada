**Purpose:** This document outlines the specific code style and quality rules for the Karmada project. When reviewing code or generating PR descriptions, Gemini should enforce these rules to ensure consistency and maintainability.

> **Note:** These rules supplement the official Go style guide. Where this guide is silent, standard Go best practices apply.

## code review

Please follow the coding guidelines defined in `CODE_STYLE_GUIDE.md` file located in the root of this repository.

## pr descriptions review

Please follow the pull request template defined in `.github/PULL_REQUEST_TEMPLATE.md`.

When reviewing a PR description, please perform the following checks and **provide specific, actionable suggestions** instead of just pointing out problems.

1.  **PR Type Validation:**
    *   If the `/kind` labels are missing or seem incorrect, **propose specific labels** based on the PR's content.
    *   *Example Suggestion:* "This PR introduces a new API. I suggest adding the following labels: `/kind api-change`."

2.  **"What this PR does / why we need it" section:**
    *   If this section is empty, **propose a concise description** based on the PR's summary.
    *   *Example Suggestion:* "The description section is empty. Based on the changes, I suggest the following description: 
	`This PR introduces a new API ...`"

3.  **Release Note Requirement:**
    *   Check the "Does this PR introduce a user-facing change?" section.
    *   If the answer is "yes" or if you determine that the PR introduces a change that will be noticeable to end-users (e.g., UI changes, API modifications, behavior changes), then a release note is required.
    *   Verify that the `release-note` block contains a clear and concise description of the change.
    *   If the `release-note` block is empty or contains "NONE", and you believe a release note is necessary, you should:
        *   Point out that the PR appears to have a user-facing change.
        *   Suggest a well-written release note that accurately describes the change. The release note should follow the examples provided in the PR template.
   	 	*   *Example Suggestion:* "This PR introduces a user-facing API change and requires a release note. I suggest the following:
   		 ```release-note
    		API Change: Introduced `spec.affinity.clusterAffinity.affinityTerm.supplements` to the `PropagationPolicy` and `ClusterPropagationPolicy` APIs to support cascading cluster affinity scheduling.
    	```"
