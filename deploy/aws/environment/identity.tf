locals {
  queue_send        = ["sqs:SendMessage"]
  queue_receive     = ["sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:GetQueueAttributes", "sqs:ChangeMessageVisibility"]
  notification_read = ["dynamodb:GetItem", "dynamodb:Query"]
  cluster_arn       = aws_msk_serverless_cluster.events.arn
  topic_arns        = { for t in ["purchase-events", "analytics-results"] : t => "${replace(local.cluster_arn, ":cluster/", ":topic/")}/${t}" }
  group_arns        = { for g in ["flashkart-sales-analytics-v1", "flashkart-analytics-projection-v1"] : g => "${replace(local.cluster_arn, ":cluster/", ":group/")}/${g}" }
  kafka_connect     = { Action = ["kafka-cluster:Connect", "kafka-cluster:DescribeCluster", "kafka-cluster:WriteDataIdempotently"], Resource = [local.cluster_arn] }
  produce           = { Action = ["kafka-cluster:DescribeTopic", "kafka-cluster:WriteData"], Resource = [local.topic_arns["purchase-events"]] }
  policies = {
    admission-controller = [
      local.kafka_connect, local.produce,
      { Action = local.queue_send, Resource = [aws_sqs_queue.work["purchases"].arn] },
      { Action = local.notification_read, Resource = [aws_dynamodb_table.projection["notifications"].arn] }
    ]
    inventory-reservation = [
      local.kafka_connect, local.produce,
      { Action = local.queue_receive, Resource = [aws_sqs_queue.work["purchases"].arn] },
      { Action = local.queue_send, Resource = [aws_sqs_queue.work["outcomes"].arn] }
    ]
    order-service   = [local.kafka_connect, local.produce, { Action = local.queue_send, Resource = [aws_sqs_queue.work["outcomes"].arn] }]
    payment-service = [local.kafka_connect, local.produce]
    product-catalog = [{ Action = ["s3:GetObject"], Resource = ["${aws_s3_bucket.data["images"].arn}/*"] }]
    notification    = [{ Action = local.notification_read, Resource = [aws_dynamodb_table.projection["notifications"].arn] }]
    analytics-api = [
      local.kafka_connect, local.produce,
      { Action = ["kafka-cluster:DescribeTopic", "kafka-cluster:ReadData"], Resource = [local.topic_arns["analytics-results"]] },
      { Action = ["kafka-cluster:DescribeGroup", "kafka-cluster:AlterGroup"], Resource = [local.group_arns["flashkart-analytics-projection-v1"]] },
      { Action = ["dynamodb:PutItem", "dynamodb:Query"], Resource = [aws_dynamodb_table.projection["analytics"].arn] }
    ]
    flink = [
      local.kafka_connect,
      { Action = ["kafka-cluster:DescribeTopic", "kafka-cluster:ReadData"], Resource = [local.topic_arns["purchase-events"]] },
      { Action = ["kafka-cluster:DescribeTopic", "kafka-cluster:WriteData"], Resource = [local.topic_arns["analytics-results"]] },
      { Action = ["kafka-cluster:DescribeGroup", "kafka-cluster:AlterGroup"], Resource = [local.group_arns["flashkart-sales-analytics-v1"]] },
      { Action = ["kafka-cluster:DescribeTransactionalId", "kafka-cluster:AlterTransactionalId"], Resource = ["${replace(local.cluster_arn, ":cluster/", ":transactional-id/")}/${var.name}-sales-*"] },
      { Action = ["s3:ListBucket", "s3:GetBucketLocation"], Resource = [aws_s3_bucket.data["checkpoints"].arn] },
      { Action = ["s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:AbortMultipartUpload"], Resource = ["${aws_s3_bucket.data["checkpoints"].arn}/*"] }
    ]
    migrations   = [{ Action = ["secretsmanager:GetSecretValue"], Resource = [for db in aws_db_instance.postgres : db.master_user_secret[0].secret_arn] }]
    kafka-topics = [local.kafka_connect, { Action = ["kafka-cluster:CreateTopic", "kafka-cluster:DescribeTopic"], Resource = values(local.topic_arns) }]
  }
  issuer = replace(module.eks.cluster_oidc_issuer_url, "https://", "")
}
resource "aws_iam_role" "workload" {
  for_each = local.policies
  name     = "${var.name}-${each.key}"
  assume_role_policy = jsonencode({ Version = "2012-10-17", Statement = [{
    Effect    = "Allow", Action = "sts:AssumeRoleWithWebIdentity",
    Principal = { Federated = module.eks.oidc_provider_arn },
    Condition = { StringEquals = {
      ("${local.issuer}:aud") = "sts.amazonaws.com"
      ("${local.issuer}:sub") = "system:serviceaccount:flashkart:${each.key}"
    } }
  }] })
}
resource "aws_iam_role_policy" "workload" {
  for_each = local.policies
  role     = aws_iam_role.workload[each.key].name
  policy   = jsonencode({ Version = "2012-10-17", Statement = [for item in each.value : merge({ Effect = "Allow" }, item)] })
}

