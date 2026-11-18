resource "aws_security_group" "edge" {
  name   = "${var.name}-edge"
  vpc_id = module.vpc.vpc_id
}
resource "aws_vpc_security_group_egress_rule" "edge" {
  security_group_id            = aws_security_group.edge.id
  referenced_security_group_id = module.eks.node_security_group_id
  ip_protocol                  = "tcp"
  from_port                    = 8080
  to_port                      = 8080
}
resource "aws_vpc_security_group_ingress_rule" "pods" {
  security_group_id            = module.eks.node_security_group_id
  referenced_security_group_id = aws_security_group.edge.id
  ip_protocol                  = "tcp"
  from_port                    = 8080
  to_port                      = 8080
}
resource "aws_lb" "api" {
  name               = var.name
  internal           = false
  load_balancer_type = "application"
  security_groups    = [aws_security_group.edge.id]
  subnets            = module.vpc.public_subnets
}
resource "aws_lb_target_group" "api" {
  name        = "${var.name}-api"
  port        = 8080
  protocol    = "HTTP"
  target_type = "ip"
  vpc_id      = module.vpc.vpc_id
  health_check { path = "/" }
}
resource "aws_lb_listener" "http" {
  load_balancer_arn = aws_lb.api.arn
  port              = 80
  protocol          = "HTTP"
  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.api.arn
  }
}
# The ALB is the public entry point. The web pods serve the React build and route /api/* to the services.
resource "aws_vpc_security_group_ingress_rule" "public" {
  for_each          = toset(var.public_cidrs)
  security_group_id = aws_security_group.edge.id
  cidr_ipv4         = each.value
  ip_protocol       = "tcp"
  from_port         = 80
  to_port           = 80
}
