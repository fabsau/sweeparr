#!/usr/bin/env bash
set -euo pipefail

# Applies GitHub repository security settings, rulesets (protecting main),
# squash-only merges, auto-merge for Dependabot, and vulnerability scanning
# to https://github.com/fabsau/sweeparr.
#
# Prerequisites:
#   gh auth login  (with repo + admin:repo_hook / security_events scopes)

REPO="${1:-fabsau/sweeparr}"

echo "Configuring repository settings for ${REPO}..."
gh api -X PATCH "repos/${REPO}" \
  --field allow_squash_merge=true \
  --field allow_merge_commit=false \
  --field allow_rebase_merge=false \
  --field allow_auto_merge=true \
  --field delete_branch_on_merge=true \
  --field squash_merge_commit_title=PR_TITLE \
  --field squash_merge_commit_message=PR_BODY \
  --field security_and_analysis[secret_scanning][status]=enabled \
  --field security_and_analysis[secret_scanning_push_protection][status]=enabled \
  --field security_and_analysis[dependabot_security_updates][status]=enabled \
  >/dev/null

echo "Enabling Dependabot vulnerability alerts..."
gh api -X PUT "repos/${REPO}/vulnerability-alerts" >/dev/null

echo "Enabling automated security fixes..."
gh api -X PUT "repos/${REPO}/automated-security-fixes" >/dev/null

echo "Applying repository ruleset to protect main (PR required, no force push, CI checks required)..."
RULESET_PAYLOAD=$(cat << 'JSON'
{
  "name": "Protect main",
  "target": "branch",
  "enforcement": "active",
  "conditions": {
    "ref_name": {
      "include": ["refs/heads/main"],
      "exclude": []
    }
  },
  "rules": [
    { "type": "deletion" },
    { "type": "non_fast_forward" },
    { "type": "required_linear_history" },
    { "type": "required_signatures" },
    {
      "type": "pull_request",
      "parameters": {
        "required_approving_review_count": 1,
        "dismiss_stale_reviews_on_push": true,
        "require_code_owner_review": true,
        "require_last_push_approval": false,
        "required_review_thread_resolution": true
      }
    },
    {
      "type": "required_status_checks",
      "parameters": {
        "strict_required_status_checks_policy": true,
        "required_status_checks": [
          { "context": "Frontend Build & Locale Parity" },
          { "context": "Go Vet & Race Tests" },
          { "context": "Docker Build Check" }
        ]
      }
    }
  ]
}
JSON
)

EXISTING_ID=$(gh api "repos/${REPO}/rulesets" --jq '.[] | select(.name=="Protect main") | .id' 2>/dev/null || true)
if [ -n "${EXISTING_ID}" ]; then
  gh api -X PUT "repos/${REPO}/rulesets/${EXISTING_ID}" --input - <<< "${RULESET_PAYLOAD}" >/dev/null
  echo "Updated existing ruleset #${EXISTING_ID}."
else
  gh api -X POST "repos/${REPO}/rulesets" --input - <<< "${RULESET_PAYLOAD}" >/dev/null
  echo "Created ruleset 'Protect main'."
fi

echo "Done! ${REPO} is configured with branch protection, auto-merge, and security scanning."
