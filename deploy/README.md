# Deploying the full edition to Kubernetes

The root [README](../README.md) covers the one-host `docker compose` setup. This directory holds an example Kustomize deployment of the full edition; adapt the host names, registry and ConfigMap to your environment.

```
k8s/            namespace, ConfigMap, API deployment (1 replica), web deployment, Services, Gateway API HTTPRoute,
                cert-manager Certificate, MetalLB LoadBalancer for the mail ports, optional in-cluster PostgreSQL
k8s/make-secrets.sh   creates the `maildev-secrets` (database URL, master key) and `ad-ca` (pinned LDAPS certificate) secrets
openbao/setup.sh      enables Transit, creates the policy and AppRole used for at-rest encryption, stores them as a secret
Makefile              build/push the two images and apply the manifests with a tag
```

## Steps

1. Edit `k8s/configmap.yaml` (host names, LDAP, directory URL, S/MIME CA URLs, OpenBao address), `k8s/httproute.yaml` and `k8s/certificate.yaml` (host names), `k8s/kustomization.yaml` and `Makefile` (image registry).
2. Secrets:

   ```bash
   DATABASE_URL='postgres://maildev:<password>@db.example.com:5432/maildev?sslmode=require' \
   LDAP_HOST=dc01.example.com bash k8s/make-secrets.sh        # generates MAILDEV_MASTER_KEY, pins the LDAPS certificate
   BAO_ADDR=https://vault.example.com BAO_TOKEN=... bash openbao/setup.sh   # optional: at-rest encryption
   ```

3. Build, push and deploy:

   ```bash
   make push deploy TAG=$(date +%Y%m%d%H%M)
   ```

4. DNS: point the web host name at the gateway and the mail host name at the LoadBalancer IP of `svc/maildev-mail` (ports 25, 587, 465, 143, 993, 110, 995). The containers listen on 1025/1587/1465/1143/1993/1110/1995; `MAILDEV_PUBLIC_*_PORTS` in the ConfigMap tell the UI which public ports to show.

The API keeps the event bus in memory (SSE and IMAP IDLE), so run exactly one API replica. The web deployment is stateless and can scale.

## Make the catcher the only way out

The catcher only helps when applications actually use it. Remove the real SMTP credentials from dev/test configuration, block outbound 25/465/587 from the dev network, and hand every team its project credentials from the *Development info* page.
