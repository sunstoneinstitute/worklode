
The ClickStack collector discovers targets by Service annotations, so a dedicated single-port `worklode-metrics` Service (`deploy/base/metrics-service.yaml`) carries `prometheus.io/scrape: "true"`. Scraped metrics flow directly to ClickHouse, bypassing the otel-gateway. Pod-template annotations carry nothing.

