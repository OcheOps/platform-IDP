data "aws_iam_policy_document" "workload_assume" {
  for_each = local.oidc_subjects
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = [data.terraform_remote_state.foundation.outputs.oidc_provider_arn]
    }
    condition {
      test     = "StringEquals"
      variable = "${data.terraform_remote_state.foundation.outputs.oidc_provider}:aud"
      values   = ["sts.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "${data.terraform_remote_state.foundation.outputs.oidc_provider}:sub"
      values   = [each.value]
    }
  }
}

resource "aws_iam_role" "workload" {
  for_each           = local.oidc_subjects
  name               = "${var.installation_id}-${replace(each.key, "_", "-")}"
  assume_role_policy = data.aws_iam_policy_document.workload_assume[each.key].json
}

data "aws_iam_policy_document" "external_secrets" {
  statement {
    actions   = ["secretsmanager:DescribeSecret", "secretsmanager:GetSecretValue", "secretsmanager:ListSecretVersionIds"]
    resources = ["arn:aws:secretsmanager:${var.aws_region}:${data.aws_caller_identity.current.account_id}:secret:platform/${var.installation_id}/*"]
  }
}

resource "aws_iam_role_policy" "external_secrets" {
  name   = "secrets-manager-read"
  role   = aws_iam_role.workload["external_secrets"].id
  policy = data.aws_iam_policy_document.external_secrets.json
}

data "aws_iam_policy_document" "external_dns" {
  statement {
    actions   = ["route53:ChangeResourceRecordSets"]
    resources = ["arn:aws:route53:::hostedzone/${var.dns_zone_id}"]
  }
  statement {
    actions   = ["route53:ListHostedZones", "route53:ListResourceRecordSets", "route53:ListTagsForResource"]
    resources = ["*"]
  }
}

resource "aws_iam_role_policy" "external_dns" {
  count  = var.dns_provider == "route53" ? 1 : 0
  name   = "route53-zone-writer"
  role   = aws_iam_role.workload["external_dns"].id
  policy = data.aws_iam_policy_document.external_dns.json
}
