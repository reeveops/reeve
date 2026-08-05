terraform {
  required_version = ">= 1.4"
  backend "local" {}

  required_providers {
    random = {
      source = "hashicorp/random"
    }
  }
}

# One pet name per workspace (dev / prod). terraform.workspace is the
# stack name reeve selected.
resource "random_pet" "name" {
  length = 2
  prefix = terraform.workspace
}

output "pet" {
  value = random_pet.name.id
}
