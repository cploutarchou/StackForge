package templates

const ControlPlaneService = `[Unit]
Description=StackForge Control Plane API
After=network-online.target
Wants=network-online.target

[Service]
EnvironmentFile=/etc/stackforge/control-plane.env
ExecStart=/usr/local/bin/stackforge serve
Restart=always
User=stackforge
Group=stackforge

[Install]
WantedBy=multi-user.target
`

const ReconcilerService = `[Unit]
Description=StackForge Reconciler
After=stackforge-control-plane.service

[Service]
EnvironmentFile=/etc/stackforge/control-plane.env
ExecStart=/usr/local/bin/stackforge serve
Restart=always
User=stackforge
Group=stackforge

[Install]
WantedBy=multi-user.target
`
