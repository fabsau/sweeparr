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
        "required_approving_review_count": 0,
        "dismiss_stale_reviews_on_push": false,
        "require_code_owner_review": false,
        "require_last_push_approval": false,
        "required_review_thread_resolution": true
      }
    },
    {
      "type": "required_status_checks",
      "parameters": {
        "strict_required_status_checks_policy": true,
        "required_status_checks": [
          {"context": "Frontend Build & Locale Parity", "integration_id": 15368},
          {"context": "Go Vet & Race Tests", "integration_id": 15368},
          {"context": "Docker Build Check", "integration_id": 15368},
          {"context": "Frontend Dependency Audit", "integration_id": 15368},
          {"context": "Go Vulnerability Check", "integration_id": 15368},
          {"context": "Secret Scan (Gitleaks)", "integration_id": 15368},
          {"context": "CodeQL (go)", "integration_id": 15368},
          {"context": "CodeQL (javascript-typescript)", "integration_id": 15368},
          {"context": "Trivy Filesystem & IaC Scan", "integration_id": 15368},
          {"context": "CodeQL", "integration_id": 57789},
          {"context": "Trivy", "integration_id": 57789}
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

echo "Applying tag ruleset to protect release tags (v*)..."
TAG_RULESET_PAYLOAD=$(cat << 'JSON'
{
  "name": "Protect release tags",
  "target": "tag",
  "enforcement": "active",
  "conditions": {
    "ref_name": {
      "include": ["refs/tags/v*"],
      "exclude": []
    }
  },
  "rules": [
    { "type": "deletion" },
    { "type": "non_fast_forward" }
  ]
}
JSON
)

EXISTING_TAG_ID=$(gh api "repos/${REPO}/rulesets" --jq '.[] | select(.name=="Protect release tags") | .id' 2>/dev/null || true)
if [ -n "${EXISTING_TAG_ID}" ]; then
  gh api -X PUT "repos/${REPO}/rulesets/${EXISTING_TAG_ID}" --input - <<< "${TAG_RULESET_PAYLOAD}" >/dev/null
  echo "Updated existing tag ruleset #${EXISTING_TAG_ID}."
else
  gh api -X POST "repos/${REPO}/rulesets" --input - <<< "${TAG_RULESET_PAYLOAD}" >/dev/null
  echo "Created ruleset 'Protect release tags'."
fi

echo "Hardening GitHub Actions permissions (selected actions + mandatory SHA pinning)..."
gh api -X PUT "repos/${REPO}/actions/permissions" \
  --field enabled=true \
  --field allowed_actions="selected" \
  --field sha_pinning_required=true \
  >/dev/null

gh api -X PUT "repos/${REPO}/actions/permissions/selected-actions" \
  --input - << 'JSON' >/dev/null
{
  "github_owned_allowed": true,
  "verified_allowed": false,
  "patterns_allowed": [
    "aquasecurity/trivy-action@*",
    "aquasecurity/setup-trivy@3fb12ec12f41e471780db15c232d5dd185dcb514",
    "dependabot/fetch-metadata@*",
    "docker/build-push-action@*",
    "docker/login-action@*",
    "docker/metadata-action@*",
    "docker/setup-buildx-action@*",
    "docker/setup-qemu-action@*",
    "googleapis/release-please-action@*"
  ]
}
JSON

gh api -X PUT "repos/${REPO}/actions/permissions/workflow" \
  --field default_workflow_permissions="read" \
  --field can_approve_pull_request_reviews=true \
  >/dev/null

gh api -X PUT "repos/${REPO}/actions/permissions/fork-pr-contributor-approval" \
  --field approval_policy="all_external_contributors" \
  >/dev/null

echo "Done! ${REPO} is configured with branch/tag protection, SHA-pinned Actions, auto-merge, and security scanning."
