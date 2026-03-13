# nomad-autoscaler-ovh

Nomad Autoscaler [target plugin](https://developer.hashicorp.com/nomad/tools/autoscaling/plugins/target) for horizontal cluster scaling via [OVH dedicated servers](https://eu.api.ovh.com/console/).

Orders new servers when cluster resources are exhausted, terminates idle servers on scale-in. Servers are identified by Nomad node attributes (`unique.platform.ovh.service_name`) and joined to the cluster via ConfigDrive userdata.

## Agent Configuration

```hcl
target "ovh-dedicated" {
  driver = "nomad-autoscaler-ovh"
  config {
    ovh_endpoint           = "ovh-eu"
    ovh_application_key    = "YOUR_APP_KEY"
    ovh_application_secret = "YOUR_APP_SECRET"
    ovh_consumer_key       = "YOUR_CONSUMER_KEY"
    ovh_subsidiary         = "FR"
  }
}
```

| Key | Default | Description |
|-----|---------|-------------|
| `ovh_endpoint` | `ovh-eu` | OVH API endpoint (`ovh-eu`, `ovh-ca`, `ovh-us`) |
| `ovh_application_key` | required | OVH API application key |
| `ovh_application_secret` | required | OVH API application secret |
| `ovh_consumer_key` | required | OVH API consumer key |
| `ovh_subsidiary` | auto-detect | OVH subsidiary for ordering. Auto-detected from `GET /me` if not set |

### Nomad ACL

When using a Nomad cluster with ACLs enabled, the plugin requires a token with:

```hcl
node {
  policy = "write"
}
```

## Policy Configuration

```hcl
check "allocated_cpu" {
  source = "nomad-apm"
  query  = "node_percentage-allocated_cpu"

  strategy "target-value" {
    target = 70
  }

  target "ovh-dedicated" {
    node_pool                = "default"
    node_drain_deadline      = "15m"
    node_purge               = "true"
    node_selector_strategy   = "least_busy"
    ovh_datacenter           = "gra3"
    ovh_plan_code            = "24adv-1"
    ovh_os_template          = "debian12_64"
    ovh_ssh_key              = "ssh-ed25519 AAAA..."
    ovh_user_data_file       = "/etc/pigeon/worker-userdata.sh"
    ovh_post_install_script  = "https://example.com/bootstrap.sh"
    ovh_product_type         = "eco"
  }
}
```

| Key | Default | Description |
|-----|---------|-------------|
| `ovh_datacenter` | `""` | OVH datacenter for new orders (e.g. `gra3`, `bhs8`). Required for scale-out |
| `ovh_plan_code` | `""` | OVH plan code for new servers (e.g. `24adv-1`). Required for scale-out |
| `ovh_os_template` | `debian12_64` | OS template for server installation |
| `ovh_ssh_key` | `""` | SSH public key content for server installation |
| `ovh_user_data_file` | `""` | Path to a file containing ConfigDrive userdata for server bootstrap |
| `ovh_post_install_script` | `""` | URL to a post-installation script run after OS install |
| `ovh_product_type` | `eco` | OVH cart product type (`eco`, `baremetalServers`) |
| `datacenter` | `""` | Nomad client datacenter filter |
| `node_class` | `""` | Nomad client node class filter |
| `node_pool` | `""` | Nomad client node pool filter |
| `node_drain_deadline` | `15m` | Drain deadline before termination |
| `node_drain_ignore_system_jobs` | `false` | Whether to stop system jobs when draining |
| `node_purge` | `false` | Purge Nomad node after termination |
| `node_selector_strategy` | `least_busy` | Node selection strategy for scale-in |

The `node_percentage-allocated_cpu` query measures how much of the cluster's CPU capacity is **allocated** (not used). When allocations consume >70% of available capacity, the autoscaler orders new servers. This scales based on scheduling pressure — whether Nomad can place new workloads — not on actual CPU utilization.

## Delivery Latency

OVH dedicated servers take **2–10 minutes** to deliver. Unlike AWS ASG/Azure VMSS/GCE MIG, OVH has no provider-side "desired count" that updates instantly, so the policy **cooldown** is the only mechanism preventing double-ordering:

```hcl
scaling "workers" {
  min      = 1
  max      = 10
  cooldown = "15m"

  policy {
    evaluation_interval = "30s"
    # ...
  }
}
```

## Build

```bash
make build    # → build/nomad-autoscaler-ovh
make test
```

## License

MPL-2.0