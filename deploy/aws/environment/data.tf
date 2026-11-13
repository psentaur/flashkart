locals {
  databases = { a = 0, b = 1, c = 0, d = 1, transactional = 0 }
  queues    = toset(["purchases", "outcomes"])
  keys      = { notifications = ["customer_id", "attempt_id"], analytics = ["sale_id", "starts_at"] }
  buckets   = toset(["frontend", "checkpoints", "images"])
}
resource "aws_db_subnet_group" "private" {
  name       = var.name
  subnet_ids = module.vpc.private_subnets
}
resource "aws_db_instance" "postgres" {
  for_each                    = local.databases
  identifier                  = "${var.name}-${each.key}"
  engine                      = "postgres"
  engine_version              = "17"
  instance_class              = "db.t4g.micro"
  allocated_storage           = 20
  storage_encrypted           = true
  db_name                     = "flashkart"
  username                    = "flashkart_admin"
  manage_master_user_password = true
  db_subnet_group_name        = aws_db_subnet_group.private.name
  vpc_security_group_ids      = [aws_security_group.database[each.key].id]
  availability_zone           = module.vpc.azs[each.value]
  multi_az                    = false
  publicly_accessible         = false
  skip_final_snapshot         = true
  backup_retention_period     = 1
}
resource "aws_sqs_queue" "dead" {
  for_each                  = local.queues
  name                      = "${var.name}-${each.key}-dlq"
  message_retention_seconds = 1209600
  sqs_managed_sse_enabled   = true
}
resource "aws_sqs_queue" "work" {
  for_each                   = local.queues
  name                       = "${var.name}-${each.key}"
  visibility_timeout_seconds = 180
  sqs_managed_sse_enabled    = true
  redrive_policy             = jsonencode({ deadLetterTargetArn = aws_sqs_queue.dead[each.key].arn, maxReceiveCount = 5 })
}
resource "aws_dynamodb_table" "projection" {
  for_each     = local.keys
  name         = "${var.name}-${each.key}"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = each.value[0]
  range_key    = each.value[1]
  dynamic "attribute" {
    for_each = toset(each.value)
    content {
      name = attribute.value
      type = "S"
    }
  }
}
resource "aws_s3_bucket" "data" {
  for_each      = local.buckets
  bucket        = "${var.name}-${var.account_id}-${var.region}-${each.key}"
  force_destroy = true
}
resource "aws_s3_bucket_public_access_block" "data" {
  for_each                = aws_s3_bucket.data
  bucket                  = each.value.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}
