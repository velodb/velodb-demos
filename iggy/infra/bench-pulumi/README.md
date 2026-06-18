# bench-pulumi

Provisions the two-VM AWS topology (us-east-1a) for the Iggy-vs-Kafka → VeloDB
Cloud benchmark.

| VM | Instance | Role |
|----|----------|------|
| `iggy-bench-producer` | `c7i.2xlarge` (8 vCPU) | isolated load generator |
| `iggy-bench-broker` | `c6id.4xlarge` (16 vCPU, local NVMe) | Iggy/Kafka + connectors |

Backend (VeloDB Cloud) is **not** managed here; provision it separately and put
its endpoint in the connector config.

Both VMs run **Ubuntu 24.04**, with user-data that raises `memlock`/`nofile` and
installs a Rust + C toolchain so you can build `iggy-server` on-box with
`target-cpu=native` (raw binary, never a container). The **broker** additionally
upgrades to **mainline kernel 7.0.10**, applies the io_uring + latency OS tuning
(swappiness, dirty ratios, 2048×2 MB hugepages, `io_uring_disabled=0`), and mounts
its local NVMe at `/mnt/nvme`.

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

VM setup is in standalone, idempotent scripts, the single source of truth.
Pulumi composes them into user-data for fresh provisions, and you can re-run them
on a live VM at any time (safe to run repeatedly):

| Script | Where | Does |
|--------|-------|------|
| `common-setup.sh` | both VMs | memlock/nofile limits, C + Rust toolchain (apt) |
| `broker-setup.sh` | broker only | install mainline kernel 7.0.10, OS tuning (sysctls + hugepages), mount NVMe, readiness marker |
| `preflight.sh` | broker | read-only kernel/io_uring/limits/tuning check before a run |
| `install-iggy.sh` | broker/producer | build Iggy (server + connectors + doris sink / bench) + connector config template |
| `install-kafka.sh` | broker/producer | Kafka 4.0 KRaft (no ZooKeeper) + client tools |
| `run-*.sh` | broker | launch iggy-server / connectors / kafka |

Re-run on a live VM:

```bash
scp -i ~/.ssh/id_ed25519 scripts/*.sh ubuntu@<vm>:/tmp/
ssh -i ~/.ssh/id_ed25519 ubuntu@<vm> 'sudo cloud-init status --wait; \
  /tmp/common-setup.sh && /tmp/broker-setup.sh && /tmp/preflight.sh'
```

> **Kernel reboot:** on first run `broker-setup.sh` installs mainline kernel
> 7.0.10 and flags a reboot. Reboot the broker, then re-run `broker-setup.sh`
> (idempotent); it skips the install and proceeds on the fresh kernel.

> **Note:** because user-data is now composed from these scripts, a future
> `pulumi up` will see a user-data change and (with `userDataReplaceOnChange`)
> **replace** the instances, wiping the ephemeral NVMe. The running VMs were set
> up by executing the scripts directly, so they already match; only re-provision
> when you intend a fresh box.

## After provisioning

SSH in with your `~/.ssh/id_ed25519` key (output `sshBroker` / `sshProducer`).
On the broker, confirm the prep landed:

```bash
uname -r                                  # 7.0.10-...  (>= 5.19 minimum)
cat /proc/sys/kernel/io_uring_disabled    # 0
cat /proc/sys/vm/swappiness               # 10
cat /proc/sys/vm/nr_hugepages             # 2048
ulimit -l                                 # unlimited
df -h /mnt/nvme                           # NVMe mounted
cat /etc/iggy-bench-ready                 # OS/kernel/tuning recorded when prep finished
```

Build the broker stack (after `source ~/.cargo/env`):

```bash
RUSTFLAGS="-C target-cpu=native" cargo build --release --bin iggy-server
# + connectors runtime + doris plugin, same flags
```

The producer reaches the broker over the **private** network; see the
`brokerIggyEndpoint` / `brokerKafkaEndpoint` outputs (e.g.
`iggy://iggy:iggybench@10.0.1.x:8090`).

## Cost

Two on-demand instances (~$0.43/hr `c7i.2xlarge` + ~$0.97/hr `c6id.4xlarge`,
us-east-1) ≈ **$1.4/hr** while running. `pulumi destroy` removes everything,
including the ephemeral NVMe data.
