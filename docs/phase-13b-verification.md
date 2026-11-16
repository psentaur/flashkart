# Phase 13B live AWS acceptance

The 16 November 2026 preflight authenticated successfully. AWS reported an active Free account plan. Amazon MSK returned `SubscriptionRequiredException` in both `ap-south-1` and `ap-south-2`. The regional EC2 Standard On-Demand limits were 8 and 5 vCPUs respectively. The Terraform environment requests three four-vCPU workers, so it needs at least 12 available vCPUs in its deployment region. Both Terraform roots validated. The preflight created no AWS infrastructure.

After the account upgrade, AWS reported an active Paid plan and a 16-vCPU EC2 Standard On-Demand quota in `ap-south-1`. MSK still returned `SubscriptionRequiredException` in both regions. No infrastructure was provisioned during the retry.

A subsequent preflight confirmed that the MSK Serverless API worked in `ap-south-1`. Terraform created the retained state bucket and ECR repository. The first disposable apply brought MSK Serverless, EKS, the managed node group, and the notification Lambda online. It failed when CloudFront required AWS Support to verify the account before creating a distribution. An RDS waiter also returned an expired-token error. The database was present in Terraform state.

The first EKS access configuration relied on account root assuming a role, which STS rejected. The Terraform configuration now accepts an explicit IAM user or role for Kubernetes access. The deployment scripts validate that a supplied kubeconfig points to the intended EKS endpoint. Terraform validation and the kubeconfig checks passed locally.

The live notification Lambda returned only the invalid message ID from a mixed invocation. Repeating that invocation preserved one notification for the valid fixture. A malformed fixture sent through the actual outcomes queue appeared in the outcomes dead-letter queue. Evidence is in `build/cloud/13b/first/lambda`.

AWS Support can use the failed CloudFront `CreateDistributionWithTags` request ID `ea7500e3-2651-4232-9e6a-1a63fe1760c7` to investigate the account verification requirement. The five immutable ECR digests are recorded in `build/cloud/13b/first/ecr-images.json`.

Use an account with MSK access and sufficient EC2 capacity for the configured worker group. Run the [Phase 12 deployment procedure](phase-12-development.md) and retain separate report directories for the first deployment and the recreation. Keep Terraform plans and state in restricted storage because they can contain credentials.

| Check | Live evidence to keep |
| --- | --- |
| Public deployment | Run `make cloud-smoke` and `make test-cloud`. Keep the confirmed purchase, finalized analytics, HPA samples, CloudWatch stream reports, source revision, GitOps revision, and ECR digests. Verify the IAM trust subjects and resource scopes from the deployed roles. |
| MSK IAM refresh | Keep the same client pod and process running across an IAM token expiry. Publish before and after expiry, verify both purchases appear in finalized analytics, and record client identity, timestamps, and broker or client logs that show successful reauthentication. |
| Lambda batch and DLQ | Invoke the deployed notification function with a mixed batch containing a valid unique terminal outcome and a malformed sibling. Verify the response lists only the malformed message ID and the valid notification appears once. Send a distinct malformed fixture through the actual outcomes queue, then confirm that exact fixture reaches the outcomes DLQ after redelivery. Save message IDs and invocation evidence. |
| Flink recovery | Record a completed checkpoint whose external path is in the Terraform checkpoint bucket. Replace the JobManager and confirm that Flink restores state from a checkpoint in that bucket and finalizes a subsequent purchase. Keep the checkpoint path, recovery logs, and resulting analytics. |
| Replica RDS recovery | Run `REPLICA=a CONFIRM_OUTAGE=flashkart-demo sh deploy/aws/replica-outage.sh`. Keep backend readiness, preferred owners, the fallback hint count before and during the outage, the post-recovery drain, and the catalog version before and after recovery. |
| Teardown and recreation | Archive first-run reports and preserve bootstrap state and ECR. Run ordered teardown, recreate the environment from the recorded revision and images, publish freshly rendered GitOps configuration, and rerun smoke, purchase, analytics and replica recovery. Keep the second set of reports separately. |

Live acceptance and recreation remain open.
