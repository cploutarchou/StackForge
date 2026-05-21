package status

import (
	"stackforge/internal/stackforge/inventory"
)

type Report struct {
	Cluster      string           `json:"cluster"`
	Install      string           `json:"install_status"`
	Health       string           `json:"health"`
	Nodes        []inventory.Node `json:"nodes"`
	Warnings     []string         `json:"warnings"`
	ControlPlane string           `json:"control_plane"`
}

func FromInventory(inv *inventory.Inventory) Report {
	return Report{Cluster: inv.ClusterName, Install: inv.InstallStatus, Health: inv.LastHealthCheckStatus, Nodes: inv.Nodes, Warnings: inv.Warnings, ControlPlane: inv.ControlPlaneEndpoint}
}
