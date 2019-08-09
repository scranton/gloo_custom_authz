# Solo.io Gloo Custom Authentication and Authorzation

## Keycloak

Keycloak is deployed on its own GKE cluster behind <https://keycloak.sololabs.dev/>

`start_keycloak_cluster.sh`

## Auth demo

```shell
start_gloo_cluster.sh
run_auth_demo.sh
```

<http://localhost:8080>

Username: `user1`
Password: `password`
