#!/bin/bash
# =============================================================================
#           FINAL DEPLOY SCRIPT FOR THE DOCUMENT PROCESSING PIPELINE
# =============================================================================
# Deploys services for the orchestrated PDF processing pipeline. Each service
# is configured with only the environment variables it requires.
#
# USAGE:
#   ./scripts/deploy.sh [SERVICE_NAME]
#
# PREREQUISITE:
#   Run `source ./scripts/setup_env.sh` to load environment variables.
# =============================================================================

# Exit immediately if a command exits with a non-zero status.
set -e

# --- SCRIPT SETUP ---
# Ensure environment variables are loaded
if [ -z "$PROJECT_ID" ] || [ -z "$REGION" ]; then
    echo "ERROR: Core environment variables (PROJECT_ID, REGION) not set."
    echo "Please run 'source ./scripts/setup_env.sh' first."
    exit 1
fi

# Common flags for all Go functions
# FIX: The --source flag is now specific to each function, so it has been removed from here.
COMMON_FLAGS=(
    "--gen2"
    "--runtime=go123"
    "--project=${PROJECT_ID}"
    "--region=${REGION}"
    "--service-account=${SERVICE_ACCOUNT_EMAIL}"
    "--timeout=540s"
    "--memory=1Gi"
)

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
        --service-account="${SERVICE_ACCOUNT_EMAIL}" \
        --set-env-vars="PROJECT_ID=${PROJECT_ID},SPLIT_PAGES_BUCKET=${SPLIT_PAGES_BUCKET},TRANSLATOR_URL=${TRANSLATOR_URL},AGGREGATOR_URL=${AGGREGATOR_URL},CLEANER_URL=${CLEANER_URL},SPLITTER_URL=${SPLITTER_URL}"
}

# --- MAIN LOGIC ---

# Check if a service name was provided
if [ -z "$1" ]; then
    echo "Usage: $0 [SERVICE_NAME]"
    echo "Available services: pdf-splitter, page-translator, markdown-aggregator, markdown-cleaner, section-splitter, workflow, all"
    exit 1
fi

SERVICE_TO_DEPLOY=$1

deploy_pdf_splitter() {
    echo "--- Deploying pdf-splitter ---"
    gcloud functions deploy "pdf-splitter" \
        "${COMMON_FLAGS[@]}" \
        --entry-point="SplitAndPublish" \
        --source=./cmd/pdf-splitter \
        --trigger-resource="${UPLOADS_BUCKET}" \
        --trigger-event="google.cloud.storage.object.v1.finalized" \
        --set-env-vars="GOOGLE_CLOUD_PROJECT_ID=${PROJECT_ID},SPLIT_PAGES_BUCKET=${SPLIT_PAGES_BUCKET},FIRESTORE_COLLECTION=${FIRESTORE_COLLECTION},WORKFLOW_LOCATION=${WORKFLOW_LOCATION},WORKFLOW_ID=${WORKFLOW_ID}"
}

deploy_page_translator() {
    echo "--- Deploying page-translator ---"
    gcloud functions deploy "page-translator" \
        "${COMMON_FLAGS[@]}" \
        --entry-point="HandleTranslatePage" \
        --source=./cmd/page-translator \
        --trigger-http \
        --no-allow-unauthenticated \
        --set-env-vars="GOOGLE_CLOUD_PROJECT_ID=${PROJECT_ID},VERTEX_AI_REGION=${VERTEX_AI_REGION},TRANSLATED_MARKDOWN_BUCKET=${TRANSLATED_MARKDOWN_BUCKET}"
}

deploy_markdown_aggregator() {
    echo "--- Deploying markdown-aggregator ---"
    gcloud functions deploy "markdown-aggregator" \
        "${COMMON_FLAGS[@]}" \
        --entry-point="HandleAggregateMarkdown" \
        --source=./cmd/markdown-aggregator \
        --trigger-http \
        --no-allow-unauthenticated \
        --set-env-vars="GOOGLE_CLOUD_PROJECT_ID=${PROJECT_ID},TRANSLATED_MARKDOWN_BUCKET=${TRANSLATED_MARKDOWN_BUCKET},AGGREGATED_MARKDOWN_BUCKET=${AGGREGATED_MARKDOWN_BUCKET}"
}

deploy_markdown_cleaner() {
    echo "--- Deploying markdown-cleaner ---"
    gcloud functions deploy "markdown-cleaner" \
        "${COMMON_FLAGS[@]}" \
        --entry-point="HandleCleanMarkdown" \
        --source=./cmd/markdown-cleaner \
        --trigger-http \
        --no-allow-unauthenticated \
        --set-env-vars="GOOGLE_CLOUD_PROJECT_ID=${PROJECT_ID},VERTEX_AI_REGION=${VERTEX_AI_REGION},CLEANED_MARKDOWN_BUCKET=${CLEANED_MARKDOWN_BUCKET}"
}

deploy_section_splitter() {
    echo "--- Deploying section-splitter ---"
    gcloud functions deploy "section-splitter" \
        "${COMMON_FLAGS[@]}" \
        --entry-point="HandleSplitSections" \
        --source=./cmd/section-splitter \
        --trigger-http \
        --no-allow-unauthenticated \
        --set-env-vars="GOOGLE_CLOUD_PROJECT_ID=${PROJECT_ID},VERTEX_AI_REGION=${VERTEX_AI_REGION},FINAL_SECTIONS_BUCKET=${FINAL_SECTIONS_BUCKET}"
}

case "$SERVICE_TO_DEPLOY" in
    pdf-splitter)
        deploy_pdf_splitter
        ;;
    page-translator)
        deploy_page_translator
        ;;
    markdown-aggregator)
        deploy_markdown_aggregator
        ;;
    markdown-cleaner)
        deploy_markdown_cleaner
        ;;
    section-splitter)
        deploy_section_splitter
        ;;
    workflow)
        deploy_workflow
        ;;
    all)
        echo "Deploying all services..."
        deploy_pdf_splitter
        deploy_page_translator
        deploy_markdown_aggregator
        deploy_markdown_cleaner
        deploy_section_splitter
        deploy_workflow
        ;;
    *)
        echo "Error: Unknown service '$SERVICE_TO_DEPLOY'."
        exit 1
        ;;
esac

echo ""
echo "✅ Deployment script finished for '$SERVICE_TO_DEPLOY'."