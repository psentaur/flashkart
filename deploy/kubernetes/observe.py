"""Render local observability resources and the overview dashboard."""
import hashlib
import json
from pathlib import Path

panels = {
    "Application health": 'sum by (app) (up{job="flashkart"})',
    "HTTP requests per second": "sum by (app) (rate(flashkart_http_requests_total[1m]))",
    "Available stock by sale": "flashkart_inventory_available_stock",
    "Work waiting for delivery": "sum by (source) (flashkart_outbox_pending) or max by (service) (flashkart_workflow_pending)",
    "Replica backend failures": "sum by (app) (rate(flashkart_replica_sql_failures_total[1m]))",
    "Replica peer failures": 'sum by (app) (rate(flashkart_replica_peer_requests_total{result="error"}[1m]))',
    "Analytics event-time progress": '{__name__=~"flink_.*currentOutputWatermark"}',
    "Analytics late events": '{__name__=~"flink_.*lateEventsDropped"}',
}
dashboard = {
    "uid": "flashkart", "title": "Flashkart", "schemaVersion": 39, "refresh": "10s",
    "panels": [{"id": i + 1, "type": "timeseries", "title": name,
                "gridPos": {"x": 12 * (i % 2), "y": 8 * (i // 2), "w": 12, "h": 8},
                "datasource": {"type": "prometheus", "uid": "prometheus"},
                "targets": [{"refId": "A", "expr": expr}]}
               for i, (name, expr) in enumerate(panels.items())],
}
prometheus = r"""global:
  scrape_interval: 10s
scrape_configs:
- job_name: flashkart
  kubernetes_sd_configs:
  - role: pod
    namespaces:
      names: [flashkart]
  relabel_configs:
  - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_scrape]
    regex: "true"
    action: keep
  - source_labels: [__meta_kubernetes_pod_ip, __meta_kubernetes_pod_annotation_prometheus_io_port]
    regex: '(.+);(\d+)'
    replacement: '$1:$2'
    target_label: __address__
  - source_labels: [__address__, __meta_kubernetes_pod_annotation_prometheus_io_port]
    regex: '(.+);'
    replacement: '$1'
    target_label: __address__
  - source_labels: [__meta_kubernetes_pod_container_port_name, __meta_kubernetes_pod_annotation_prometheus_io_port, __meta_kubernetes_pod_container_name]
    regex: 'http;;.*|.*;9249;flink-main-container'
    action: keep
  - source_labels: [__meta_kubernetes_pod_label_app]
    target_label: app
"""
loki = """auth_enabled: false
server:
  http_listen_port: 3100
common:
  instance_addr: 127.0.0.1
  path_prefix: /tmp/loki
  replication_factor: 1
  ring:
    kvstore:
      store: inmemory
  storage:
    filesystem:
      chunks_directory: /tmp/loki/chunks
      rules_directory: /tmp/loki/rules
schema_config:
  configs:
  - from: 2024-01-01
    store: tsdb
    object_store: filesystem
    schema: v13
    index:
      prefix: index_
      period: 24h
"""
fluent = """[SERVICE]
    Flush 1
    Parsers_File /fluent-bit/etc/parsers.conf
[INPUT]
    Name tail
    Path /var/log/containers/*_flashkart_*.log
    Tag kube.*
    multiline.parser docker, cri
    DB /positions/tail.db
    Skip_Long_Lines On
    Inotify_Watcher Off
[OUTPUT]
    Name loki
    Match kube.*
    Host loki
    Port 3100
    Labels job=flashkart
"""
datasources = """apiVersion: 1
datasources:
- name: Prometheus
  uid: prometheus
  type: prometheus
  url: http://prometheus:9090
  isDefault: true
- name: Loki
  uid: loki
  type: loki
  url: http://loki:3100
"""
provider = """apiVersion: 1
providers:
- name: Flashkart
  options:
    path: /dashboards
"""
objects = []


def add(api, kind, name, **fields):
    objects.append(dict(apiVersion=api, kind=kind, metadata={"name": name}, **fields))


telemetry = {"prometheus.yml": prometheus, "loki.yml": loki,
    "fluent-bit.conf": fluent, "datasources.yaml": datasources, "provider.yaml": provider,
    "flashkart.json": json.dumps(dashboard)}
add("v1", "ConfigMap", "telemetry", data=telemetry)
# Mounted subPath files update when pods restart.
annotations = {"flashkart.io/telemetry-config": hashlib.sha256(
    json.dumps(telemetry, sort_keys=True).encode()).hexdigest()}
add("v1", "ServiceAccount", "prometheus")
add("rbac.authorization.k8s.io/v1", "Role", "prometheus",
    rules=[{"apiGroups": [""], "resources": ["pods"], "verbs": ["get", "list", "watch"]}])
add("rbac.authorization.k8s.io/v1", "RoleBinding", "prometheus",
    roleRef={"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": "prometheus"},
    subjects=[{"kind": "ServiceAccount", "name": "prometheus"}])
for name, image, port, args, files in (
    ("prometheus", "prom/prometheus:v3.5.0", 9090,
     ["--config.file=/etc/prometheus/prometheus.yml", "--storage.tsdb.retention.time=24h"],
     {"prometheus.yml": "/etc/prometheus/prometheus.yml"}),
    ("grafana", "grafana/grafana:12.1.0", 3000, [], {"datasources.yaml": "/etc/grafana/provisioning/datasources/flashkart.yaml",
         "provider.yaml": "/etc/grafana/provisioning/dashboards/flashkart.yaml", "flashkart.json": "/dashboards/flashkart.json"}),
    ("loki", "grafana/loki:3.5.3", 3100, ["-config.file=/etc/loki/local-config.yaml"],
     {"loki.yml": "/etc/loki/local-config.yaml"}),
    ("fluent-bit", "fluent/fluent-bit:4.0.6", None, ["-c", "/fluent-bit/etc/fluent-bit.conf"],
     {"fluent-bit.conf": "/fluent-bit/etc/fluent-bit.conf"}),
):
    selector = {"app": name}
    mounts = [{"name": "config", "mountPath": path, "subPath": key} for key, path in files.items()]
    volumes = [{"name": "config", "configMap": {"name": "telemetry"}}]
    container = {"name": name, "image": image, "args": args, "volumeMounts": mounts}
    spec = {"containers": [container], "volumes": volumes}
    if port:
        container["ports"] = [{"containerPort": port}]
        add("v1", "Service", name, spec={"selector": selector, "ports": [{"port": port}]})
    if name == "prometheus":
        spec["serviceAccountName"] = name
    if name == "fluent-bit":
        volumes.extend([{"name": "logs", "hostPath": {"path": "/var/log"}}, {"name": "positions", "emptyDir": {}}])
        mounts.extend([{"name": "logs", "mountPath": "/var/log", "readOnly": True},
                       {"name": "positions", "mountPath": "/positions"}])
    add("apps/v1", "Deployment" if port else "DaemonSet", name, spec={
        "selector": {"matchLabels": selector}, "template": {"metadata": {"labels": selector, "annotations": annotations}, "spec": spec},
    })

Path(__file__).with_name("kind").joinpath("observability.json").write_text(
    json.dumps({"apiVersion": "v1", "kind": "List", "items": objects}, indent=2) + "\n")
