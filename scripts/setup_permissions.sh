#!/bin/bash
# =============================================================================
#          PERMISSIONS SETUP SCRIPT FOR DOCUMENT PROCESSING PIPELINE
# =============================================================================
#
# USAGE:
# 1. First, run `source ./scripts/setup_env.sh` to load project variables.
# 2. Then, run this script directly: `./scripts/setup_permissions.sh`
#
# WHAT IT DOES:
# 1. Creates the pipeline's central Service Account if it doesn't exist.
# 2. Grants the Service Account the broad 'Editor' role for rapid development.
#
# =============================================================================

# Exit immediately if a command exits with a non-zero status.
set -e

# --- SCRIPT SETUP ---
# Ensure environment variables are loaded
if [ -z "$PROJECT_ID" ] || [ -z "$SERVICE_ACCOUNT_EMAIL" ]; then
    echo "ERROR: Environment variables not set. Please run 'source ./scripts/setup_env.sh' first."
    exit 1
fi

echo "Setting up permissions for project: ${PROJECT_ID}"
echo ""

# --- 1. Create the Service Account (if it doesn't exist) ---
echo "--> Checking for Service Account: ${SERVICE_ACCOUNT_EMAIL}..."

# The '||' operator ensures the create command only runs if the describe command fails (i.e., SA does not exist)
gcloud iam service-accounts describe "${SERVICE_ACCOUNT_EMAIL}" --project="${PROJECT_ID}" >/dev/null 2>&1 || {
  echo "    Service Account not found. Creating it now..."
  gcloud iam service-accounts create "pipeline-runner-sa" \
    --display-name="Pipeline Runner Service Account" \
    --project="${PROJECT_ID}"
  echo "    Service Account created."
}

echo "    Service Account exists."
echo ""


# --- 2. Grant Broad Permissions for Development ---
echo "--> Granting TEMPORARY development permissions..."

# This is the ONLY permission binding we need during this phase.
# The 'editor' role includes all other permissions required.
# The command is idempotent; it won't add a duplicate if it already exists.
gcloud projects add-iam-policy-binding "${PROJECT_ID}" \
    --member="serviceAccount:${SERVICE_ACCOUNT_EMAIL}" \
    --role="roles/editor"

echo "    Successfully granted 'roles/editor' to ${SERVICE_ACCOUNT_EMAIL}."
echo ""

# --- 3. IMPORTANT: Final Warning and Next Steps ---
echo "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!  WARNING  !!!!!!!!!!!!!!!!!!!!!!!!!!!!!!"
echo "The 'Editor' role is highly privileged and NOT SUITABLE for production."
echo "This is a temporary measure to accelerate initial development."
echo ""
echo "MANDATORY PRE-PRODUCTION TASK: We must replace this with a set of"
echo "fine-grained roles based on the Principle of Least Privilege."
echo "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!"
echo ""
echo "✅ Permissions setup complete."