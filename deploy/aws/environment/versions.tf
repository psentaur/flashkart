terraform {
  required_version = ">= 1.10"
  backend "s3" {}
  required_providers {
    aws    = { source = "hashicorp/aws", version = "~> 6.28.0" }
    random = { source = "hashicorp/random", version = "~> 3.7" }
  }
}
provider "aws" {
  region              = var.region
  allowed_account_ids = [var.account_id]
  default_tags {
    tags = { Project = "flashkart", Environment = var.name }
  }
}
variable "account_id" { type = string }
variable "region" {
  type    = string
  default = "ap-south-1"
}
variable "name" {
  type = string
  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{2,19}$", var.name))
    error_message = "Use a lowercase name of 3–20 characters."
  }
}
variable "eks_version" { type = string }
variable "operator_cidrs" {
  type = list(string)
  validation {
    condition     = length(var.operator_cidrs) > 0 && alltrue([for cidr in var.operator_cidrs : can(cidrhost(cidr, 0)) && !endswith(cidr, "/0")])
    error_message = "Supply restricted EKS API CIDRs."
  }
}
variable "artifact_repository" { type = string }
variable "image_tag" { type = string }
