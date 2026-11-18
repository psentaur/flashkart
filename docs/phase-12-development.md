# Phase 12 AWS deployment

Phase 12 separates reusable Terraform state storage and immutable ECR artifacts from the disposable application environment. The environment uses private EKS workers in two availability zones, five Single-AZ RDS instances, MSK Serverless, SQS and DLQs, DynamoDB, Lambda, and CloudFront with private S3 and ALB origins. All EKS workloads use the same binaries and migrations as the kind environment.

## Infrastructure lifecycle script

Use the Bash wrapper from any working directory. Copy its configuration and fill in the account ID, EKS version, restricted operator CIDRs, image revision, and (when available) the production Clerk publishable key:

```sh
cp deploy/aws/infra.env.example .env.cloud
# Edit .env.cloud, then authenticate the AWS CLI for that account.
bash deploy/aws/infra.sh bootstrap
bash deploy/aws/infra.sh images
bash deploy/aws/infra.sh plan
bash deploy/aws/infra.sh create
# Later, remove the application environment and its data:
bash deploy/aws/infra.sh destroy
```

`bootstrap` creates the shared state bucket and immutable ECR repository. `images` uses the existing Maven/container build pipeline. When the Clerk public key is empty it builds backend images only, so infrastructure can be created before configuring production login. Once the key is set, rerun `images` to include the web image and frontend assets; existing immutable image tags are reused. Use a new `IMAGE_TAG` when application source changes. `FLASHKART_IMAGE_SCOPE=backend` explicitly selects backend images even when a Clerk key is present; its prerequisites are listed below. `create` requires the matching notification image because Terraform creates a container-image Lambda. It initializes S3 state under `flashkart/$FLASHKART_CLOUD_NAME.tfstate`, validates the selected AWS account and state environment, and lets Terraform show and confirm the apply plan. After a successful `create`, the script prints the CloudFront website URL and all NAT gateway Elastic IP addresses. Open the HTTPS CloudFront URL in your browser; NAT Elastic IPs only handle outbound traffic. The website becomes available after the frontend is published and the application services are deployed. Infrastructure provisioning is separate from application deployment through `make cloud-render` and `make demo`.

If `kubectl` is missing, the destroy script downloads the pinned client from Kubernetes, verifies its SHA-256 checksum, and caches it under `build/cloud-tools` without sudo. It prefers an existing client on `PATH` and reuses the cache on later runs. A failed download or checksum stops cleanup before deleting workloads.

`destroy` asks you to type the environment name before deleting anything, removes Argo workloads before their controllers through the existing teardown script, then destroys Terraform resources. If EKS was never created or is already absent, it destroys the remaining partial infrastructure directly. AWS/Kubernetes permission or connectivity failures stop cleanup rather than skipping it. The shared state bucket and ECR repository are retained by the existing bootstrap lifecycle protection. Keep the bootstrap's local Terraform state for future runs.

Use `--config /absolute/path/to/config` for another trusted Bash configuration file and `--yes` for unattended apply/destruction. `.env.cloud` and Terraform state are ignored by Git. `AWS_PROFILE` selects a profile. When using infrastructure previously initialized by the manual instructions, set `STATE_KEY` to the existing key (for example `flashkart/demo.tfstate`) rather than creating a second state. Existing shared bootstrap resources can be selected with `STATE_BUCKET` and `ARTIFACT_REPOSITORY`.

## RDS capacity in another Availability Zone

If RDS reports `InvalidVPCNetworkStateFault` and names an AZ outside the two existing VPC subnets, set `DATABASE_AVAILABILITY_ZONE` in `.env.cloud` to the AZ reported by AWS (for example `ap-south-1c`). The wrapper passes it to Terraform's `database_availability_zone` variable. Terraform adds a dedicated private database subnet at `10.42.64.0/20`, includes it in the existing DB subnet group, and places the five Single-AZ databases in the selected AZ. EKS, Kafka, existing subnet CIDRs, and NAT placement are unchanged. The database subnet uses the VPC's local route for application connectivity and does not require another NAT gateway.

Run `bash deploy/aws/infra.sh plan`, review the changes, then rerun `bash deploy/aws/infra.sh create` against the same state. Do not destroy the partially created environment. Capacity is temporary, so this is a placement workaround, not a guarantee of successful provisioning. All five databases share that AZ with this override. Changing/removing it after databases exist can relocate them; inspect the plan before applying.

## Build and deploy

Use an AWS operator identity authorized for the intended account, Terraform 1.10 or later, AWS CLI, kubectl, a container engine, Java 17 and Maven, Node, jq, and k6. Set the account ID explicitly in both Terraform roots. Restrict the EKS API to the operator CIDRs. Review the plan before creating the disposable stack.

1. Initialize and apply `deploy/aws/bootstrap` with `account_id`, `region`, and `artifact_name`. Retain its state securely and record its state bucket and artifact repository outputs.
2. Set `ARTIFACT_REPOSITORY`, `AWS_REGION`, and `IMAGE_TAG` to the ECR repository, its region, and the source revision. Run `make cloud-images build-web`. The Lambda image must exist before environment apply.
3. Initialize the environment backend with `terraform -chdir=deploy/aws/environment init -backend-config="bucket=$STATE_BUCKET" -backend-config="region=$AWS_REGION" -backend-config="key=flashkart/demo.tfstate" -backend-config="use_lockfile=true"`. Supply `account_id`, `region`, `name`, `eks_version`, `operator_cidrs`, `artifact_repository`, and `image_tag` through a local variable file or `TF_VAR_*`. Apply the reviewed plan. The backend contains generated SQL passwords and must be restricted to deployment operators.
4. Run `make cloud-render`. Put the non-secret files from `build/cloud/manifests` on the GitOps branch reachable by Argo CD. Set `GITOPS_REPO`, `GITOPS_REVISION`, `GITOPS_PATH`, `CLERK_SECRET_KEY`, `CLERK_ISSUER_URL`, `FLASHKART_ALLOWED_ORIGINS`, and `FLASHKART_OWNER_USER_ID`. Build the frontend with its production `VITE_CLERK_PUBLISHABLE_KEY`; see [authentication setup](authentication.md).
5. Run `make demo`. The bootstrap resolves the requested Git revision to a commit and pins Argo CD to it. Rerun the bootstrap with the next revision to deploy an update. Bootstrap installs Argo CD, creates database and Clerk secrets, and registers the controller and application Git sources. Argo CD applies migrations and provisions Kafka topics before services start. The script uploads the frontend and runs a public smoke test.

