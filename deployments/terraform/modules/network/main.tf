variable "name" { type = string }
variable "vpc_cidr" { type = string }
variable "allowed_ingress_cidrs" { type = list(string) }
variable "app_ports" { type = list(number) }
variable "data_alb_ports" {
  description = "Port service di host stateful yang diakses ALB (Notification)."
  type        = list(number)
}

data "aws_availability_zones" "this" {
  state = "available"
}

resource "aws_vpc" "this" {
  cidr_block           = var.vpc_cidr
  enable_dns_support   = true
  enable_dns_hostnames = true
  tags                 = { Name = var.name }
}

resource "aws_internet_gateway" "this" {
  vpc_id = aws_vpc.this.id
  tags   = { Name = var.name }
}

# Dua subnet publik, tanpa NAT Gateway (HLD §4.5). Instance tidak punya SSH; akses admin lewat SSM.
# Subnet publik tanpa NAT (HLD §4.5, hemat biaya): instance perlu IP publik untuk keluar; tidak ada ingress selain dari SG.
#trivy:ignore:AVD-AWS-0164
resource "aws_subnet" "public" {
  count                   = 2
  vpc_id                  = aws_vpc.this.id
  cidr_block              = cidrsubnet(var.vpc_cidr, 8, count.index)
  availability_zone       = data.aws_availability_zones.this.names[count.index]
  map_public_ip_on_launch = true
  tags                    = { Name = "${var.name}-public-${count.index}" }
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.this.id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.this.id
  }
  tags = { Name = "${var.name}-public" }
}

resource "aws_route_table_association" "public" {
  count          = 2
  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public.id
}

resource "aws_security_group" "alb" {
  name_prefix = "${var.name}-alb-"
  description = "ALB: HTTP/HTTPS dari CIDR yang diizinkan"
  vpc_id      = aws_vpc.this.id
  lifecycle { create_before_destroy = true }
}

resource "aws_vpc_security_group_ingress_rule" "alb_in" {
  for_each          = { for pair in setproduct(var.allowed_ingress_cidrs, [80, 443]) : "${pair[0]}-${pair[1]}" => pair }
  security_group_id = aws_security_group.alb.id
  cidr_ipv4         = each.value[0]
  from_port         = each.value[1]
  to_port           = each.value[1]
  ip_protocol       = "tcp"
}

resource "aws_security_group" "app" {
  name_prefix = "${var.name}-app-"
  description = "Instance aplikasi: hanya dari ALB"
  vpc_id      = aws_vpc.this.id
  lifecycle { create_before_destroy = true }
}

resource "aws_vpc_security_group_ingress_rule" "app_from_alb" {
  for_each                     = toset([for p in var.app_ports : tostring(p)])
  security_group_id            = aws_security_group.app.id
  referenced_security_group_id = aws_security_group.alb.id
  from_port                    = tonumber(each.value)
  to_port                      = tonumber(each.value)
  ip_protocol                  = "tcp"
}

resource "aws_vpc_security_group_egress_rule" "alb_to_app" {
  for_each                     = toset([for p in var.app_ports : tostring(p)])
  security_group_id            = aws_security_group.alb.id
  referenced_security_group_id = aws_security_group.app.id
  from_port                    = tonumber(each.value)
  to_port                      = tonumber(each.value)
  ip_protocol                  = "tcp"
}

resource "aws_vpc_security_group_egress_rule" "alb_to_data" {
  for_each                     = toset([for p in var.data_alb_ports : tostring(p)])
  security_group_id            = aws_security_group.alb.id
  referenced_security_group_id = aws_security_group.data.id
  from_port                    = tonumber(each.value)
  to_port                      = tonumber(each.value)
  ip_protocol                  = "tcp"
}

# Tanpa NAT: instance menjangkau ECR, SSM, Midtrans, Resend, FCM lewat HTTPS publik. Hanya 443 keluar; port data hanya ke host stateful.
#trivy:ignore:AVD-AWS-0104
resource "aws_vpc_security_group_egress_rule" "app_https" {
  security_group_id = aws_security_group.app.id
  cidr_ipv4         = "0.0.0.0/0"
  from_port         = 443
  to_port           = 443
  ip_protocol       = "tcp"
}

resource "aws_vpc_security_group_egress_rule" "app_to_data" {
  for_each                     = toset(["5432", "6379", "5672"])
  security_group_id            = aws_security_group.app.id
  referenced_security_group_id = aws_security_group.data.id
  from_port                    = tonumber(each.value)
  to_port                      = tonumber(each.value)
  ip_protocol                  = "tcp"
}

resource "aws_security_group" "data" {
  name_prefix = "${var.name}-data-"
  description = "Host stateful: PostgreSQL, Valkey, RabbitMQ dari aplikasi; Notification dari ALB"
  vpc_id      = aws_vpc.this.id
  lifecycle { create_before_destroy = true }
}

resource "aws_vpc_security_group_ingress_rule" "data_from_app" {
  for_each                     = toset(["5432", "6379", "5672"])
  security_group_id            = aws_security_group.data.id
  referenced_security_group_id = aws_security_group.app.id
  from_port                    = tonumber(each.value)
  to_port                      = tonumber(each.value)
  ip_protocol                  = "tcp"
}

resource "aws_vpc_security_group_ingress_rule" "data_from_alb" {
  for_each                     = toset([for p in var.data_alb_ports : tostring(p)])
  security_group_id            = aws_security_group.data.id
  referenced_security_group_id = aws_security_group.alb.id
  from_port                    = tonumber(each.value)
  to_port                      = tonumber(each.value)
  ip_protocol                  = "tcp"
}

#trivy:ignore:AVD-AWS-0104
resource "aws_vpc_security_group_egress_rule" "data_https" {
  security_group_id = aws_security_group.data.id
  cidr_ipv4         = "0.0.0.0/0"
  from_port         = 443
  to_port           = 443
  ip_protocol       = "tcp"
}

output "vpc_id" { value = aws_vpc.this.id }
output "public_subnet_ids" { value = aws_subnet.public[*].id }
output "alb_sg_id" { value = aws_security_group.alb.id }
output "app_sg_id" { value = aws_security_group.app.id }
output "data_sg_id" { value = aws_security_group.data.id }
