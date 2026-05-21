# StackForge API

## Auth

Send `Authorization: Bearer <admin-key>` for all `/api/v1` routes. `/health` is public. `/ready` is protected by default.

## Endpoints

- `GET /health`
- `GET /ready`
- `POST /api/v1/domains`
- `GET /api/v1/domains`
- `GET /api/v1/domains/{id}`
- `PATCH /api/v1/domains/{id}`
- `DELETE /api/v1/domains/{id}`
- `POST /api/v1/domains/{id}/verification-token`
- `POST /api/v1/domains/{id}/verify`
- `POST /api/v1/domains/{id}/dns/apply`
- `DELETE /api/v1/domains/{id}/dns`
- `POST /api/v1/domains/{id}/routing/apply`
- `DELETE /api/v1/domains/{id}/routing`
- `GET /api/v1/domains/{id}/status`
- `POST /api/v1/domains/{id}/reconcile`
- `POST /api/v1/domains/reconcile-all`
- `GET /api/v1/audit-logs`
- `GET /api/v1/domains/{id}/audit-logs`

## Example

```bash
curl -H "Authorization: Bearer $STACKFORGE_ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{"tenant_id":"tenant-123","domain":"example.com","target_service_name":"frontend","target_service_port":8080}' \
  http://127.0.0.1:8080/api/v1/domains
```

Errors use HTTP status codes with a plain error body.