Workers use encrypted 80 GiB gp3 root disks to accommodate the EMR operator and Flink images. EKS module 21.15.0 supports the `MINIMAL` update strategy, which replaces one worker at a time without surge capacity. Analytics pods reserve 20 GiB ephemeral storage so they cannot be scheduled onto legacy 20 GiB workers. The backend runtime includes trusted CA certificates for TLS connections to MSK and Clerk. The Lambda config file is explicitly readable by its runtime user.

The bootstrap installs cert-manager before enabling the EMR operator admission webhook. The webhook adds defaults on creation; the rendered manifest also declares the required Flink pod labels. Its update mutator is restricted to CREATE because it appends duplicate affinity rules on each UPDATE and repeatedly restarts jobs. Update validation remains enabled. Argo CD preserves this explicit webhook operation setting. The job uses its existing `flink` IRSA service account. The storefront and API target group start before analytics so an analytics startup failure does not block the website.

The notification Lambda owns the outcomes queue in AWS. The EKS notification deployment serves reads from DynamoDB. MSK uses per-service IAM roles, including scoped topic, group, and transaction permissions. Flink uses the EMR 7.13.0 runtime, AWS MSK IAM authentication, and S3 checkpoint storage. [EMR Flink release](https://docs.aws.amazon.com/emr/latest/EMR-on-EKS-DevelopmentGuide/emr-eks-7.13.0-flink-20260410.html) · [CloudFront VPC origins](https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/private-content-vpc-origins.html)

## Acceptance and recreation

Run `make test-cloud` to exercise public admin and checkout APIs, verify finalized analytics, sample HPA scale-out during k6 load, and export workload, GitOps, and CloudWatch evidence to `build/cloud`. Record separate evidence for MSK token renewal, SQS partial-batch retries and DLQ redrive, a Flink restore from S3, and an outage and recovery of one replica RDS instance. Run `REPLICA=a CONFIRM_OUTAGE=flashkart-demo sh deploy/aws/replica-outage.sh` against the disposable stack to stop that RDS backend, write through the surviving peers, and restart the backend. Capture replica SQL failures and hint metrics during the outage and confirm hint drain after recovery.

Archive the reports with the infrastructure revision, GitOps commit, image tags and digests. Run `CONFIRM_TEARDOWN=flashkart make teardown-demo` with the same Terraform variables and state. The application is deleted before Flink and load-balancer controllers so they can remove their managed resources. Terraform then destroys the environment. The bootstrap root retains the ECR artifacts and state bucket. Recreate the environment from the same source and images, render fresh endpoint values, deploy through GitOps, and repeat the smoke and recovery checks.

## Verification status

The development demo was deployed to AWS on 2026-11-18 using the existing Clerk development app. Public Google/email login rendering, customer/admin authorization, password sign-in, password reset, verified email registration, and a confirmed customer checkout have been verified. Evidence is stored locally in `build/cloud`. The approved EMR webhook workaround was applied with UPDATE validation retained. All GitOps applications reached Synced/Healthy, the analytics job reached RUNNING/STABLE and wrote aggregate results, and checkpoints continued completing without new failures. All three workers now use 80 GiB disks; the rollout completed within the existing CPU quota using MINIMAL updates. A final Terraform plan reported no changes. Full load, recovery, and recreation acceptance remain separate work; Phase 12 is not yet fully accepted.

## Cloud demo login checks

For an explicitly approved development demo, the existing Clerk development app can be used with the HTTPS CloudFront origin in `FLASHKART_ALLOWED_ORIGINS` and `FLASHKART_FRONTEND_URL`. Load `.env` before `.env.cloud` so the cloud origin overrides local settings. Keep these files ignored and private. Use a production Clerk instance for a production launch.

Run the existing real Clerk browser checks against the deployed URL:

```sh
cd web
CLERK_TEST_BASE_URL=https://YOUR-DISTRIBUTION.cloudfront.net npx playwright test --config playwright.clerk.config.ts --grep "login and registration|real Clerk customer|real Clerk admin"
CLERK_TEST_BASE_URL=https://YOUR-DISTRIBUTION.cloudfront.net FLASHKART_TEST_CLOUD_SMOKE=1 npx playwright test --config playwright.clerk.config.ts --grep "deployment helper"
```

The smoke option creates a test product and sale, completes a customer purchase, and verifies the confirmed order using renewed browser sessions. Temporary Clerk users are deleted afterward. The selected Terraform state must describe that same website.

The requested teardown on 2026-11-18 removed 142 Terraform resources. Terraform state was empty afterward, and a direct AWS audit found no remaining application VPC, instances, Elastic IPs, EKS cluster, RDS databases, MSK cluster, CloudFront resources, load balancer, Lambda, DynamoDB tables, queues, application buckets, roles, or log groups. The shared state bucket and ECR repository were retained. The application KMS key is disabled and scheduled for deletion on 2026-12-18. Local evidence is in `build/cloud/destroy`.
