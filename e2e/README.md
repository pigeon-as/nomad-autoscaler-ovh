# E2E Tests

Tests the full autoscaler → plugin → OVH pipeline by launching the
`nomad-autoscaler` binary as a subprocess with our plugin loaded via go-plugin
RPC. The autoscaler evaluates a min=1 scaling policy and orders a real OVH
server.

**Warning**: These tests order real OVH dedicated servers and incur costs.

## Requirements

- `nomad` binary on `$PATH` (for the dev agent)
- `nomad-autoscaler` binary on `$PATH`
- OVH API credentials (application key, secret, consumer key)

## Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `OVH_APPLICATION_KEY` | Yes | | OVH API application key |
| `OVH_APPLICATION_SECRET` | Yes | | OVH API application secret |
| `OVH_CONSUMER_KEY` | Yes | | OVH API consumer key |
| `OVH_ENDPOINT` | No | `ovh-eu` | OVH API endpoint |
| `OVH_SUBSIDIARY` | No | `FR` | OVH subsidiary for ordering |
| `E2E_PLAN_CODE` | Lifecycle | | Eco server plan code (e.g. `25skleb01`) |
| `E2E_DATACENTER` | No | `gra3` | OVH datacenter for new servers |
| `E2E_OS_TEMPLATE` | No | `debian12_64` | OS template for server reinstall |
| `E2E_PRODUCT_TYPE` | No | `eco` | OVH product type (eco, baremetalServers) |

## Tests

### TestPluginHealthy

Always runs. Verifies `nomad-autoscaler` loaded the plugin binary via
go-plugin RPC and reports healthy on `/v1/health`.

### TestScaleLifecycle

Requires `E2E_PLAN_CODE`. The autoscaler evaluates a min=1 policy on an empty
`ovh-e2e` node class, triggering a scale-out. The test polls the OVH API
for a new service name to appear.

## Usage

In one terminal, start the Nomad dev agent:

```sh
make dev
```

In another terminal, run the tests:

```sh
export OVH_APPLICATION_KEY="..."
export OVH_APPLICATION_SECRET="..."
export OVH_CONSUMER_KEY="..."
export E2E_PLAN_CODE="25skleb01"
make e2e
```
