# Observability Stack

The observability stack introduced in Initiative 1 · Epic 3 ships with:

- **OpenTelemetry SDK** embedded in every service. Traces are exported to the OpenTelemetry Collector at `otel-collector:4317`.
- **Prometheus exporter** per service. Metrics endpoints are exposed on ports 9090–9094 (`/metrics`) and scraped by Prometheus.
- **Grafana** with provisioned datasources (Prometheus + Tempo) and a sample “Worker Overview” dashboard.
- **Tempo** stores traces for quick inspection via Grafana Explore.

## Running locally

```bash
docker compose up otel-collector tempo prometheus grafana -d
docker compose up api worker executor crawler memory -d
```

Visit Grafana at http://localhost:3001 (default credentials `admin` / `admin`). The “Worker Overview” dashboard visualises run/task counters and checkpoint retries. Use Grafana Explore → Tempo to view traces created by the worker service.

Prometheus is available at http://localhost:9090.

## Adding new instrumentation

1. Initialize telemetry in your service with `runtime.SetupTelemetry`. Pass a unique metrics port if you need one.
2. Create counters, histograms, or spans using the returned `Meter` and `Tracer`.
3. Grafana dashboards can be added under `observability/grafana/dashboards` and will auto-provision on startup.

## Alerting

Prometheus is prepared for rule files (`observability/prometheus.yml`). Add alerting rules and configure receivers to surface queue backlogs or latency issues.
