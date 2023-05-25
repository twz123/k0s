resource "null_resource" "k0sctl_apply" {
  count = var.k0sctl_binary == null ? 0 : 1

  triggers = {
    k0sctl_config = jsonencode(local.k0sctl_config)
    # k0s_binary_hash          = var.k0s_binary == null ? null : filesha256(var.k0s_binary)
    # airgap_image_bundle_hash = var.airgap_image_bundle == null ? null : filesha256(var.k0sctl_airgap_image_bundle)
  }

  provisioner "local-exec" {
    environment = {
      K0SCTL_BINARY = var.k0sctl_binary
      K0SCTL_CONFIG = jsonencode(local.k0sctl_config)
    }

    command = <<-EOF
      printf %s "$K0SCTL_CONFIG" | env SSH_KNOWN_HOSTS= "$K0SCTL_BINARY" apply --disable-telemetry --disable-upgrade-check -c -
      EOF
  }
}

data "external" "k0s_kubeconfig" {
  # Dirty hack to get the kubeconfig into Terrafrom. Requires jq.

  count = var.k0sctl_binary == null ? 0 : 1
  query = {
    k0sctl_config = jsonencode(local.k0sctl_config)
  }

  program = [
    "/usr/bin/env", "sh", "-ec",
    "jq '.k0sctl_config | fromjson' | '${var.k0sctl_binary}' kubeconfig --disable-telemetry -c - | jq --raw-input --slurp '{kubeconfig: .}'",
  ]

  depends_on = [null_resource.k0sctl_apply]
}
