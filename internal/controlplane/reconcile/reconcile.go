package reconcile

import (
	"context"
	"fmt"

	"stackforge/internal/controlplane/discovery/consul"
	"stackforge/internal/controlplane/dns/cloudflare"
	"stackforge/internal/controlplane/domain"
	"stackforge/internal/controlplane/orchestrator/nomad"
)

type Result struct {
	DomainID string   `json:"domain_id"`
	Actions  []string `json:"actions"`
	Error    string   `json:"error,omitempty"`
}

func Domain(d domain.Domain) Result {
	return DomainWithClients(context.Background(), d, nil)
}

type Clients struct {
	Cloudflare *cloudflare.Client
	Consul     *consul.Client
	Nomad      *nomad.Client
}

func DomainWithClients(ctx context.Context, d domain.Domain, clients *Clients) Result {
	if d.OwnershipStatus != "verified" {
		return Result{DomainID: d.ID, Error: "ownership is not verified; refusing to apply routing"}
	}
	res := Result{DomainID: d.ID}
	if clients == nil {
		return Result{DomainID: d.ID, Error: "external clients are not configured; refusing to pretend reconciliation succeeded"}
	}
	if clients.Cloudflare == nil {
		return Result{DomainID: d.ID, Error: "Cloudflare client is not configured"}
	}
	zoneID := d.ProviderZoneID
	if zoneID == "" {
		var err error
		zoneID, err = clients.Cloudflare.LookupZone(ctx, d.RootDomain)
		if err != nil {
			return Result{DomainID: d.ID, Error: err.Error()}
		}
	}
	if err := clients.Cloudflare.UpsertRecord(ctx, zoneID, cloudflare.Record{Type: d.DNSRecordType, Name: d.Domain, Content: d.DNSRecordValue, Proxied: d.Proxied}); err != nil {
		return Result{DomainID: d.ID, Error: err.Error()}
	}
	res.Actions = append(res.Actions, fmt.Sprintf("ensured DNS %s %s", d.DNSRecordType, d.Domain))
	if clients.Consul == nil {
		return Result{DomainID: d.ID, Actions: res.Actions, Error: "Consul client is not configured"}
	}
	if err := clients.Consul.WriteRoute(ctx, d.Domain, consul.RouteMetadata{Domain: d.Domain, Service: d.TargetServiceName, Port: d.TargetServicePort}); err != nil {
		return Result{DomainID: d.ID, Actions: res.Actions, Error: err.Error()}
	}
	res.Actions = append(res.Actions, "wrote Consul route metadata")
	if clients.Nomad == nil {
		return Result{DomainID: d.ID, Actions: res.Actions, Error: "Nomad client is not configured"}
	}
	if err := clients.Nomad.JobStatus(ctx, d.TargetServiceName); err != nil {
		return Result{DomainID: d.ID, Actions: res.Actions, Error: err.Error()}
	}
	res.Actions = append(res.Actions, "checked Nomad job state")
	return res
}
