# Phase 13B live AWS acceptance

The 16 November 2026 preflight authenticated successfully. AWS reported an active Free account plan. Amazon MSK returned `SubscriptionRequiredException` in both `ap-south-1` and `ap-south-2`. The regional EC2 Standard On-Demand limits were 8 and 5 vCPUs respectively. The Terraform environment requests three four-vCPU workers, so it needs at least 12 available vCPUs in its deployment region. Both Terraform roots validated. The preflight created no AWS infrastructure.

After the account upgrade, AWS reported an active Paid plan and a 16-vCPU EC2 Standard On-Demand quota in `ap-south-1`. MSK still returned `SubscriptionRequiredException` in both regions. No infrastructure was provisioned during the retry.

A subsequent preflight confirmed that the MSK Serverless API worked in `ap-south-1`. Terraform created the retained state bucket and ECR repository. The first disposable apply brought MSK Serverless, EKS, the managed node group, and the notification Lambda online. It failed when CloudFront required AWS Support to verify the account before creating a distribution. An RDS waiter also returned an expired-token error. The database was present in Terraform state.

The first EKS access configuration relied on account root assuming a role, which STS rejected. The Terraform configuration now accepts an explicit IAM user or role for Kubernetes access. The deployment scripts validate that a supplied kubeconfig points to the intended EKS endpoint. Terraform validation and the kubeconfig checks passed locally.

The live notification Lambda returned only the invalid message ID from a mixed invocation. Repeating that invocation preserved one notification for the valid fixture. A malformed fixture sent through the actual outcomes queue appeared in the outcomes dead-letter queue. Evidence is in `build/cloud/13b/first/lambda`.

AWS Support can use the failed CloudFront `CreateDistributionWithTags` request ID `ea7500e3-2651-4232-9e6a-1a63fe1760c7` to investigate the account verification requirement. The five immutable ECR digests are recorded in `build/cloud/13b/first/ecr-images.json`.

Terraform removed 117 disposable resources and the environment state is empty. The bootstrap state bucket, ECR repository, and rehearsal evidence were retained. See `build/cloud/13b/environment-destroy.txt` for the teardown log.

Use an account with MSK access and sufficient EC2 capacity for the configured worker group. Run the [Phase 12 deployment procedure](phase-12-development.md) and retain separate report directories for the first deployment and the recreation. Keep Terraform plans and state in restricted storage because they can contain credentials.

| Check | Live evidence to keep |
| --- | --- |
| Public deployment | Run `make cloud-smoke` and `make test-cloud`. Keep the confirmed purchase, finalized analytics, HPA samples, CloudWatch stream reports, source revision, GitOps revision, and ECR digests. Verify the IAM trust subjects and resource scopes from the deployed roles. |
| MSK IAM refresh | Keep the same client pod and process running across an IAM token expiry. Publish before and after expiry, verify both purchases appear in finalized analytics, and record client identity, timestamps, and broker or client logs that show successful reauthentication. |
| Lambda batch and DLQ | Invoke the deployed notification function with a mixed batch containing a valid unique terminal outcome and a malformed sibling. Verify the response lists only the malformed message ID and the valid notification appears once. Send a distinct malformed fixture through the actual outcomes queue, then confirm that exact fixture reaches the outcomes DLQ after redelivery. Save message IDs and invocation evidence. |
| Flink recovery | Record a completed checkpoint whose external path is in the Terraform checkpoint bucket. Replace the JobManager and confirm that Flink restores state from a checkpoint in that bucket and finalizes a subsequent purchase. Keep the checkpoint path, recovery logs, and resulting analytics. |
| Replica RDS recovery | Run `REPLICA=a CONFIRM_OUTAGE=flashkart-demo sh deploy/aws/replica-outage.sh`. Keep backend readiness, preferred owners, the fallback hint count before and during the outage, the post-recovery drain, and the catalog version before and after recovery. |
| Teardown and recreation | Archive first-run reports and preserve bootstrap state and ECR. Run ordered teardown, recreate the environment from the recorded revision and images, publish freshly rendered GitOps configuration, and rerun smoke, purchase, analytics and replica recovery. Keep the second set of reports separately. |

## CDN removal

CloudFront still required account verification on the next attempt. A distribution probe was rejected with the same `AccessDenied` error, and nothing was created. The CDN is not a project requirement, so the environment no longer uses CloudFront. The Application Load Balancer is now internet-facing in the public subnets. It forwards to the `web` pods, which already served the React build and routed `/api/*` in the kind environment. The frontend S3 bucket and `publish-web.sh` were removed. `public_url` is now the ALB's HTTP DNS name, and `public_cidrs` controls which IPv4 ranges may reach it. The change passed `terraform fmt` and `terraform validate`.

Traffic to the ALB is plain HTTP because the deployment has no custom domain or certificate. Demo bearer tokens therefore cross the network unencrypted. Set `public_cidrs` to the operators' and testers' addresses during acceptance.

## Live acceptance on 16 November 2026

The disposable environment was applied from the CDN-free configuration in `ap-south-1`. Argo CD deployed GitOps revision `a8d9f16daa30f6956295a48d4eb3afb7232731ee` and reported it Synced and Healthy. Images are tagged `0d0e433ac2640280fd80edad347cde46db29fcdc`; their digests are in `image-digests.json`. Reports are in `build/cloud/13b/first`.

