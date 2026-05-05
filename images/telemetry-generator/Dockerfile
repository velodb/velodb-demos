# syntax=docker/dockerfile:1.7

# -----------------------------------------------------------------------------
# Stage 1 — fetch the fat JAR once on the native build platform.
# (cisco-open/test-telemetry-generator is a single fat JAR, arch-independent.)
# -----------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM alpine:3.20 AS fetch

ARG TG_VERSION=v23.7.0
ARG TG_JAR_URL=https://github.com/cisco-open/test-telemetry-generator/releases/download/${TG_VERSION}/test-telemetry-generator-${TG_VERSION}-fat.jar

RUN apk add --no-cache curl ca-certificates && \
    mkdir -p /out && \
    curl -fsSL -o /out/telemetry-generator.jar "${TG_JAR_URL}"

# -----------------------------------------------------------------------------
# Stage 2 — runtime (multi-arch JRE provided upstream by Eclipse Temurin)
# -----------------------------------------------------------------------------
FROM eclipse-temurin:17-jre

WORKDIR /app
COPY --from=fetch /out/telemetry-generator.jar /app/telemetry-generator.jar
RUN mkdir -p /definitions

ENTRYPOINT ["java", "-jar", "/app/telemetry-generator.jar"]
