output "deployment" {
  value = {
    name    = var.name
    region  = var.region
    vpc     = module.vpc.vpc_id
    brokers = split(",", data.aws_msk_bootstrap_brokers.events.bootstrap_brokers_sasl_iam)
    databases = { for key, db in aws_db_instance.postgres : key => {
      host = db.address, admin_secret = db.master_user_secret[0].secret_arn
    } }
    roles           = { for key, role in aws_iam_role.workload : key => role.arn }
    controller_role = aws_iam_role.load_balancer.arn
    queues          = { for key, queue in aws_sqs_queue.work : key => queue.url }
    tables          = { for key, table in aws_dynamodb_table.projection : key => table.name }
    buckets         = { for key, bucket in aws_s3_bucket.data : key => bucket.id }
    logs            = aws_cloudwatch_log_group.node.name
    target_group    = aws_lb_target_group.api.arn
    repository      = var.artifact_repository
    tag             = var.image_tag
    public_url      = "https://${aws_cloudfront_distribution.site.domain_name}"
  }
}
output "database_passwords" {
  sensitive = true
  value     = { for key, password in random_password.database : key => password.result }
}

output "replica_databases" {
  value = { for key, db in aws_db_instance.postgres : key => db.identifier if key != "transactional" }
}
