data "aws_availability_zones" "available" { state = "available" }

locals {
  azs = slice(data.aws_availability_zones.available.names, 0, 3)
  tags = {
    "platform-idp:managed"      = "true"
    "platform-idp:installation" = var.installation_id
    "platform-idp:service"      = "shared"
    "platform-idp:owner"        = "platform-owners"
    "platform-idp:environment"  = "platform"
    "platform-idp:cost-scope"   = "shared"
  }
}

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "6.6.1"

  name = "${var.installation_id}-vpc"
  cidr = var.vpc_cidr
  azs  = local.azs

  private_subnets  = [for index, _ in local.azs : cidrsubnet(var.vpc_cidr, 4, index)]
  public_subnets   = [for index, _ in local.azs : cidrsubnet(var.vpc_cidr, 8, index + 48)]
  database_subnets = [for index, _ in local.azs : cidrsubnet(var.vpc_cidr, 8, index + 64)]

  enable_nat_gateway           = true
  single_nat_gateway           = true
  enable_dns_hostnames         = true
  create_database_subnet_group = true

  public_subnet_tags  = { "kubernetes.io/role/elb" = "1" }
  private_subnet_tags = { "kubernetes.io/role/internal-elb" = "1" }
}

module "eks" {
  source  = "terraform-aws-modules/eks/aws"
  version = "21.24.2"

  name                                     = "${var.installation_id}-eks"
  kubernetes_version                       = var.kubernetes_version
  endpoint_public_access                   = true
  endpoint_public_access_cidrs             = var.cluster_public_access_cidrs
  enable_irsa                              = true
  enable_cluster_creator_admin_permissions = true

  vpc_id     = module.vpc.vpc_id
  subnet_ids = module.vpc.private_subnets

  addons = {
    coredns                = {}
    eks-pod-identity-agent = { before_compute = true }
    kube-proxy             = {}
    vpc-cni                = { before_compute = true }
  }

  eks_managed_node_groups = {
    platform = {
      instance_types = var.node_instance_types
      min_size       = var.node_minimum_size
      max_size       = var.node_maximum_size
      desired_size   = var.node_minimum_size
      capacity_type  = "ON_DEMAND"
    }
  }
}

resource "random_password" "database_master" {
  length           = 32
  special          = true
  override_special = "!#$%&*()-_=+[]{}<>:?"
}

resource "aws_secretsmanager_secret" "database_master" {
  name                    = "platform/${var.installation_id}/shared/postgres-master"
  recovery_window_in_days = 30
}

resource "aws_secretsmanager_secret_version" "database_master" {
  secret_id     = aws_secretsmanager_secret.database_master.id
  secret_string = jsonencode({ username = "platform_admin", password = random_password.database_master.result })
}

resource "aws_security_group" "database" {
  name_prefix = "${var.installation_id}-postgres-"
  description = "PostgreSQL access from EKS nodes"
  vpc_id      = module.vpc.vpc_id
  ingress {
    description     = "PostgreSQL from EKS nodes"
    from_port       = 5432
    to_port         = 5432
    protocol        = "tcp"
    security_groups = [module.eks.node_security_group_id]
  }
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_db_instance" "shared" {
  identifier                 = "${var.installation_id}-shared"
  engine                     = "postgres"
  engine_version             = "17"
  instance_class             = var.database_instance_class
  allocated_storage          = 20
  max_allocated_storage      = 100
  storage_type               = "gp3"
  storage_encrypted          = true
  username                   = "platform_admin"
  password                   = random_password.database_master.result
  db_subnet_group_name       = module.vpc.database_subnet_group_name
  vpc_security_group_ids     = [aws_security_group.database.id]
  publicly_accessible        = false
  backup_retention_period    = 7
  deletion_protection        = true
  skip_final_snapshot        = false
  final_snapshot_identifier  = "${var.installation_id}-shared-final"
  auto_minor_version_upgrade = true
  apply_immediately          = false
}
