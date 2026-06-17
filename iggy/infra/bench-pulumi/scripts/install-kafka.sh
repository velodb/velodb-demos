#!/usr/bin/env bash
# Install Kafka in KRaft combined mode (no ZooKeeper). Role-aware via KAFKA_ROLE:
#   broker -> configure (NVMe log dir, advertised private IP) + format storage (default)
#   client -> just unpack, for kafka-*-perf-test.sh on the producer VM
# Idempotent: re-download only if absent, re-format only if storage is unformatted.
set -euxo pipefail

KAFKA_VERSION="${KAFKA_VERSION:-4.0.0}"
SCALA_VERSION="${SCALA_VERSION:-2.13}"
KAFKA_HOME="${KAFKA_HOME:-/opt/kafka}"
LOG_DIR="${KAFKA_LOG_DIR:-/mnt/nvme/kafka-logs}"
KAFKA_ROLE="${KAFKA_ROLE:-broker}"

# Kafka 4.0 needs Java >= 17.
sudo apt-get update -y && sudo apt-get install -y openjdk-17-jdk-headless

TGZ="kafka_${SCALA_VERSION}-${KAFKA_VERSION}.tgz"
if [ ! -x "$KAFKA_HOME/bin/kafka-server-start.sh" ]; then
  cd /tmp
  curl -fsSLO "https://downloads.apache.org/kafka/${KAFKA_VERSION}/${TGZ}" \
    || curl -fsSLO "https://archive.apache.org/dist/kafka/${KAFKA_VERSION}/${TGZ}"
  sudo mkdir -p "$KAFKA_HOME"
  sudo tar -xzf "$TGZ" -C "$KAFKA_HOME" --strip-components=1
  sudo chown -R "$USER" "$KAFKA_HOME"
fi

if [ "$KAFKA_ROLE" = "client" ]; then
  echo "install-kafka.sh: client tools at $KAFKA_HOME/bin (perf-test scripts)"
  exit 0
fi

PRIVATE_IP=$(hostname -I | awk '{print $1}')
sudo mkdir -p "$LOG_DIR" && sudo chown "$USER" "$LOG_DIR"

# Single-node KRaft combined config. Logs on NVMe; advertise the private IP so
# the producer VM connects. 4 partitions / RF=1 to match the Iggy side.
CFG="$KAFKA_HOME/config/server.properties"
cat > "$CFG" <<EOF
process.roles=broker,controller
node.id=1
controller.quorum.voters=1@localhost:9093
listeners=PLAINTEXT://0.0.0.0:9092,CONTROLLER://0.0.0.0:9093
advertised.listeners=PLAINTEXT://${PRIVATE_IP}:9092
controller.listener.names=CONTROLLER
listener.security.protocol.map=PLAINTEXT:PLAINTEXT,CONTROLLER:PLAINTEXT
inter.broker.listener.name=PLAINTEXT
log.dirs=${LOG_DIR}
num.partitions=4
default.replication.factor=1
offsets.topic.replication.factor=1
transaction.state.log.replication.factor=1
transaction.state.log.min.isr=1
num.network.threads=8
num.io.threads=16
EOF

if [ ! -f "$LOG_DIR/meta.properties" ]; then
  CLUSTER_ID=$("$KAFKA_HOME/bin/kafka-storage.sh" random-uuid)
  "$KAFKA_HOME/bin/kafka-storage.sh" format -t "$CLUSTER_ID" -c "$CFG"
fi
echo "install-kafka.sh: done (KRaft, advertised ${PRIVATE_IP}:9092). Start with run-kafka.sh"
