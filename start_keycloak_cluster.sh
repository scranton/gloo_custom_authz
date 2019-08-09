#!/usr/bin/env bash

# Expects
# brew install kubernetes-cli kubernetes-helm httpie

# Optional
# brew install go jq; brew cask install minikube

# Based on GlooE Custom Auth server example
# https://gloo.solo.io/enterprise/authentication/custom_auth/

K8S_TOOL=gcloud     # kind or minikube or gcloud
TILLER_MODE=cluster # local or cluster

DEMO_CLUSTER_NAME=demo-keycloak

# Will exit script if we would use an uninitialised variable:
set -o nounset
# Will exit script when a simple command (not a control structure) fails:
set -o errexit

function print_error {
  read -r line file <<<"$(caller)"
  echo "An error occurred in line $line of file $file:" >&2
  sed "${line}q;d" "$file" >&2
}
trap print_error ERR

K8S_TOOL="${K8S_TOOL:-minikube}" # kind or minikube

case "$K8S_TOOL" in
  kind)
    if [[ -x $(command -v go) ]] && [[ $(go version) =~ go1.12.[6-9] ]]; then
      # Install latest version of kind https://kind.sigs.k8s.io/
      GO111MODULE="on" go get sigs.k8s.io/kind@v0.4.0
    fi

    DEMO_CLUSTER_NAME="${DEMO_CLUSTER_NAME:-kind}"

    # Delete existing cluster, i.e. restart cluster
    if [[ $(kind get clusters) == *"$DEMO_CLUSTER_NAME"* ]]; then
      kind delete cluster --name "$DEMO_CLUSTER_NAME"
    fi

    # Setup local Kubernetes cluster using kind (Kubernetes IN Docker) with control plane and worker nodes
    kind create cluster --name "$DEMO_CLUSTER_NAME" --wait 60s

    # Configure environment for kubectl to connect to kind cluster
    KUBECONFIG=$(kind get kubeconfig-path --name="$DEMO_CLUSTER_NAME")
    export KUBECONFIG
    ;;

  minikube)
    DEMO_CLUSTER_NAME="${DEMO_CLUSTER_NAME:-minikube}"

    # for Mac (can also use Virtual Box)
    # brew install hyperkit; brew cask install minikube
    # minikube config set vm-driver hyperkit

    # minikube config set cpus 4
    # minikube config set memory 4096

    minikube delete --profile "$DEMO_CLUSTER_NAME" && true # Ignore errors
    minikube start --profile "$DEMO_CLUSTER_NAME"

    source <(minikube docker-env -p "$DEMO_CLUSTER_NAME")
    ;;

  gcloud)
    DEMO_CLUSTER_NAME="${DEMO_CLUSTER_NAME:-gke-scranton}"

    gcloud container clusters delete "$DEMO_CLUSTER_NAME" --quiet && true # Ignore errors
    gcloud container clusters create "$DEMO_CLUSTER_NAME" \
      --machine-type "n1-standard-1" \
      --num-nodes "3" \
      --labels=creator=scranton

    gcloud container clusters get-credentials "$DEMO_CLUSTER_NAME"

    kubectl create clusterrolebinding cluster-admin-binding \
      --clusterrole cluster-admin \
      --user "$(gcloud config get-value account)"
    ;;

esac

TILLER_MODE="${TILLER_MODE:-local}" # local or cluster

case "$TILLER_MODE" in
  local)
    # Run Tiller locally (external) to Kubernetes cluster as it's faster
    TILLER_PID_FILE=/tmp/tiller.pid
    if [[ -f $TILLER_PID_FILE ]]; then
      xargs kill <"$TILLER_PID_FILE" && true # Ignore errors killing old Tiller process
      rm "$TILLER_PID_FILE"
    fi
    TILLER_PORT=":44134"
    ( (tiller --storage=secret --listen=$TILLER_PORT) & echo $! > "$TILLER_PID_FILE" & )
    export HELM_HOST=$TILLER_PORT
    ;;

  cluster)
    unset HELM_HOST
    # Install Helm and Tiller
    kubectl --namespace kube-system create serviceaccount tiller

    kubectl create clusterrolebinding tiller-cluster-rule \
      --clusterrole=cluster-admin \
      --serviceaccount=kube-system:tiller

    helm init --service-account tiller

    # Wait for tiller to be fully running
    kubectl --namespace kube-system rollout status deployment/tiller-deploy --watch=true
    ;;

esac

kubectl create secret generic realm-secret --from-file=realm.json

helm repo add codecentric https://codecentric.github.io/helm-charts
helm upgrade --install keycloak codecentric/keycloak \
  --namespace default \
  --values - <<EOF
keycloak:
  extraVolumes: |
    - name: realm-secret
      secret:
        secretName: realm-secret

  extraVolumeMounts: |
    - name: realm-secret
      mountPath: "/realm/"
      readOnly: true

  extraArgs: -Dkeycloak.import=/realm/realm.json

  service:
    ## ServiceType
    ## ref: https://kubernetes.io/docs/user-guide/services/#publishing-services---service-types
    type: NodePort

  ## Ingress configuration.
  ## ref: https://kubernetes.io/docs/user-guide/ingress/
  ingress:
    enabled: true
    path: /*

    annotations:
      kubernetes.io/ingress.global-static-ip-name: "keycloak-sololabs"
      ingress.gcp.kubernetes.io/pre-shared-cert: "keycloak-sololabs-cert"
      # kubernetes.io/ingress.allow-http: "false"

    ## List of hosts for the ingress
    hosts:
      - "keycloak.sololabs.dev"
EOF

# KEYCLOAK_POD_NAME=$(kubectl --namespace default get pods -l app.kubernetes.io/instance=keycloak -o jsonpath="{.items[0].metadata.name}")

kubectl --namespace default rollout status statefulset/keycloak --watch=true

# ( kubectl --namespace default port-forward service/keycloak-http 9090:8080 >/dev/null )&
# echo "Visit http://127.0.0.1:9090 to use Keycloak"

echo "Keycloak password is "
kubectl --namespace default get secret keycloak-http -o jsonpath="{.data.password}" | base64 --decode; echo

# lookup_dns_ip() {
#   host "$1" | sed -rn 's@^.* has address @@p'
# }

# INGRESS_IP="$(kubectl get service/keycloak-http --output=jsonpath='{.status.loadBalancer.ingress[0].ip}')"
# ZONE="sololabs-dns"

# gcloud dns record-sets transaction start --zone=$ZONE
# gcloud dns record-sets transaction remove "$(lookup_dns_ip 'keycloak.sololabs.dev.')" --name=keycloak.sololabs.dev. --ttl=300 --type=A --zone=$ZONE
# gcloud dns record-sets transaction add "$INGRESS_IP" --name="keycloak.sololabs.dev." --ttl=300 --type=A --zone=$ZONE
# gcloud dns record-sets transaction execute --zone=$ZONE