### Defects found and fixed

| Defect | Symptom in AWS | Fix |
| --- | --- | --- |
| The application image had no CA bundle | The `kafka-topics` hook failed with `x509: certificate signed by unknown authority`. MSK, SQS, DynamoDB and STS clients were affected. | Copy the CA bundle from the Go build stage. |
| The EMR Flink CRD requires `emrReleaseLabel` | Argo CD could not apply the `FlinkDeployment`. | Set `emr-7.13.0-flink-latest`. |
| EMR mandatory labels were missing | The operator reported `Missing EMR on EKS mandatory labels`. The webhook that adds them is disabled. | Add the EMR component and resource-type labels to JobManager and TaskManager pods. |
| The HA storage directory was missing | Validation required `spec.jobManager.storageDir`. | Set it to the checkpoint bucket's `ha` prefix. |
| Node disks were too small | The JobManager was evicted for low ephemeral storage. The EMR image is over 5 GB compressed. | Use 60 GB gp3 node volumes. |
| The MSK IAM jar was on the parent classloader | The job failed with `ClassNotFoundException` for Kafka's `AuthenticateCallbackHandler`. | Install the jar in `usrlib` with the job jar. |

The node-volume rollout stalled because the EC2 quota is 16 vCPUs. The managed node group tried to surge three 4-vCPU nodes. The old nodes were drained and terminated one at a time to finish it. A cluster that changes node configuration needs either a higher quota or a smaller surge. Flink's first job, evicted with its node, left a terminal record in the S3 HA store. That stale HA state was cleared before the redeployment.

### Results

| Check | Result | Evidence |
| --- | --- | --- |
| Public deployment | Pass. An admin published a sale through the public ALB. The customer purchase reached `CONFIRMED`, and analytics finalized one confirmed purchase. | `purchase.json`, `analytics.json`, `index.html` |
| HPA scale-out | Pass. A 3-minute k6 run sent 341,371 requests at about 1,896 per second, with 0 failures and p95 of 54 ms. Catalog and Admission each peaked at 5 replicas. | `k6.log`, `hpa.jsonstream`, `hpa-peaks.json` |
| CloudWatch logs | Pass. Application, platform and Lambda log streams are present. | `log-streams.json`, `lambda-streams.json` |
| Scoped IAM | Pass. Each workload role trusts only its own service account and grants no wildcard resources. Only the AWS Load Balancer Controller role, which uses AWS's published policy, contains wildcards. | `iam-trust-*.json`, `iam-policy-*.json` |
| Lambda partial batch | Pass. Two invocations of a mixed batch each reported only `malformed-msg-1` as failed. DynamoDB holds exactly one notification for the valid fixture. | `lambda/` |
| Outcomes DLQ | Pass. Message `00076fa4-1c82-41f4-a58e-cd66365e4a11` reached `flashkart-demo-outcomes-dlq` after five receives. | `lambda/dlq-*.json` |
| Flink S3 recovery | Pass. The leading JobManager was deleted after checkpoint 70 completed. The standby restored the job from checkpoint 71 in the Terraform bucket and finalized a later purchase. | `flink/` |
| MSK IAM token refresh | Pass on behavioural evidence. The same producer and TaskManager processes, with no restarts, published and consumed purchases 21 to 40 minutes after they started. That exceeds the 15-minute token lifetime. Neither client logs reauthentication at INFO level. | `msk/`, `analytics.json`, `flink/analytics.json` |
| Replica RDS outage | Pass. With `flashkart-demo-a` stopped, replica `a` reported its backend unready, and fallback `d` held one hint. After restart the hint drained, and the fixture's version matched the version written during the outage. The first write after the stop returned 503, and an immediate retry succeeded. | `replica/` |

The public ALB occasionally refused connections from the operator network during the run, while both ALB addresses answered other probes normally. The first smoke attempt timed out for that reason and passed when rerun. The `cloud-flink-operator` Argo application reports OutOfSync and Healthy because of chart drift; the operator itself worked.

### Teardown

Argo CD applications were deleted first, so their controllers removed the workloads and load-balancer targets. Four resources were removed outside Terraform because their delete calls carry ARNs in the request path: the Lambda event source mapping, the operator access entry and its policy association, and the MSK Serverless cluster. They were deleted with the AWS CLI and removed from state, and the MSK deletion finished before Terraform destroyed the remaining 130 resources. A sweep afterwards found no EKS clusters, EC2 instances, EBS volumes, RDS instances or snapshots, MSK clusters, NAT gateways, load balancers, Elastic IPs, VPCs, Lambda functions, SQS queues or DynamoDB tables. The state bucket, the ECR repository and the `flashkart-operator` IAM user were retained. The log is `build/cloud/13b/first/environment-destroy.txt`.

Recreation from the recorded revision remains open. The environment will be recreated before the presentation, and the smoke, purchase, analytics and replica recovery checks will run again then. The node group needs a larger EC2 vCPU quota than 16, or a smaller update surge, before any later node configuration change.
