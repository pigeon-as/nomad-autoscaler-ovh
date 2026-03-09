# E2E Tests

Tests the full OVH server lifecycle (order, verify, terminate) through the
autoscaler plugin with a real OVH API account.

**Warning**: These tests order real OVH dedicated servers and incur costs.

## Requirements

- Nomad binary on `$PATH`
- OVH API credentials (application key, secret, consumer key)
- An OVH Eco plan code for the cheapest available server

## Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `OVH_APPLICATION_KEY` | Yes | | OVH API application key |
| `OVH_APPLICATION_SECRET` | Yes | | OVH API application secret |
| `OVH_CONSUMER_KEY` | Yes | | OVH API consumer key |
| `E2E_PLAN_CODE` | Yes | | Eco server plan code (e.g. `25skleb01`) |
| `E2E_REVERSE_DOMAIN` | Yes | | Domain suffix for reverse DNS (e.g. `infra.example.com`) |
| `OVH_ENDPOINT` | No | `ovh-eu` | OVH API endpoint |
| `OVH_SUBSIDIARY` | No | `FR` | OVH subsidiary for ordering |
| `E2E_DATACENTER` | No | `gra3` | OVH datacenter for new servers |
| `E2E_OS_TEMPLATE` | No | `debian12_64` | OS template for server reinstall |

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
export E2E_REVERSE_DOMAIN="infra.example.com"
make e2e
```

## Timing

The lifecycle test orders a real server and waits for delivery + reinstall,
then terminates it. Expect 20–60 minutes total depending on OVH availability.
