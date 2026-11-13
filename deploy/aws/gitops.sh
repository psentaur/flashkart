#!/bin/sh
set -eu
: "${GITOPS_REPO:?Set the URL of the cloud manifest repository}"
: "${GITOPS_REVISION:?Set the deployment revision}"
: "${GITOPS_PATH:?Set the path to the rendered overlay}"
: "${FLASHKART_ADMIN_TOKEN:?Set the administrator token}"
: "${FLASHKART_CUSTOMER_TOKEN:?Set the customer token}"
# Pin symbolic refs to the commit requested by this invocation.
if printf '%s\n' "$GITOPS_REVISION" | grep -Eq '^[[:xdigit:]]{40}$'; then
    revision=$(printf '%s' "$GITOPS_REVISION" | tr 'A-F' 'a-f')
else
    revision=$(git ls-remote --exit-code "$GITOPS_REPO" "$GITOPS_REVISION" "$GITOPS_REVISION^{}" |
        awk '$2 ~ /\^\{\}$/ { peeled=$1; next } { sha=$1; refs++ }
             END { if (refs == 1) print (peeled != "" ? peeled : sha); else exit 1 }' ) || {
        echo 'GITOPS_REVISION must resolve to one Git ref or be a full commit SHA.' >&2
        exit 2
    }
fi
export FLASHKART_GITOPS_COMMIT="$revision"
work=$(mktemp -d)
chmod 700 "$work"
trap 'rm -rf "$work"' EXIT
tf() { terraform -chdir=deploy/aws/environment "$@"; }
tf output -json deployment >"$work/settings.json"
tf output -json database_passwords >"$work/passwords.json"
name=$(jq -er .name "$work/settings.json")
region=$(jq -er .region "$work/settings.json")
export KUBECONFIG="$work/kubeconfig"
aws eks update-kubeconfig --name "$name" --region "$region" >/dev/null
ns() { kubectl create namespace "$1" --dry-run=client -o yaml | kubectl apply -f -; }
ns argocd
ns flashkart
kubectl apply --server-side -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/v3.1.8/manifests/install.yaml
kubectl wait --for=condition=Established crd/applications.argoproj.io --timeout=180s

curl -fsSL https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem -o "$work/ca.pem"
kubectl -n flashkart create configmap rds-ca --from-file="$work/ca.pem" --dry-run=client -o yaml | kubectl apply -f -
kubectl -n flashkart create secret generic demo-tokens \
    --from-literal=FLASHKART_ADMIN_TOKEN="$FLASHKART_ADMIN_TOKEN" \
    --from-literal=FLASHKART_CUSTOMER_TOKEN="$FLASHKART_CUSTOMER_TOKEN" \
    --from-literal=FLASHKART_CUSTOMER_ID=cloud-buyer --dry-run=client -o yaml | kubectl apply -f -

python3 - "$work" <<'PY' | kubectl apply -f -
import json, pathlib, sys
work = pathlib.Path(sys.argv[1])
settings = json.loads((work / "settings.json").read_text())
passwords = json.loads((work / "passwords.json").read_text())
objects = []
def secret(name, data):
    objects.append(dict(apiVersion="v1", kind="Secret", metadata=dict(name=name, namespace="flashkart"), stringData=data))
secret("migration-passwords", passwords)
roles = {"flash-sale-manager":"flash_sale_app", "inventory-reservation":"inventory_app",
         "admission-controller":"admission_app", "order-service":"orders_app", "payment-service":"payment_app"}
for name, key in {**roles, **{f"replica-{k}": k for k in "abcd"}}.items():
    backend = key if key in "abcd" else "transactional"
    role = "replica_app" if key in "abcd" else key
    host = settings["databases"][backend]["host"]
    secret(f"db-{name}", {"dsn": f"postgres://{role}:{passwords[key]}@{host}/flashkart?sslmode=verify-full&sslrootcert=/rds/ca.pem&pool_max_conns=8"})
print(json.dumps(dict(apiVersion="v1", kind="List", items=objects)))
PY

python3 - "$work/settings.json" <<'PY' | kubectl apply -f -
import json, os, sys
s = json.load(open(sys.argv[1]))
apps = []
def app(name, namespace, source):
    apps.append({"apiVersion":"argoproj.io/v1alpha1", "kind":"Application",
       "metadata":{"name":name,"namespace":"argocd","finalizers":["resources-finalizer.argocd.argoproj.io"]},
       "spec":{"project":"default","source":source,"destination":{"server":"https://kubernetes.default.svc","namespace":namespace},
               "syncPolicy":{"automated":{"prune":True,"selfHeal":True},"syncOptions":["CreateNamespace=true","ServerSideApply=true"]}}})
