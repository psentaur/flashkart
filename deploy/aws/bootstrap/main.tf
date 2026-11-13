terraform {
  required_version = ">= 1.10"
  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 6.28.0" }
  }
}
variable "account_id" { type = string }
variable "region" { type = string }
variable "artifact_name" { type = string }
provider "aws" {
  region              = var.region
  allowed_account_ids = [var.account_id]
}
resource "aws_s3_bucket" "state" {
  bucket = "${var.artifact_name}-${var.account_id}-tfstate"
  lifecycle { prevent_destroy = true }
}
resource "aws_s3_bucket_versioning" "state" {
  bucket = aws_s3_bucket.state.id
  versioning_configuration { status = "Enabled" }
}
resource "aws_s3_bucket_public_access_block" "state" {
  bucket                  = aws_s3_bucket.state.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}
resource "aws_ecr_repository" "images" {
  name                 = var.artifact_name
  image_tag_mutability = "IMMUTABLE"
  image_scanning_configuration { scan_on_push = true }
  lifecycle { prevent_destroy = true }
}
output "state_bucket" { value = aws_s3_bucket.state.id }
output "artifact_repository" { value = aws_ecr_repository.images.repository_url }
