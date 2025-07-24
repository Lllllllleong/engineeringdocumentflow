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
# 2. Grants the broad 'Editor' role to all necessary service accounts
#    to overcome common permission issues during development.
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

echo "Setting up broad development permissions for project: ${PROJECT_ID}"
echo ""

# --- 1. Identify all required service accounts ---
export PROJECT_NUMBER=$(gcloud projects describe "${PROJECT_ID}" --format="value(projectNumber)")
export COMPUTE_SERVICE_ACCOUNT="${PROJECT_NUMBER}-compute@developer.gserviceaccount.com"
export EVENTARC_SERVICE_AGENT="service-${PROJECT_NUMBER}@gcp-sa-eventarc.iam.gserviceaccount.com"


# --- 2. Create the primary Service Account (if it doesn't exist) ---
echo "--> Checking for Service Account: ${SERVICE_ACCOUNT_EMAIL}..."

# The '||' operator ensures the create command only runs if the describe command fails
gcloud iam service-accounts describe "${SERVICE_ACCOUNT_EMAIL}" --project="${PROJECT_ID}" >/dev/null 2>&1 || {
  echo "    Service Account not found. Creating it now..."
  gcloud iam service-accounts create "pipeline-runner-sa" \
    --display-name="Pipeline Runner Service Account" \
    --project="${PROJECT_ID}"
  echo "    Service Account created."
}
echo "    Service Account exists."
echo ""


# --- 3. Grant Broad Permissions for Development ---
echo "--> Granting TEMPORARY 'Editor' role to all pipeline-related service accounts..."

# Grant Editor to your primary SA
echo "    Granting Editor to ${SERVICE_ACCOUNT_EMAIL}..."
gcloud projects add-iam-policy-binding "${PROJECT_ID}" \
    --member="serviceAccount:${SERVICE_ACCOUNT_EMAIL}" \
    --role="roles/editor" \
    --condition=None >/dev/null

# Grant Editor to the Compute Engine default SA (used by Eventarc)
echo "    Granting Editor to ${COMPUTE_SERVICE_ACCOUNT}..."
gcloud projects add-iam-policy-binding "${PROJECT_ID}" \
    --member="serviceAccount:${COMPUTE_SERVICE_ACCOUNT}" \
    --role="roles/editor" \
    --condition=None >/dev/null

# Grant Editor to the Eventarc Service Agent
echo "    Granting Editor to ${EVENTARC_SERVICE_AGENT}..."
gcloud projects add-iam-policy-binding "${PROJECT_ID}" \
    --member="serviceAccount:${EVENTARC_SERVICE_AGENT}" \
    --role="roles/editor" \
    --condition=None >/dev/null

echo ""
echo "    Successfully granted 'roles/editor' to all service accounts."
echo ""

# --- 4. IMPORTANT: Final Warning and Next Steps ---
echo "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!  WARNING  !!!!!!!!!!!!!!!!!!!!!!!!!!!!!!"
echo "The 'Editor' role is highly privileged and NOT SUITABLE for production."
echo "This is a temporary measure to accelerate initial development."
echo ""
echo "MANDATORY PRE-PRODUCTION TASK: You MUST replace this with a set of"
echo "fine-grained roles based on the Principle of Least Privilege."
echo "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!"
echo ""
echo "✅ Permissions setup complete."