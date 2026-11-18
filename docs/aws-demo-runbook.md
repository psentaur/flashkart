# AWS demo runbook

This guide brings Flashkart up on AWS for a demonstration, walks through the demo, and tears it down again. It uses the existing Terraform state bucket and ECR repository, which are kept between runs. A full run takes about an hour to bring up. While it is up, it costs roughly US$2 an hour, mostly MSK Serverless, the three EKS workers, and the NAT gateways. Tear it down as soon as the demo is over.

## What you need

- AWS credentials for the `flashkart-operator` IAM user, or another identity allowed to run Terraform in account `224033508410`, region `ap-south-1`.
- Terraform 1.10 or later, the AWS CLI, `kubectl`, `jq`, Docker, Java 17 with Maven, Node 22, and Python 3.
- The demo tokens `FLASHKART_ADMIN_TOKEN` and `FLASHKART_CUSTOMER_TOKEN`, which must be distinct. Keep them out of Git.
- Push access to `psentaur/flashkart`. Argo CD deploys from GitHub, so every commit it uses must be pushed.

Check out the branch you want to deploy and note its full commit:

```sh
git checkout codex/phase-13b-aws-acceptance && git pull
export SOURCE=$(git rev-parse HEAD)
```

## 1. Variables

Create a local `demo.tfvars` outside the repository. These are the values used on 18 November 2026:

```hcl
account_id                 = "224033508410"
region                     = "ap-south-1"
name                       = "flashkart-demo"
eks_version                = "1.34"
eks_operator_principal_arn = "arn:aws:iam::224033508410:user/flashkart-operator"
operator_cidrs             = ["<your public IP>/32"]
artifact_repository        = "224033508410.dkr.ecr.ap-south-1.amazonaws.com/flashkart"
image_tag                  = "<SOURCE>"
```

`operator_cidrs` decides who can reach the Kubernetes API. Put the public IP of the machine running `kubectl` there; `curl -s https://checkip.amazonaws.com` shows it. `eks_operator_principal_arn` must be the identity whose credentials you use. `public_cidrs` is not set, so anyone with the link can open the site. Set it to your own ranges to restrict access.

```sh
export AWS_REGION=ap-south-1
export ARTIFACT_REPOSITORY=224033508410.dkr.ecr.ap-south-1.amazonaws.com/flashkart
export IMAGE_TAG=$SOURCE
export GITOPS_REPO=https://github.com/psentaur/flashkart.git GITOPS_PATH=deploy/aws/gitops/demo
export FLASHKART_ADMIN_TOKEN=... FLASHKART_CUSTOMER_TOKEN=...
```

## 2. Images

```sh
make cloud-images
```

This builds and pushes the `app`, `notification`, `flink`, `web` and `migrations` images tagged with the commit. The notification Lambda image must exist before Terraform runs. If Docker Hub rate-limits anonymous pulls (HTTP 429), run `docker login` and retry.

## 3. Infrastructure

```sh
terraform -chdir=deploy/aws/environment init -backend-config=bucket=flashkart-224033508410-tfstate \
  -backend-config=region=ap-south-1 -backend-config=key=flashkart/demo.tfstate -backend-config=use_lockfile=true
terraform -chdir=deploy/aws/environment plan -var-file=/path/to/demo.tfvars -out=demo.plan
terraform -chdir=deploy/aws/environment apply demo.plan
```

Apply takes 25 to 30 minutes. EKS and the RDS instances take the longest.

- If RDS reports `InsufficientDBInstanceCapacity` for an availability zone, wait a few minutes and run plan and apply again.
- The account's EC2 quota is 16 vCPUs. The three 4-vCPU workers fit, but changing the node group's configuration later needs the old nodes drained first.
- When apply finishes, run `plan` again. It should report no changes.

## 4. Deploy configuration

```sh
make cloud-render
cp build/cloud/manifests/resources.json deploy/aws/gitops/demo/resources.json
git add deploy/aws/gitops/demo/resources.json
git commit -m "Render the demo overlay for <purpose>"
git push
export GITOPS_REVISION=$(git rev-parse HEAD)
```

The rendered file holds endpoints and image references but no passwords or tokens. Check `git diff` before committing anyway.

## 5. Deploy

```sh
make demo
```

This installs Argo CD and the controllers, creates the database and token secrets, deploys the commit in `GITOPS_REVISION`, and runs a public smoke test. It is safe to run again if a wait times out. It finishes with `Flashkart revision … is healthy and token consumers are ready.` The site is the `public_url` from `terraform -chdir=deploy/aws/environment output -json deployment`, an `http://…elb.amazonaws.com` address. The Flink analytics job needs a few more minutes to pull its image and start.

## 6. Demo script

Sign in with the administrator token and set up the data:

1. **Products** (Product Catalog). Create two or three products with a photo and a regular price. Photos upload to S3.
2. **Sales** (Flash Sale Manager). For each product, publish either a **Flash sale** (a price below the regular price, a stock limit and a short window) or a **Regular listing** (the regular price, open until ended).

Then sign in as a customer in another browser or a private window:

3. **Shop.** Each product appears once, with live stock (Inventory Reservation). Flash deals show their discount and a timer. Add items to the cart with a quantity.
4. **Cart.** Place one order with **Card** and one with **Test card**, which the Payment Service always declines.
5. **My orders and the bell.** Each order shows its tracker: placed, reserved, payment, confirmed. The bell shows one notification per order from the Notification Service; opening it shows that order.

Back as administrator:

6. **Rush test.**
   - **Browser shoppers** traces a crowd service by service; it needs the customer token.
   - **Cloud burst** sends 400 parallel shoppers for 20 seconds from inside the cluster. The Admission Controller throttles most of them, and Inventory sells exactly the stock limit.
7. **Analytics** (Sales Analytics, through Kafka and Flink). About a minute later it shows totals across all sales, and per sale: throttled, sold out, confirmed, revenue and latency.
8. **Sales → End sale** stops a listing early. **Replicas** shows the leaderless replica layer.

## 7. Teardown

```sh
TF_CLI_ARGS_destroy="-var-file=/path/to/demo.tfvars" CONFIRM_TEARDOWN=flashkart make teardown-demo
```

`TF_CLI_ARGS_destroy` passes the same variables to `terraform destroy`; without it, Terraform asks for each one.

The script deletes the Argo CD applications first, so their controllers remove load balancers and pods, then destroys the environment. The state bucket and ECR repository are kept. Afterwards, confirm that nothing billable is left:

```sh
aws eks list-clusters; aws rds describe-db-instances --query 'DBInstances[].DBInstanceIdentifier'
aws kafka list-clusters-v2 --query 'ClusterInfoList[].ClusterName'
aws ec2 describe-nat-gateways --filter Name=state,Values=available --query 'NatGateways[].NatGatewayId'
aws elbv2 describe-load-balancers --query 'LoadBalancers[].LoadBalancerName'
```

All of these should return empty lists. Behind some HTTPS proxies, AWS calls that carry an ARN in the URL path fail with signature or decoding errors. Examples are the MSK cluster, the Lambda event source mapping, and the EKS access entry. If that happens, run those steps from a network without the proxy.
