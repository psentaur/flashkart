"""Regenerate the Kubernetes base from the shared service and migration configuration."""
import json
from pathlib import Path

root = Path(__file__).resolve().parents[2]
base = root / "deploy/kubernetes/base"
objects = []
replicas = ("a", "b", "c", "d")


def add(api, kind, name, wave=0, **fields):
    resource = dict(apiVersion=api, kind=kind, metadata=dict(name=name), **fields)
    if wave:
        resource["metadata"]["annotations"] = {"argocd.argoproj.io/sync-wave": str(wave)}
    objects.append(resource)
    return resource


def service(name, **ports):
    add("v1", "Service", name, spec={
        "selector": {"app": name}, "ports": [
            {"name": label, "port": port, "targetPort": port} for label, port in ports.items()
        ],
    })


def pod(name, container, **spec):
    return {"metadata": {"labels": {"app": name}}, "spec": {"containers": [container], **spec}}


def env(values):
    return [{"name": key, "value": str(value)} for key, value in values.items()]


def probe(path):
    return {"httpGet": {"path": path, "port": 8080}, "periodSeconds": 5, "timeoutSeconds": 2}


config = json.loads((root / "deploy/config/local.example.json").read_text())
config.update(http_address="0.0.0.0:8080", grpc_address="0.0.0.0:7001",
              peers={key: f"replica-{key}:7001" for key in replicas},
              inventory_grpc_address="inventory-reservation:7001", order_grpc_address="order-service:7001",
              payment_grpc_address="payment-service:7001")
config["aws"] = {"region": "us-east-1", "endpoints": {
    service: "https://127.0.0.1:9297" for service in ("sqs", "dynamodb")}}
config["kafka"]["brokers"] = ["kafka:9092"]
configs = {}
for name in ("market", *replicas):
    cfg = dict(config, replica_id=name) if name in replicas else config
    configs[name + ".json"] = json.dumps(cfg)
add("v1", "ConfigMap", "service-config", data=configs)

for backend in (*replicas, "transactional"):
    name = f"postgres-{backend}"
    service(name, sql=5432)
    container = {"name": "postgres", "image": "postgres:17", "ports": [{"containerPort": 5432}],
                 "env": env(dict(POSTGRES_USER="flashkart_admin", POSTGRES_PASSWORD="flashkart-local-admin",
                                 POSTGRES_DB="flashkart")),
                 "readinessProbe": {"exec": {"command": ["pg_isready", "-U", "flashkart_admin", "-d", "flashkart"]}},
                 "volumeMounts": [{"name": "data", "mountPath": "/var/lib/postgresql/data"}]}
    add("apps/v1", "StatefulSet", name, spec={
        "serviceName": name, "selector": {"matchLabels": {"app": name}}, "template": pod(name, container),
        "volumeClaimTemplates": [{"apiVersion": "v1", "kind": "PersistentVolumeClaim", "metadata": {"name": "data"}, "spec": {
            "accessModes": ["ReadWriteOnce"], "volumeMode": "Filesystem", "resources": {"requests": {"storage": "1Gi"}}}}],
    })

migration = (root / "deploy/local/migrate.sh").read_text()
# Preserve the existing role creation and SQL ordering. The adapter replaces compose exec with TCP psql.
sql = {f"{group}-{path.name}": path.read_text() for group in ("replica", "transactional")
       for path in sorted((root / "migrations" / group).glob("*.sql"))}
sql["migrate.sh"] = migration
sql["adapter"] = """#!/bin/sh
set -eu
shift 2
database=$1
shift 2
exec psql -h "$database" "$@"
"""
add("v1", "ConfigMap", "migrations", data=sql)
add("batch/v1", "Job", "migrate", 1, spec={"backoffLimit": 4, "template": pod("migrate", {
    "name": "migrate", "image": "postgres:17", "env": env({"PGPASSWORD": "flashkart-local-admin"}),
    "command": ["sh", "-ec", '''
mkdir -p /tmp/migrations/replica /tmp/migrations/transactional
for group in replica transactional; do
    for path in /sql/"$group"-*.sql; do
        name=${path##*/}
        cp "$path" "/tmp/migrations/$group/${name#*-}"
    done
done
cd /tmp
sh /sql/migrate.sh /sql/adapter
'''], "volumeMounts": [{"name": "sql", "mountPath": "/sql"}]},
    restartPolicy="Never", volumes=[{"name": "sql", "configMap": {"name": "migrations", "defaultMode": 365}}])})
objects[-1]["metadata"]["annotations"].update({
    "argocd.argoproj.io/hook": "Sync", "argocd.argoproj.io/hook-delete-policy": "BeforeHookCreation"})

service("kafka", kafka=9092)
kafka_env = dict(KAFKA_NODE_ID=1, KAFKA_PROCESS_ROLES="broker,controller",
                 KAFKA_LISTENERS="BROKER://:9092,CONTROLLER://:9093",
                 KAFKA_ADVERTISED_LISTENERS="BROKER://kafka:9092",
                 KAFKA_LISTENER_SECURITY_PROTOCOL_MAP="BROKER:PLAINTEXT,CONTROLLER:PLAINTEXT",
                 KAFKA_INTER_BROKER_LISTENER_NAME="BROKER", KAFKA_CONTROLLER_LISTENER_NAMES="CONTROLLER",
                 KAFKA_CONTROLLER_QUORUM_VOTERS="1@kafka:9093", KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR=1,
                 KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR=1, KAFKA_TRANSACTION_STATE_LOG_MIN_ISR=1,
                 KAFKA_LOG_DIRS="/var/lib/kafka/data")
