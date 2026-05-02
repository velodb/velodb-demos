# syntax=docker/dockerfile:1.7

# -----------------------------------------------------------------------------
# Stage 1 — build (runs on the build machine's native arch via BUILDPLATFORM,
# JS output is platform-independent so we don't pay a cross-arch penalty).
# -----------------------------------------------------------------------------
FROM --platform=$BUILDPLATFORM node:20-alpine AS builder

WORKDIR /app

COPY package*.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci --no-audit --no-fund

COPY . .

# Build-time URL overrides. Default to relative paths so nginx can proxy.
ARG VITE_API_BASE_URL=""
ARG VITE_GRAFANA_URL=http://localhost:33000
ARG VITE_OTEL_FRONTEND_URL=http://localhost:8080
ARG VITE_OTEL_FEATURE_FLAG_URL=http://localhost:8080/feature
ENV VITE_API_BASE_URL=$VITE_API_BASE_URL \
    VITE_GRAFANA_URL=$VITE_GRAFANA_URL \
    VITE_OTEL_FRONTEND_URL=$VITE_OTEL_FRONTEND_URL \
    VITE_OTEL_FEATURE_FLAG_URL=$VITE_OTEL_FEATURE_FLAG_URL

RUN npm run build

# -----------------------------------------------------------------------------
# Stage 2 — runtime (~25 MB)
# -----------------------------------------------------------------------------
FROM nginx:1.27-alpine

COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=builder /app/dist /usr/share/nginx/html

EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]
