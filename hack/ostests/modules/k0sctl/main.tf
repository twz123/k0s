resource "null_resource" "k0sctl_apply" {
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
      printf %s "$K0SCTL_CONFIG" | env -u SSH_AUTH_SOCK SSH_KNOWN_HOSTS='' "$K0SCTL_BINARY" apply --disable-telemetry --disable-upgrade-check -c -
      EOF
  }
}

data "external" "k0s_kubeconfig" {
  query = {
    k0sctl_config = jsonencode(local.k0sctl_config)
  }

  program = [
    "env", "sh", "-ec",
    <<-EOS
    jq '.k0sctl_config | fromjson' |
      { "$1" kubeconfig --disable-telemetry -c - || echo ~~~FAIL; } |
      jq --raw-input --slurp "$2"
    EOS
    , "--",
    var.k0sctl_binary, <<-EOS
      if endswith("~~~FAIL\n") then
        error("Failed to generate kubeconfig!")
      else
        {kubeconfig: .}
      end
    EOS
  ]

  depends_on = [null_resource.k0sctl_apply]
}
