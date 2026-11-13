# Phase 12 AWS deployment

Phase 12 separates reusable Terraform state storage and immutable ECR artifacts from the disposable application environment. The environment uses private EKS workers in two availability zones, five Single-AZ RDS instances, MSK Serverless, SQS and DLQs, DynamoDB, Lambda, and CloudFront with private S3 and ALB origins. All EKS workloads use the same binaries and migrations as the kind environment.

## Build and deploy

Use an AWS operator identity authorized for the intended account, Terraform 1.10 or later, AWS CLI, kubectl, a container engine, Java 17 and Maven, Node, jq, and k6. Set the account ID explicitly in both Terraform roots. Restrict the EKS API to the operator CIDRs. Review the plan before creating the disposable stack.

1. Initialize and apply `deploy/aws/bootstrap` with `account_id`, `region`, and `artifact_name`. Retain its state securely and record its state bucket and artifact repository outputs.
2. Set `ARTIFACT_REPOSITORY`, `AWS_REGION`, and `IMAGE_TAG` to the ECR repository, its region, and the source revision. Run `make cloud-images build-web`. The Lambda image must exist before environment apply.
3. Initialize the environment backend with `terraform -chdir=deploy/aws/environment init -backend-config="bucket=$STATE_BUCKET" -backend-config="region=$AWS_REGION" -backend-config="key=flashkart/demo.tfstate" -backend-config="use_lockfile=true"`. Supply `account_id`, `region`, `name`, `eks_version`, `operator_cidrs`, `artifact_repository`, and `image_tag` through a local variable file or `TF_VAR_*`. Apply the reviewed plan. The backend contains generated SQL passwords and must be restricted to deployment operators.
4. Run `make cloud-render`. Put the non-secret files from `build/cloud/manifests` on the GitOps branch reachable by Argo CD. Set `GITOPS_REPO`, `GITOPS_REVISION`, `GITOPS_PATH`, and distinct `FLASHKART_ADMIN_TOKEN` and `FLASHKART_CUSTOMER_TOKEN` values.
5. Run `make demo`. The bootstrap resolves the requested Git revision to a commit and pins Argo CD to it. Rerun the bootstrap with the next revision to deploy an update. Bootstrap installs Argo CD, creates database and demo secrets, and registers the controller and application Git sources. Argo CD applies migrations and provisions Kafka topics before services start. The script uploads the frontend and runs a public smoke test.

The notification Lambda owns the outcomes queue in AWS. The EKS notification deployment serves reads from DynamoDB. MSK uses per-service IAM roles, including scoped topic, group, and transaction permissions. Flink uses the EMR 7.13.0 runtime, AWS MSK IAM authentication, and S3 checkpoint storage. [EMR Flink release](https://docs.aws.amazon.com/emr/latest/EMR-on-EKS-DevelopmentGuide/emr-eks-7.13.0-flink-20260410.html) · [CloudFront VPC origins](https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/private-content-vpc-origins.html)

## Acceptance and recreation

Run `make test-cloud` to exercise public admin and checkout APIs, verify finalized analytics, sample HPA scale-out during k6 load, and export workload, GitOps, and CloudWatch evidence to `build/cloud`. Record separate evidence for MSK token renewal, SQS partial-batch retries and DLQ redrive, a Flink restore from S3, and an outage and recovery of one replica RDS instance. Run `REPLICA=a CONFIRM_OUTAGE=flashkart-demo sh deploy/aws/replica-outage.sh` against the disposable stack to stop that RDS backend, write through the surviving peers, and restart the backend. Capture replica SQL failures and hint metrics during the outage and confirm hint drain after recovery.

Archive the reports with the infrastructure revision, GitOps commit, image tags and digests. Run `CONFIRM_TEARDOWN=flashkart make teardown-demo` with the same Terraform variables and state. The application is deleted before Flink and load-balancer controllers so they can remove their managed resources. Terraform then destroys the environment. The bootstrap root retains the ECR artifacts and state bucket. Recreate the environment from the same source and images, render fresh endpoint values, deploy through GitOps, and repeat the smoke and recovery checks.

## Verification status

Terraform configuration validation and local tests are separate from a live AWS rehearsal. The AWS session was expired when checked, so no cloud apply, acceptance, teardown, or recreation was performed. Phase 12 remains open until live evidence is recorded.
