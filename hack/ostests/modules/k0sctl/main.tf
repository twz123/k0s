resource "terraform_data" "k0sctl_apply" {
  triggers_replace = [
    var.k0sctl_executable_path,
    sha256(jsonencode(local.k0sctl_config)),
  ]

  provisioner "local-exec" {
    environment = {
      K0SCTL_EXECUTABLE_PATH = var.k0sctl_executable_path
      K0SCTL_CONFIG = jsonencode(local.k0sctl_config)
    }

    command = <<-EOF
      printf %s "$K0SCTL_CONFIG" | env -u SSH_AUTH_SOCK SSH_KNOWN_HOSTS='' "$K0SCTL_EXECUTABLE_PATH" apply --disable-telemetry --disable-upgrade-check -c -
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
    var.k0sctl_executable_path, <<-EOS
      if endswith("~~~FAIL\n") then
        error("Failed to generate kubeconfig!")
      else
        {kubeconfig: .}
      end
    EOS
  ]

  depends_on = [terraform_data.k0sctl_apply]
}
