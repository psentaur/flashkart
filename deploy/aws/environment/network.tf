data "aws_availability_zones" "available" {
  state = "available"
  filter {
    name   = "opt-in-status"
    values = ["opt-in-not-required"]
  }
}

module "vpc" {
  source                 = "terraform-aws-modules/vpc/aws"
  version                = "6.5.0"
  name                   = var.name
  cidr                   = "10.42.0.0/16"
  azs                    = slice(data.aws_availability_zones.available.names, 0, 2)
  public_subnets         = ["10.42.0.0/24", "10.42.1.0/24"]
  private_subnets        = ["10.42.16.0/20", "10.42.32.0/20"]
  enable_nat_gateway     = true
  one_nat_gateway_per_az = true
  enable_dns_hostnames   = true
}

module "eks" {
  source                                   = "terraform-aws-modules/eks/aws"
  version                                  = "21.10.1"
  name                                     = var.name
  kubernetes_version                       = var.eks_version
  endpoint_public_access                   = true
  endpoint_public_access_cidrs             = var.operator_cidrs
  endpoint_private_access                  = true
  enable_cluster_creator_admin_permissions = true
  enable_irsa                              = true
  vpc_id                                   = module.vpc.vpc_id
  subnet_ids                               = module.vpc.private_subnets
  addons = {
    coredns    = {}
    kube-proxy = {}
    vpc-cni    = { before_compute = true }
  }
  eks_managed_node_groups = {
    workers = {
      ami_type       = "AL2023_x86_64_STANDARD"
      instance_types = ["m6i.xlarge"]
      min_size       = 3
      desired_size   = 3
      max_size       = 3
      iam_role_additional_policies = {
        logs = aws_iam_policy.node_logs.arn
      }
    }
  }
}

resource "aws_security_group" "database" {
  for_each = toset(["a", "b", "c", "d", "transactional"])
  name     = "${var.name}-db-${each.key}"
  vpc_id   = module.vpc.vpc_id
}

resource "aws_vpc_security_group_ingress_rule" "database" {
  for_each                     = aws_security_group.database
  security_group_id            = each.value.id
  referenced_security_group_id = module.eks.node_security_group_id
  ip_protocol                  = "tcp"
  from_port                    = 5432
  to_port                      = 5432
}

resource "aws_security_group" "kafka" {
  name   = "${var.name}-kafka"
  vpc_id = module.vpc.vpc_id
}
resource "aws_vpc_security_group_ingress_rule" "kafka" {
  security_group_id            = aws_security_group.kafka.id
  referenced_security_group_id = module.eks.node_security_group_id
  ip_protocol                  = "tcp"
  from_port                    = 9098
  to_port                      = 9098
}

resource "aws_msk_serverless_cluster" "events" {
  cluster_name = var.name
  vpc_config {
    subnet_ids         = module.vpc.private_subnets
    security_group_ids = [aws_security_group.kafka.id]
  }
  client_authentication {
    sasl {
      iam { enabled = true }
    }
  }
}
data "aws_msk_bootstrap_brokers" "events" {
  cluster_arn = aws_msk_serverless_cluster.events.arn
}
