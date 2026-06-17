#!/usr/bin/env bash
# Run the Kafka broker (KRaft combined mode, no ZooKeeper).
set -euo pipefail
KAFKA_HOME="${KAFKA_HOME:-/opt/kafka}"
exec "$KAFKA_HOME/bin/kafka-server-start.sh" "$KAFKA_HOME/config/server.properties"
