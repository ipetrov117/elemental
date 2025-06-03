#!/bin/bash -x

set -euo pipefail

setupUsers(){
  echo "linux" | passwd root --stdin

{{ range .Users -}}
  useradd -m {{ .Username }}
  echo '{{ .Username }}:{{ .Password }}' | chpasswd -e
{{ end }}
}

enableDefaultServices() {
  systemctl enable NetworkManager.service
  systemctl enable systemd-sysext
}

setupManifestServiceCleanup() {
  local manifestDir="$1"
  cat << EOF > /etc/systemd/system/kubernetes-resources-install-cleanup.service
[Unit]
Description=Cleans up kubernetes-resources-install.service and kubernetes-resources-install.timer

[Service]
Type=oneshot
RemainAfterExit=no
Restart=on-failure
RestartSec=30
ExecStartPre=/bin/sh -c "systemctl stop kubernetes-resources-install.timer"
ExecStart=/bin/sh -c "rm -f /etc/systemd/system/kubernetes-resources-install.{timer,service} && systemctl daemon-reload"
ExecStartPost=/bin/sh -c "rm -rf \"${manifestDir}\""
EOF
}

setupManifestInstallTimer() {
  cat << EOF > /etc/systemd/system/kubernetes-resources-install.timer
[Unit]
Description=Kubernetes Resources Install timer
After=multi-user.target rke2-server.service

[Timer]
OnBootSec=30s
AccuracySec=5s
Persistent=true

[Install]
WantedBy=timers.target
EOF
}

setupManifestsInstallService() {
  local manifestDir="$1"
  cat << EOF > /etc/systemd/system/kubernetes-resources-install.service
[Unit]
Description=Kubernetes Resources Install
Requires=rke2-server.service
After=rke2-server.service
ConditionPathExists=/var/lib/rancher/rke2/bin/kubectl
ConditionPathExists=/etc/rancher/rke2/rke2.yaml

[Service]
Type=oneshot
TimeoutSec=900
RemainAfterExit=no
Restart=on-failure
RestartSec=30
ExecStartPre=/bin/sh -c 'until [ "\$(systemctl show -p SubState --value rke2-server.service)" = "running" ]; do sleep 10; done'
ExecStartPre=cp /var/lib/rancher/rke2/bin/kubectl ${manifestDir}/kubectl
ExecStart="${manifestDir}/create_manifests.sh"
ExecStartPost=/bin/sh -c "systemctl start kubernetes-resources-install-cleanup.service"
EOF
}

setupManifestCreationScript(){
  local manifestDir="$1"
  cat << EOF > "${manifestDir}/create_manifests.sh"
#!/bin/bash

manifests=(
{{- range .Manifests }}
"{{ . }}"
{{- end }}
)

failed=false
for manifest in "\${manifests[@]}"; do
  output_file="\$(mktemp -p "${manifestDir}" kubectl_out.XXXXXX)"

  if ! ${manifestDir}/kubectl create -f "\$manifest" --kubeconfig /etc/rancher/rke2/rke2.yaml >"\$output_file" 2>&1; then
    if ! grep -q "AlreadyExists" "\$output_file"; then
      failed=true
    fi
  fi

  cat "\$output_file"  # Print the output for visibility
  rm -f "\$output_file"
done

if [ "\$failed" = true ]; then
  exit 1
fi
EOF

  chmod +x "${manifestDir}/create_manifests.sh"
}

createKubernetesManifests() {
  local manifest_create_dir=/opt/unified-core/manifest-create
  mkdir -p "${manifest_create_dir}"

  setupManifestCreationScript "${manifest_create_dir}"
  setupManifestsInstallService "${manifest_create_dir}"
  setupManifestInstallTimer
  setupManifestServiceCleanup "${manifest_create_dir}"

  systemctl enable kubernetes-resources-install.timer
}

main() {
  setupUsers
  enableDefaultServices

{{- if .Manifests }}
  createKubernetesManifests
{{- end }}
}

main