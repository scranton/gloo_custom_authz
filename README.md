# Solo.io Gloo Custom Authentication and Authorization

## Auth demo

```shell
start_gloo_cluster.sh
run_auth_demo.sh
```

<http://localhost:8080>

### Test User Credentials

* Username: `user1`
* Password: `password`

### Protected Resources

* <http://localhost:8080/> - Approved
* <http://localhost:8080/foo> - Denied
* <http://localhost:8080/bar> - Approved

## Keycloak

Keycloak is deployed on its own GKE cluster behind <https://keycloak.sololabs.dev/>

```shell
cd keycloak
./start_keycloak_cluster.sh
```
