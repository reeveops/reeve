# GCP WIF wiring recipe

Single-cloud GCP using Workload Identity Federation. GitHub Actions OIDC
→ GCP STS → service account impersonation.

## Before copying

This directory provides cloud setup, configuration, and a shared-workflow caller; it does not contain workload projects.
Use your existing Pulumi project and backend, then replace all project, service-account, bucket, and approver placeholders.

Set repository variables `REEVE_GCP_WIF_PROVIDER` and `REEVE_GCP_SERVICE_ACCOUNT` for the controller's Reeve bucket access.
The `.reeve/auth.yaml` bindings and `engine.state.auth_provider` supply engine credentials separately.

## One-time cloud setup

```bash
PROJECT_ID=mycompany-prod
PROJECT_NUMBER=$(gcloud projects describe $PROJECT_ID --format='value(projectNumber)')
REPO=myorg/myrepo

# Pool
gcloud iam workload-identity-pools create github \
  --project=$PROJECT_ID --location=global \
  --display-name="GitHub Actions"

# Provider (pinned to your repo)
gcloud iam workload-identity-pools providers create-oidc reeve \
  --project=$PROJECT_ID \
  --workload-identity-pool=github --location=global \
  --issuer-uri="https://token.actions.githubusercontent.com" \
  --attribute-mapping="google.subject=assertion.sub,attribute.repository=assertion.repository" \
  --attribute-condition="assertion.repository=='$REPO'"

# Service account for reeve
gcloud iam service-accounts create reeve-prod \
  --project=$PROJECT_ID \
  --display-name="reeve GitOps"

# Read-only SA for drift
gcloud iam service-accounts create reeve-drift-readonly \
  --project=$PROJECT_ID \
  --display-name="reeve drift (read-only)"

# Allow the WIF pool to impersonate both SAs
for sa in reeve-prod reeve-drift-readonly; do
  gcloud iam service-accounts add-iam-policy-binding \
    $sa@$PROJECT_ID.iam.gserviceaccount.com \
    --role=roles/iam.workloadIdentityUser \
    --member="principalSet://iam.googleapis.com/projects/$PROJECT_NUMBER/locations/global/workloadIdentityPools/github/attribute.repository/$REPO"
done

# Grant whatever project roles the stacks need (scope to least-privilege)
gcloud projects add-iam-policy-binding $PROJECT_ID \
  --member="serviceAccount:reeve-prod@$PROJECT_ID.iam.gserviceaccount.com" \
  --role=YOUR_WORKLOAD_ROLE

gcloud projects add-iam-policy-binding $PROJECT_ID \
  --member="serviceAccount:reeve-drift-readonly@$PROJECT_ID.iam.gserviceaccount.com" \
  --role=YOUR_READONLY_WORKLOAD_ROLE
```

## GCS bucket

```bash
gcloud storage buckets create gs://mycompany-reeve \
  --project=$PROJECT_ID \
  --location=us \
  --uniform-bucket-level-access

gcloud storage buckets add-iam-policy-binding gs://mycompany-reeve \
  --member="serviceAccount:reeve-prod@$PROJECT_ID.iam.gserviceaccount.com" \
  --role=roles/storage.objectAdmin
```

## Adjust configs

Search and replace:

- `mycompany-prod` → your project ID
- `111` → your project number
- `myorg/myrepo` → your repo
- `mycompany-reeve` → your bucket

## Validate and use

The named workload roles must cover the resources your project manages; grant state-backend/KMS access to the effective engine identities too.
Stack credentials override same-named backend credential variables, so the read-only drift identity still needs any backend permissions required by Pulumi refresh.

Run `reeve lint`, `reeve stacks`, and `reeve rules explain YOUR_PROJECT/prod`, then open one small workload PR.
Expect the controller to authenticate through the workflow inputs and the engine to authenticate through its explicit providers.

Keep IAM restrictions tied to the actual repository claims; organizations using customized immutable OIDC subjects must use their configured claim format.
When retiring a disposable trial, remove its bucket, service accounts, and trust bindings only after preserving anything you still need.
