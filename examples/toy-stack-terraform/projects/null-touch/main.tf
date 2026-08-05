terraform {
  required_version = ">= 1.4"
  backend "local" {}

  required_providers {
    null = {
      source = "hashicorp/null"
    }
  }
}

# Re-provisions whenever rev changes - handy for exercising previews.
resource "null_resource" "touch" {
  triggers = {
    rev = "1"
  }
}
