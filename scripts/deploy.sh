#!/bin/bash
# =============================================================================
#           UNIFIED DEPLOY SCRIPT FOR THE DOCUMENT PROCESSING PIPELINE
# =============================================================================
# Deploys services for the orchestrated PDF processing pipeline.
#
# USAGE:
#   ./scripts/deploy.sh [SERVICE_NAME]
#
# EXAMPLES:
#   ./scripts/deploy.sh pdf-splitter       # Deploys only the pdf-splitter function
#   ./scripts/deploy.sh page-translator    # Deploys only the page-translator function
#   ./scripts/deploy.sh workflow           # Deploys only the Cloud Workflow
#   ./scripts/deploy.sh all                # Deploys all functions and the workflow
#
# PREREQUISITE:
#   Run `source ./scripts/setup_env.sh` to load environment variables.
# =============================================================================

# Exit immediately if a command exits with a non-zero status.
set -e

# --- SCRIPT SETUP ---
# Ensure environment variables are loaded
if [ -z "$PROJECT_ID" ]; then
    echo "ERROR: Environment variables not set. Please run 'source ./scripts/setup_env.sh' first."
    exit 1
fi

# --- HELPER FUNCTIONS ---

# Deploys a Go Cloud Function from the monorepo
# $1: Function name (e.g., pdf-splitter)
# $2: Trigger type ('gcs' or 'http')
deploy_go_function() {
    local FUNCTION_NAME=$1
    local TRIGGER_TYPE=$2
    local ENTRY_POINT="main" # All our HTTP functions will have a main entry point

    echo ""
    echo "------------------------------------------------------------"
    echo "Deploying Go Function: ${FUNCTION_NAME}"
    echo "------------------------------------------------------------"

    # Set common deploy flags
    local DEPLOY_FLAGS=(
        "--gen2"
        "--runtime=go1.22"
        "--project=${PROJECT_ID}"
        "--region=${REGION}"
        "--source=." # CORRECT: Source is the repo root for Go modules
        "--service-account=${SERVICE_ACCOUNT_EMAIL}"
        "--timeout=540s"
        "--memory=1Gi"
        "--update-labels=app=pdf-pipeline,service=${FUNCTION_NAME}"
        # This tells Cloud Build where to find the function's main package
        "--set-build-env-vars=GOPACKAGE=github.com/Lllllllleong/engineeringdocumentflow/cmd/${FUNCTION_NAME}"
    )

    # Set trigger-specific flags
    if [ "$TRIGGER_TYPE" == "gcs" ]; then
        # This is for the first function in the pipeline
        DEPLOY_FLAGS+=(
            "--entry-point=SplitAndPublish"
            "--trigger-resource=${UPLOADS_BUCKET}"
            "--trigger-event=google.cloud.storage.object.v1.finalized"
            "--set-env-vars=SPLIT_PAGES_BUCKET=${SPLIT_PAGES_BUCKET},FIRESTORE_COLLECTION=${FIRESTORE_COLLECTION},WORKFLOW_LOCATION=${WORKFLOW_LOCATION},WORKFLOW_ID=${WORKFLOW_ID}"
        )
    elif [ "$TRIGGER_TYPE" == "http" ]; {
        # This is for all the "worker" functions called by the workflow
        DEPLOY_FLAGS+=(
            "--trigger-http"
            "--no-allow-unauthenticated"
            "--set-env-vars=MARKDOWN_BUCKET=${MARKDOWN_BUCKET},AGGREGATED_BUCKET=${AGGREGATED_BUCKET},CLEANED_BUCKET=${CLEANED_BUCKET},FINAL_SECTIONS_BUCKET=${FINAL_SECTIONS_BUCKET},VERTEX_AI_REGION=${VERTEX_AI_REGION},GEMINI_MODEL_NAME=${GEMINI_MODEL_NAME}"
        )
    }
    else
        echo "ERROR: Invalid trigger type '${TRIGGER_TYPE}' for ${FUNCTION_NAME}."
        exit 1
    fi

    gcloud functions deploy "${FUNCTION_NAME}" "${DEPLOY_FLAGS[@]}"
}

# Deploys the Cloud Workflow definition
deploy_workflow() {
    echo ""
    echo "------------------------------------------------------------"
    echo "Deploying Cloud Workflow: document-processing-orchestrator"
    echo "------------------------------------------------------------"

    gcloud workflows deploy document-processing-orchestrator \
        --project="${PROJECT_ID}" \
        --location="${REGION}" \
        --source=./orchestration/document-processing-orchestrator.yaml \
        --service-account="${SERVICE_ACCOUNT_EMAIL}"
}

# --- MAIN LOGIC ---

# Check if a service name was provided
if [ -z "$1" ]; then
    echo "Usage: $0 [SERVICE_NAME]"
    echo "Available services: pdf-splitter, page-translator, markdown-aggregator, markdown-cleaner, section-splitter, workflow, all"
    exit 1
fi

case "$1" in
    pdf-splitter)
        deploy_go_function "pdf-splitter" "gcs"
        ;;
    page-translator)
        deploy_go_function "page-translator" "http"
        ;;
    markdown-aggregator)
        deploy_go_function "markdown-aggregator" "http"
        ;;
    markdown-cleaner)
        deploy_go_function "markdown-cleaner" "http"
        ;;
    section-splitter)
        deploy_go_function "section-splitter" "http"
        ;;
    workflow)
        deploy_workflow
        ;;
    all)
        echo "Deploying all services..."
        deploy_go_function "pdf-splitter" "gcs"
        deploy_go_function "page-translator" "http"
        deploy_go_function "markdown-aggregator" "http"
        deploy_go_function "markdown-cleaner" "http"
        deploy_go_function "section-splitter" "http"
        deploy_workflow
        ;;
    *)
        echo "Error: Unknown service '$1'."
        echo "Available services: pdf-splitter, page-translator, markdown-aggregator, markdown-cleaner, section-splitter, workflow, all"
        exit 1
        ;;
esac

echo ""
echo "✅ Deployment script finished for '$1'."