def chart(repo, name, rev, values):
    return {"repoURL":repo,"chart":name,"targetRevision":rev,"helm":{"valuesObject":values}}
app("cloud-lb-controller","kube-system",chart("https://aws.github.io/eks-charts","aws-load-balancer-controller","1.13.4",{
    "clusterName":s["name"],"fullnameOverride":"aws-load-balancer-controller","region":s["region"],"vpcId":s["vpc"],
    "serviceAccount":{"annotations":{"eks.amazonaws.com/role-arn":s["controller_role"]}}}))
app("cloud-metrics-server","kube-system",chart("https://kubernetes-sigs.github.io/metrics-server/","metrics-server","3.13.0",{}))
app("cloud-flink-operator","flink-operator",chart("public.ecr.aws/emr-on-eks","flink-kubernetes-operator","7.13.0",{
    "watchNamespaces":["flashkart"],"webhook":{"create":False},"emrContainers":{"awsRegion":s["region"]}}))
print(json.dumps({"apiVersion":"v1","kind":"List","items":apps}))
PY
kubectl -n argocd create secret generic emr-helm-repo \
    --from-literal=type=helm --from-literal=name=emr-on-eks \
    --from-literal=url=public.ecr.aws/emr-on-eks --from-literal=enableOCI=true \
    --dry-run=client -o yaml | kubectl label --local -f - argocd.argoproj.io/secret-type=repository -o yaml | kubectl apply -f -
for crd in flinkdeployments.flink.apache.org targetgroupbindings.elbv2.k8s.aws; do
    for n in $(seq 1 90); do
        if kubectl get crd "$crd" >/dev/null 2>&1; then break; fi
        sleep 5
    done
    kubectl wait --for=condition=Established "crd/$crd" --timeout=120s
done
kubectl -n kube-system rollout status deployment/aws-load-balancer-controller --timeout=300s
consumers="admission-controller flash-sale-manager notification order-service product-catalog"
export FLASHKART_TOKEN_CONSUMERS="$consumers"
python3 - <<'PY' | kubectl apply -f -
import json, os
print(json.dumps({"apiVersion":"argoproj.io/v1alpha1","kind":"Application",
 "metadata":{"name":"flashkart-cloud","namespace":"argocd","finalizers":["resources-finalizer.argocd.argoproj.io"]},
 "spec":{"project":"default","source":{"repoURL":os.environ["GITOPS_REPO"],"targetRevision":os.environ["FLASHKART_GITOPS_COMMIT"],"path":os.environ["GITOPS_PATH"]},
 "destination":{"server":"https://kubernetes.default.svc","namespace":"flashkart"},
 "ignoreDifferences":[{"group":"apps","kind":"Deployment","name":name,
    "jsonPointers":["/spec/template/metadata/annotations/flashkart.io~1demo-tokens-version"]}
    for name in os.environ["FLASHKART_TOKEN_CONSUMERS"].split()],
 "syncPolicy":{"automated":{"prune":True,"selfHeal":True},
               "syncOptions":["ServerSideApply=true","RespectIgnoreDifferences=true"]}}}))
PY

app=application/flashkart-cloud
# Check the revision and sync status together. Separate waits can observe different reconciliations.
wait_for_application() {
    for _ in $(seq 1 240); do
        if kubectl -n argocd get "$app" -o json | jq -e \
            --arg revision "$revision" --arg repo "$GITOPS_REPO" --arg path "$GITOPS_PATH" --arg health "$1" '
            .status.sync as $sync |
            $sync.revision == $revision and $sync.status == "Synced" and
            $sync.comparedTo.source.targetRevision == $revision and
            $sync.comparedTo.source.repoURL == $repo and $sync.comparedTo.source.path == $path and
            ($health == "" or .status.health.status == $health)' >/dev/null; then
            return 0
        fi
        sleep 5
    done
    echo "Timed out waiting for $app at $revision ${1:+($1)}" >&2
    return 1
}
wait_for_application ""

# Keep a non-secret resource version on the pod template so retries also finish interrupted rollouts.
token_version=$(kubectl -n flashkart get secret demo-tokens -o jsonpath='{.metadata.resourceVersion}')
for deployment in $consumers; do
    kubectl -n flashkart patch deployment "$deployment" --type=merge \
        -p "{\"spec\":{\"template\":{\"metadata\":{\"annotations\":{\"flashkart.io/demo-tokens-version\":\"$token_version\"}}}}}"
done
for deployment in $consumers; do
    kubectl -n flashkart rollout status "deployment/$deployment" --timeout=10m
done
wait_for_application Healthy
printf 'Flashkart revision %s is healthy and token consumers are ready.\n' "$revision"
