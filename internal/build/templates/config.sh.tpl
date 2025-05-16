#!/bin/bash -x

set -e

# Setting users
echo "linux" | passwd root --stdin

{{ range .Users -}}
useradd -m {{ .Username }}
echo '{{ .Username }}:{{ .Password }}' | chpasswd -e
{{ end }}

# Enabling services
systemctl enable NetworkManager.service
systemctl enable systemd-sysext