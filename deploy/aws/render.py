"""Render non-secret cloud GitOps objects from Terraform's deployment output."""
import hashlib
import json
from pathlib import Path
import sys

root = Path(__file__).resolve().parents[2]
output = Path(sys.argv[1])
settings = json.load(sys.stdin)
base = json.loads((root / "deploy/kubernetes/base/resources.json").read_text())["items"]
observability = json.loads((root / "deploy/kubernetes/kind/observability.json").read_text())["items"]
objects = []
aws_env = {"AWS_REGION": settings["region"], "AWS_STS_REGIONAL_ENDPOINTS": "regional"}


def add(api, kind, name, **fields):
    objects.append(dict(apiVersion=api, kind=kind, metadata={"name": name}, **fields))


def image(component):
    return f"{settings['repository']}:{component}-{settings['tag']}"


def env(values):
    return [{"name": key, "value": value} for key, value in values.items()]


for name, role in settings["roles"].items():
    add("v1", "ServiceAccount", name)
    objects[-1]["metadata"]["annotations"] = {"eks.amazonaws.com/role-arn": role}
add("v1", "ConfigMap", "flink-kafka", data={"client.properties": "\n".join([
    "security.protocol=SASL_SSL", "sasl.mechanism=AWS_MSK_IAM",
    "sasl.jaas.config=software.amazon.msk.auth.iam.IAMLoginModule required;",
    "sasl.client.callback.handler.class=software.amazon.msk.auth.iam.IAMClientCallbackHandler",
])})

for obj in base:
    name = obj["metadata"]["name"]
    kind = obj["kind"]
    if name.startswith("postgres-") or name == "kafka":
        continue
    if name == "service-config":
        for key, value in obj["data"].items():
            config = json.loads(value)
            config.update(environment="aws", aws={"region": settings["region"], "endpoints": {}},
                          kafka={"brokers": settings["brokers"], "auth": "iam"})
            obj["data"][key] = json.dumps(config)
    elif name == "migrations":
        obj["data"].pop("adapter")
        obj["data"]["migrate.sh"] = (root / "deploy/aws/migrate.sh").read_text()
        obj["data"]["databases.json"] = json.dumps(settings["databases"])
    elif kind == "Job":
        pod = obj["spec"]["template"]["spec"]
        if name == "migrate":
            pod["serviceAccountName"] = "migrations"
            pod["containers"] = [{
                "name": "migrate", "image": image("migrations"), "command": ["sh", "/sql/migrate.sh"],
                "env": env(aws_env), "volumeMounts": [
                    {"name": volume, "mountPath": path, "readOnly": True}
                    for volume, path in [("sql", "/sql"), ("database", "/database"), ("rds-ca", "/rds")]],
            }]
            pod["volumes"].extend([
                {"name": "database", "secret": {"secretName": "migration-passwords"}},
                {"name": "rds-ca", "configMap": {"name": "rds-ca"}},
            ])
        else:
            pod["serviceAccountName"] = "kafka-topics"
            pod["containers"] = [{
                "name": "topics", "image": image("app"), "command": ["kafka-topics"],
                "env": env(aws_env | {"KAFKA_BROKERS": ",".join(settings["brokers"])})}]
    elif kind == "Deployment":
        pod = obj["spec"]["template"]["spec"]
        container = pod["containers"][0]
        container["image"] = image("web" if name == "web" else "app")
        if name != "web":
            pod["containers"] = [container]
            pod["volumes"] = [v for v in pod["volumes"] if v["name"] != "emulator"]
            container["volumeMounts"] = [m for m in container["volumeMounts"] if m["name"] != "emulator"]
            container["envFrom"] = [{"secretRef": {"name": "demo-tokens"}}] if name in {
                "product-catalog", "flash-sale-manager", "admission-controller", "order-service", "notification"} else []
            dsns = [e["name"] for e in container["env"] if e["name"].endswith("_DSN")]
            container["env"] = [e for e in container["env"] if e["name"] not in dsns]
            if dsns:
                pod["volumes"].append({"name": "rds-ca", "configMap": {"name": "rds-ca"}})
                container["volumeMounts"].append({"name": "rds-ca", "mountPath": "/rds", "readOnly": True})
                container["env"].extend({"name": key, "valueFrom": {"secretKeyRef": {
                    "name": f"db-{name}", "key": "dsn"}}} for key in dsns)
            if name in settings["roles"]:
                pod["serviceAccountName"] = name
                container["env"].extend(env(aws_env))
            extra = {
                "admission-controller": {"FLASHKART_PURCHASE_QUEUE_URL": settings["queues"]["purchases"],
                                         "FLASHKART_NOTIFICATION_TABLE": settings["tables"]["notifications"]},
                "inventory-reservation": {"FLASHKART_PURCHASE_QUEUE_URL": settings["queues"]["purchases"],
                                          "FLASHKART_OUTCOME_QUEUE_URL": settings["queues"]["outcomes"]},
                "order-service": {"FLASHKART_OUTCOME_QUEUE_URL": settings["queues"]["outcomes"]},
                "notification": {"FLASHKART_NOTIFICATION_TABLE": settings["tables"]["notifications"]},
                "analytics-api": {"FLASHKART_ANALYTICS_TABLE": settings["tables"]["analytics"]},
                "product-catalog": {"FLASHKART_PRODUCT_IMAGES_BUCKET": settings["buckets"]["images"]},
            }
            container["env"].extend(env(extra.get(name, {})))
        if name.startswith("replica-"):
            labels = obj["spec"]["template"]["metadata"]["labels"]
            labels["flashkart-replica"] = "true"
            pod["topologySpreadConstraints"] = [{
                "maxSkew": 1, "topologyKey": "topology.kubernetes.io/zone", "whenUnsatisfiable": "ScheduleAnyway",
                "labelSelector": {"matchLabels": {"flashkart-replica": "true"}}}]
    objects.append(obj)

