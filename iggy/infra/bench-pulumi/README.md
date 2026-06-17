# bench-pulumi

Provisions the two-VM AWS topology (us-east-1a) for the Iggy-vs-Kafka → VeloDB
Cloud benchmark.

| VM | Instance | Role |
|----|----------|------|
| `iggy-bench-producer` | `c7i.2xlarge` (8 vCPU) | isolated load generator |
| `iggy-bench-broker` | `c6id.4xlarge` (16 vCPU, local NVMe) | Iggy/Kafka + connectors |

Backend (VeloDB Cloud) is **not** managed here — provision it separately and put
its endpoint in the connector config.

Both VMs run Amazon Linux 2023 (kernel 6.x, so io_uring works), with user-data
that raises `memlock`/`nofile`, mounts the broker's NVMe at `/mnt/nvme`, and
installs a Rust + C toolchain so you can build `iggy-server` on-box with
`target-cpu=native` (raw binary, never a container).

## Use

State is a local backend (`~/.pulumi`); no Pulumi account needed. Credentials are
read from env, not committed.

```bash
cd iggy/infra/bench-pulumi
export PULUMI_CONFIG_PASSPHRASE=""
export AWS_ACCESS_KEY_ID="$(sed -n 1p ~/.access_key)"
export AWS_SECRET_ACCESS_KEY="$(sed -n 2p ~/.access_key)"
export AWS_REGION=us-east-1

npm install
pulumi stack select bench
pulumi up            # provision
pulumi stack output  # IPs + ssh + broker endpoints
pulumi destroy       # tear down when done (stops billing)
```

Optional hardening: `pulumi config set sshCidr <your-ip>/32` to lock SSH to your
address (default is `0.0.0.0/0`, key-only auth).

## Setup scripts (`scripts/`)

VM setup is in standalone, idempotent scripts — the single source of truth.
Pulumi composes them into user-data for fresh provisions, and you can re-run them
on a live VM at any time (safe to run repeatedly):

| Script | Where | Does |
|--------|-------|------|
| `common-setup.sh` | both VMs | memlock/nofile limits, C + Rust toolchain |
| `broker-setup.sh` | broker only | mount NVMe at `/mnt/nvme`, write readiness marker |
| `preflight.sh` | broker | read-only io_uring/kernel/limits check before a run |

Re-run on a live VM:

```bash
scp -i ~/.ssh/id_ed25519 scripts/*.sh ec2-user@<vm>:/tmp/
ssh -i ~/.ssh/id_ed25519 ec2-user@<vm> 'sudo cloud-init status --wait; \
  /tmp/common-setup.sh && /tmp/broker-setup.sh && /tmp/preflight.sh'
```

> **Note:** because user-data is now composed from these scripts, a future
> `pulumi up` will see a user-data change and (with `userDataReplaceOnChange`)
> **replace** the instances — wiping the ephemeral NVMe. The running VMs were set
> up by executing the scripts directly, so they already match; only re-provision
> when you intend a fresh box.

## After provisioning

SSH in with your `~/.ssh/id_ed25519` key (output `sshBroker` / `sshProducer`).
On the broker, confirm the prep landed:

```bash
uname -r                                  # >= 5.19
cat /proc/sys/kernel/io_uring_disabled    # 0
ulimit -l                                 # unlimited
df -h /mnt/nvme                           # NVMe mounted
cat /etc/iggy-bench-ready                 # written when user-data finished
```

Build the broker stack (after `source ~/.cargo/env`):

```bash
RUSTFLAGS="-C target-cpu=native" cargo build --release --bin iggy-server
# + connectors runtime + doris plugin, same flags
```

The producer reaches the broker over the **private** network — see the
`brokerIggyEndpoint` / `brokerKafkaEndpoint` outputs (e.g.
`iggy://iggy:iggy@10.0.1.x:8090`).

## Cost

Two on-demand instances (~$0.43/hr `c7i.2xlarge` + ~$0.97/hr `c6id.4xlarge`,
us-east-1) ≈ **$1.4/hr** while running. `pulumi destroy` removes everything,
including the ephemeral NVMe data.