# A controller must resolve its own service before broker readiness.
objects[-1]["spec"]["publishNotReadyAddresses"] = True
objects[-1]["spec"]["ports"].append({"name": "controller", "port": 9093})
add("apps/v1", "StatefulSet", "kafka", spec={
    "serviceName": "kafka", "selector": {"matchLabels": {"app": "kafka"}},
    "template": pod("kafka", {"name": "kafka", "image": "apache/kafka:3.9.1", "env": env(kafka_env),
                             "readinessProbe": {"tcpSocket": {"port": 9092}},
                             "volumeMounts": [{"name": "kafka", "mountPath": "/var/lib/kafka/data"}]},
                    securityContext={"fsGroup": 1000}),
    "volumeClaimTemplates": [{"apiVersion": "v1", "kind": "PersistentVolumeClaim", "metadata": {"name": "kafka"}, "spec": {
        "accessModes": ["ReadWriteOnce"], "volumeMode": "Filesystem", "resources": {"requests": {"storage": "2Gi"}}}}],
})
add("batch/v1", "Job", "kafka-topics", 1, spec={"backoffLimit": 5, "template": pod("kafka-topics", {
    "name": "topics", "image": "apache/kafka:3.9.1", "command": ["sh", "-ec", '''
for topic in purchase-events analytics-results; do
    /opt/kafka/bin/kafka-topics.sh --bootstrap-server kafka:9092 --create --if-not-exists \
        --topic "$topic" --partitions 3 --replication-factor 1
done
''']}, restartPolicy="Never")})
objects[-1]["metadata"]["annotations"].update({
    "argocd.argoproj.io/hook": "Sync", "argocd.argoproj.io/hook-delete-policy": "BeforeHookCreation"})

roles = {"flash-sale-manager": "flash_sale", "inventory-reservation": "inventory",
         "admission-controller": "admission", "order-service": "orders", "payment-service": "payment"}
grpc_services = {"inventory-reservation", "order-service", "payment-service"}
emulated = {"inventory-reservation", "admission-controller", "order-service", "notification", "analytics-api"}
for name in (*[f"replica-{key}" for key in replicas], *roles, "product-catalog", "notification", "analytics-api"):
    replica = name.removeprefix("replica-") if name.startswith("replica-") else None
    executable = "catalog-replica" if replica else name
    port_map = dict(http=8080, grpc=7001) if replica or name in grpc_services else dict(http=8080)
    service(name, **port_map)
    values = {"FLASHKART_CONFIG": f"/config/{replica or 'market'}.json"}
    if name in roles:
        values["FLASHKART_PUBLISH_EVENTS"] = "1"
    if replica:
        values[f"FLASHKART_REPLICA_{replica.upper()}_DSN"] = (
            f"postgres://replica_app:flashkart-local-{replica}@postgres-{replica}/flashkart?sslmode=disable&pool_max_conns=8")
    if name in roles:
        role = roles[name] + "_app"
        values["FLASHKART_TRANSACTIONAL_DSN"] = (
            f"postgres://{role}:flashkart-local-{role}@postgres-transactional/flashkart?sslmode=disable&pool_max_conns=8")
    mounts = [{"name": "config", "mountPath": "/config", "readOnly": True}]
    volumes = [{"name": "config", "configMap": {"name": "service-config"}}]
    container = {"name": name, "image": "flashkart-app", "imagePullPolicy": "IfNotPresent", "command": [executable],
                 "env": env(values), "ports": [{"name": key, "containerPort": port} for key, port in port_map.items()],
                 "envFrom": [{"secretRef": {"name": "demo-tokens"}}],
                 "volumeMounts": mounts, "readinessProbe": probe("/readyz"), "livenessProbe": probe("/healthz"),
                 "resources": {"requests": {"cpu": "50m", "memory": "48Mi"}, "limits": {"memory": "192Mi"}}}
    template = pod(name, container, volumes=volumes, terminationGracePeriodSeconds=30)
    template["metadata"]["annotations"] = {"prometheus.io/scrape": "true"}
    if name in emulated:
        container["envFrom"].append({"secretRef": {"name": "emulator-settings"}})
        mounts.append({"name": "emulator", "mountPath": "/emulator", "readOnly": True})
        volumes.append({"name": "emulator", "secret": {"secretName": "emulator-files"}})
        template["spec"]["containers"].append({
            "name": "emulator-tunnel", "image": "flashkart-proxy", "imagePullPolicy": "IfNotPresent",
            "args": ["TCP-LISTEN:9297,bind=127.0.0.1,reuseaddr,fork", "TCP:emulator:9297"],
            "resources": {"requests": {"cpu": "10m", "memory": "8Mi"}},
        })
    # The market config opens a gRPC listener. Only business RPC services expose it.
    spec = {"selector": {"matchLabels": {"app": name}}, "template": template}
    if replica:
        spec["strategy"] = {"type": "Recreate"}
    if name not in ("product-catalog", "admission-controller"):
        spec["replicas"] = 1
    add("apps/v1", "Deployment", name, 2, spec=spec)
    if name in ("product-catalog", "admission-controller"):
        add("autoscaling/v2", "HorizontalPodAutoscaler", name, 2, spec={
            "scaleTargetRef": {"apiVersion": "apps/v1", "kind": "Deployment", "name": name},
            "minReplicas": 2, "maxReplicas": 5, "metrics": [{"type": "Resource", "resource": {
                "name": "cpu", "target": {"type": "Utilization", "averageUtilization": 60}}}],
        })

service("web", http=8080)
add("apps/v1", "Deployment", "web", 3, spec={
    "selector": {"matchLabels": {"app": "web"}},
    "template": pod("web", {"name": "nginx", "image": "flashkart-web", "imagePullPolicy": "IfNotPresent", "readinessProbe": probe("/")}),
})

(base / "resources.json").write_text(json.dumps({"apiVersion": "v1", "kind": "List", "items": objects}, indent=2) + "\n")
