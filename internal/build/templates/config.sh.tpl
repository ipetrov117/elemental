#!/bin/bash -x

set -euo pipefail

setupUsers(){
  echo "linux" | passwd root --stdin

{{ range .Users -}}
  useradd -m {{ .Username }}
  echo '{{ .Username }}:{{ .Password }}' | chpasswd -e
{{ end }}
}

setupEnsureSysExtService() {
  cat <<- END > /etc/systemd/system/ensure-sysext.service
[Unit]
BindsTo=systemd-sysext.service
After=systemd-sysext.service
DefaultDependencies=no
# Keep in sync with systemd-sysext.service
ConditionDirectoryNotEmpty=|/etc/extensions
ConditionDirectoryNotEmpty=|/run/extensions
ConditionDirectoryNotEmpty=|/var/lib/extensions
ConditionDirectoryNotEmpty=|/usr/local/lib/extensions
ConditionDirectoryNotEmpty=|/usr/lib/extensions
[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/bin/systemctl daemon-reload
ExecStart=/usr/bin/systemctl restart --no-block sockets.target timers.target multi-user.target
[Install]
WantedBy=sysinit.target
END
}

enableDefaultServices() {
  systemctl enable NetworkManager.service
  systemctl enable systemd-sysext

  setupEnsureSysExtService
  systemctl enable ensure-sysext.service
}

{{- if and .KubernetesDir .ManifestDeployScript }}
setupManifestsInstallService() {
  cat << EOF > /etc/systemd/system/k8s-manifest-installer.service
[Unit]
Description=Kubernetes Resources Install
Requires=rke2-server.service
After=rke2-server.service
ConditionPathExists=/var/lib/rancher/rke2/bin/kubectl
ConditionPathExists=/etc/rancher/rke2/rke2.yaml

[Install]
WantedBy=multi-user.target

[Service]
Type=oneshot
TimeoutSec=900
Restart=on-failure
RestartSec=60
ExecStartPre=/bin/sh -c 'until [ "\$(systemctl show -p SubState --value rke2-server.service)" = "running" ]; do sleep 10; done'
ExecStart="{{ .ManifestDeployScript }}"
ExecStartPost=/bin/sh -c "systemctl disable k8s-manifest-installer.service"
ExecStartPost=/bin/sh -c "rm -rf /etc/systemd/system/k8s-manifest-installer.service"
ExecStartPost=/bin/sh -c 'rm -rf "{{ .KubernetesDir }}"'
EOF
}
{{- end }}

setupUsers
enableDefaultServices

{{- if and .KubernetesDir .ManifestDeployScript }}
setupManifestsInstallService
systemctl enable k8s-manifest-installer.service
{{- end }}
