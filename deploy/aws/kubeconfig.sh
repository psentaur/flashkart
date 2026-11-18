cloud_kubeconfig() {
    export KUBECONFIG="${FLASHKART_KUBECONFIG:-$3}"
    if [ -z "${FLASHKART_KUBECONFIG:-}" ]; then
        aws eks update-kubeconfig --name "$1" --region "$2" >/dev/null
        return
    fi
    endpoint=$(aws eks describe-cluster --name "$1" --region "$2" --query cluster.endpoint --output text)
    current=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}')
    if [ -z "$endpoint" ] || [ "$endpoint" != "$current" ]; then
        echo "FLASHKART_KUBECONFIG must select the EKS cluster $1 in $2." >&2
        return 1
    fi
}