resource "aws_cloudwatch_log_group" "node" {
  name              = "/flashkart/${var.name}/application"
  retention_in_days = 14
}
resource "aws_iam_policy" "node_logs" {
  name = "${var.name}-application-logs"
  policy = jsonencode({ Version = "2012-10-17", Statement = [{
    Effect   = "Allow", Action = ["logs:CreateLogStream", "logs:PutLogEvents", "logs:DescribeLogStreams"],
    Resource = ["${aws_cloudwatch_log_group.node.arn}:*"]
  }] })
}

resource "aws_iam_role" "lambda" {
  name = "${var.name}-notification-lambda"
  assume_role_policy = jsonencode({ Version = "2012-10-17", Statement = [{
    Effect = "Allow", Action = "sts:AssumeRole", Principal = { Service = "lambda.amazonaws.com" }
  }] })
}
resource "aws_cloudwatch_log_group" "lambda" {
  name              = "/aws/lambda/${var.name}-notification"
  retention_in_days = 14
}
resource "aws_iam_role_policy" "lambda" {
  role = aws_iam_role.lambda.name
  policy = jsonencode({ Version = "2012-10-17", Statement = [
    { Effect = "Allow", Action = local.queue_receive, Resource = [aws_sqs_queue.work["outcomes"].arn] },
    { Effect = "Allow", Action = ["dynamodb:PutItem"], Resource = [aws_dynamodb_table.projection["notifications"].arn] },
    { Effect = "Allow", Action = ["logs:CreateLogStream", "logs:PutLogEvents"], Resource = ["${aws_cloudwatch_log_group.lambda.arn}:*"] },
  ] })
}
resource "aws_lambda_function" "notification" {
  function_name = "${var.name}-notification"
  role          = aws_iam_role.lambda.arn
  package_type  = "Image"
  image_uri     = "${var.artifact_repository}:notification-${var.image_tag}"
  timeout       = 30
  environment {
    variables = {
      FLASHKART_CONFIG             = "/var/task/config.json"
      FLASHKART_NOTIFICATION_TABLE = aws_dynamodb_table.projection["notifications"].name
    }
  }
  depends_on = [aws_iam_role_policy.lambda, aws_cloudwatch_log_group.lambda]
}
resource "aws_lambda_event_source_mapping" "notification" {
  event_source_arn        = aws_sqs_queue.work["outcomes"].arn
  function_name           = aws_lambda_function.notification.arn
  batch_size              = 10
  function_response_types = ["ReportBatchItemFailures"]
}

resource "aws_iam_role" "load_balancer" {
  name = "${var.name}-load-balancer-controller"
  assume_role_policy = jsonencode({ Version = "2012-10-17", Statement = [{
    Effect    = "Allow", Action = "sts:AssumeRoleWithWebIdentity",
    Principal = { Federated = module.eks.oidc_provider_arn },
    Condition = { StringEquals = {
      ("${local.issuer}:aud") = "sts.amazonaws.com"
      ("${local.issuer}:sub") = "system:serviceaccount:kube-system:aws-load-balancer-controller"
    } }
  }] })
}
resource "aws_iam_role_policy" "load_balancer" {
  role   = aws_iam_role.load_balancer.name
  policy = file("${path.module}/load-balancer-policy.json")
}
resource "random_password" "database" {
  for_each = toset(["a", "b", "c", "d", "flash_sale_app", "inventory_app", "admission_app", "orders_app", "payment_app"])
  length   = 32
  special  = false
}
