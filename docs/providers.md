# Proxy provider adapters

Provider differences are resolved in the control plane. A generated result is still an ordinary SOCKS5 host, port, username, and password, so Gateway and device behavior remain provider-independent.

The adapter model covers the patterns found in the reference service:

- fixed host and port with country/session fields in the username;
- country aliases such as a supplier-specific United Kingdom code;
- country-specific hosts or port ranges;
- country/state/city credential templates;
- random UUID, integer, or alphanumeric session IDs;
- weighted fallback state selection;
- enabled providers and weighted provider selection.

Supplier account values and passwords are passed in `secrets`. They are encrypted before PostgreSQL storage, never returned by the API, and must be referenced from password templates with `{{secret.name}}`. Do not copy credentials into `config`.

`sessionMinutes` is only exposed to templates as `{{duration}}`; it does not expire a route. Set `routeTTLMinutes` separately only when supplier credentials truly stop working after a fixed period.

Example definition:

```json
{
  "enabled": true,
  "weight": 50,
  "config": {
    "protocol": "socks5",
    "host": {"default": "{{country}}.gateway.example.net"},
    "port": {"byCountry": {"us": {"from": 10001, "to": 19999}}},
    "username": {
      "default": "{{secret.account}}-country-{{country}}-session-{{session}}",
      "byCountry": {
        "us": "{{secret.account}}-state-{{country}}_{{state}}-session-{{session}}"
      }
    },
    "password": {"default": "{{secret.password}}"},
    "countryAliases": {"gb": "uk"},
    "session": {"type": "uuid"},
    "sessionMinutes": 30
  },
  "secrets": {
    "account": "supplied-at-deploy-time",
    "password": "supplied-at-deploy-time"
  }
}
```

Create or update it with `PUT /v1/providers/vendor-a`. Omitting `secrets` during a later update preserves the existing encrypted values. `GET /v1/providers` returns only `secretKeys`.

Generate and publish a route:

```http
POST /v1/devices/device-001/route/from-provider
Authorization: Bearer ...
Content-Type: application/json

{
  "providerId": "vendor-a",
  "country": "us",
  "state": "california",
  "expectedVersion": 3
}
```

Omit `providerId` to select among enabled providers by weight. Generated routes use the same CAS and PREPARE/ACTIVATE workflow as manually supplied routes.