add("elbv2.k8s.aws/v1beta1", "TargetGroupBinding", "api", spec={
    "serviceRef": {"name": "web", "port": 8080}, "targetGroupARN": settings["target_group"], "targetType": "ip",
})
objects[-1]["metadata"]["annotations"] = {"argocd.argoproj.io/sync-wave": "3"}
checkpoint = f"s3://{settings['buckets']['checkpoints']}"
add("rbac.authorization.k8s.io/v1", "RoleBinding", "flink",
    subjects=[{"kind": "ServiceAccount", "name": "flink"}],
    roleRef={"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": "edit"})
add("flink.apache.org/v1beta1", "FlinkDeployment", "sales-analytics", spec={
    "image": image("flink"), "flinkVersion": "v1_20", "serviceAccount": "flink",
    "flinkConfiguration": {
        "high-availability.type": "kubernetes", "high-availability.storageDir": checkpoint + "/ha",
        "state.checkpoints.dir": checkpoint + "/checkpoints", "taskmanager.numberOfTaskSlots": "1",
        "fs.s3a.endpoint.region": settings["region"],
        "metrics.reporter.prom.factory.class": "org.apache.flink.metrics.prometheus.PrometheusReporterFactory",
        "metrics.reporter.prom.port": "9249",
    },
    "podTemplate": {"metadata": {"annotations": {"prometheus.io/scrape": "true", "prometheus.io/port": "9249"}},
                    "spec": {"volumes": [{"name": "kafka", "configMap": {"name": "flink-kafka"}}],
                             "containers": [{"name": "flink-main-container", "volumeMounts": [
                                 {"name": "kafka", "mountPath": "/kafka", "readOnly": True}]}]}},
    "jobManager": {"resource": {"memory": "1024m", "cpu": 0.5}},
    "taskManager": {"resource": {"memory": "1536m", "cpu": 0.5}},
    "job": {"jarURI": "local:///opt/flink/usrlib/analytics.jar", "parallelism": 1, "upgradeMode": "last-state",
            "args": ["--brokers", ",".join(settings["brokers"]), "--checkpoints", checkpoint + "/checkpoints",
                     "--transactional-id-prefix", settings["name"] + "-sales", "--kafka-properties", "/kafka/client.properties"]},
})
objects[-1]["metadata"]["annotations"] = {"argocd.argoproj.io/sync-wave": "2"}

for obj in observability:
    name = obj["metadata"]["name"]
    if name == "loki":
        continue
    if obj["kind"] == "DaemonSet" and name == "fluent-bit":
        pod = obj["spec"]["template"]["spec"]
        pod["serviceAccountName"] = name
        pod["containers"][0]["env"] = env(aws_env | {"AWS_EC2_METADATA_DISABLED": "true"})
    if name == "telemetry":
        config = obj["data"]["fluent-bit.conf"].split("[OUTPUT]")[0]
        obj["data"]["fluent-bit.conf"] = config + f"""[OUTPUT]
    Name cloudwatch_logs
    Match kube.*
    region {settings['region']}
    log_group_name {settings['logs']}
    log_stream_prefix pod-
    auto_create_group false
"""
        obj["data"]["datasources.yaml"] = obj["data"]["datasources.yaml"].split("- name: Loki")[0]
        del obj["data"]["loki.yml"]
    objects.append(obj)

telemetry = next(obj["data"] for obj in observability if obj["kind"] == "ConfigMap" and obj["metadata"]["name"] == "telemetry")
checksum = hashlib.sha256(json.dumps(telemetry, sort_keys=True).encode()).hexdigest()
for obj in observability:
    if (obj["kind"], obj["metadata"]["name"]) in {
        ("Deployment", "prometheus"), ("Deployment", "grafana"), ("DaemonSet", "fluent-bit")
    }:
        obj["spec"]["template"]["metadata"]["annotations"]["flashkart.io/telemetry-config"] = checksum

output.mkdir(parents=True, exist_ok=True)
(output / "resources.json").write_text(json.dumps({"apiVersion": "v1", "kind": "List", "items": objects}, indent=2) + "\n")
(output / "kustomization.yaml").write_text("""apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: flashkart
resources:
  - resources.json
""")
