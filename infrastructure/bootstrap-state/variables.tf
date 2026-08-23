variable "installation_id" {
  type = string
  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{1,30}[a-z0-9]$", var.installation_id))
    error_message = "installation_id must be a lowercase DNS label with 3-32 characters."
  }
}

variable "aws_region" { type = string }
variable "aws_profile" { type = string }
variable "state_bucket_name" { type = string }
variable "lock_table_name" { type = string }
variable "tags" {
  type    = map(string)
  default = {}
}
