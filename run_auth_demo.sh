#!/usr/bin/env bash

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

CLIENT_ID='test'
CLIENT_SECRET='bc375223-9270-44dc-901f-0bc1450e3a2e'

( cd auth_server_go_grpc; skaffold run )

# Patch Gloo Settings and default Virtual Service to reference custom auth service
kubectl --namespace gloo-system patch settings default \
  --type='merge' \
  --patch "$(cat<<EOF
spec:
  extensions:
    configs:
      extauth:
        extauthzServerRef:
          name: gloo-system-auth-server-8000
          namespace: gloo-system
        requestBody:
          maxRequestBytes: 10240
        requestTimeout: 1s
EOF
)"

if [[ ! -x "$(command -v glooctl)" ]] || [[ ! $(glooctl --version) == *"enterprise edition"* ]]; then
  echo "You must have glooctl enterprise installed and on your PATH"
  exit
fi

kubectl --namespace gloo-system delete secret/keycloak && true # Ignore errors from secret does not exist

glooctl create secret --namespace gloo-system \
  --name keycloak oauth \
  --client-secret "$CLIENT_SECRET"

# kubectl --namespace gloo-system delete virtualservice/default

# glooctl create virtualservice --namespace gloo-system \
#   --name default \
#   --enable-oidc-auth \
#   --oidc-auth-app-url 'http://localhost:8080/' \
#   --oidc-auth-callback-path '/callback' \
#   --oidc-auth-client-id "$CLIENT_ID" \
#   --oidc-auth-client-secret-name keycloak \
#   --oidc-auth-client-secret-namespace gloo-system \
#   --oidc-auth-issuer-url 'https://keycloak.sololabs.dev/auth/realms/k8s/'

# kubectl --namespace gloo-system get virtualservice/default --output yaml > oidc-vs.yaml

# Update Virtual Service to reference custom auth-server
kubectl --namespace gloo-system apply --filename - <<EOF
apiVersion: gateway.solo.io/v1
kind: VirtualService
metadata:
  name: default
  namespace: gloo-system
spec:
  virtualHost:
    domains:
    - '*'
    name: gloo-system.default
    routes:
    - matcher:
        prefix: /
      routeAction:
        single:
          upstream:
            name: default-echo-server-8080
            namespace: gloo-system
    virtualHostPlugins:
      extensions:
        configs:
          extauth:
            oauth:
              app_url: http://localhost:8080
              callback_path: /callback
              client_id: $CLIENT_ID
              client_secret_ref:
                name: keycloak
                namespace: gloo-system
              issuer_url: https://keycloak.sololabs.dev/auth/realms/k8s/
EOF

sleep 5
until [[ $(kubectl --namespace gloo-system get virtualservice default -o=jsonpath='{.status.state}') = "1" ]]; do
  sleep 5
done

sleep 10

PROXY_URL="http://localhost:8080"

# curl --silent --show-error ${PROXY_URL:-http://localhost:8080}/ | jq
http --json ${PROXY_URL:-http://localhost:8080}/
