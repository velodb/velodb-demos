# syntax=docker/dockerfile:1.7
#
# velodb-demo-grafana — Grafana 11.5.2 with the doris-app plugin baked in.
#
# Plugin source is shipped under doris-app/. We drop the wrong-arch backend
# at build time so the resulting image only carries the binary it can run.

FROM grafana/grafana:11.5.2

ARG TARGETARCH

USER root
COPY doris-app/ /var/lib/grafana/plugins/doris-app/

# Keep only the matching backend binary; remove the other to save ~22 MB.
RUN set -eu; \
    case "${TARGETARCH}" in \
      amd64)  rm -f /var/lib/grafana/plugins/doris-app/gpx_doris-app_linux_arm64 ;; \
      arm64)  rm -f /var/lib/grafana/plugins/doris-app/gpx_doris-app_linux_amd64 ;; \
      *)      ;; \
    esac && \
    chown -R grafana:root /var/lib/grafana/plugins/doris-app

USER grafana
