# Regix Helm chart

Installs a complete, private container registry:

- the **registry** ([CNCF Distribution](https://distribution.github.io/distribution/)) as a
  StatefulSet, with username/password authentication;
- its **volume** (a PersistentVolumeClaim) for the images;
- scheduled **garbage collection**, so deleted images actually free disk space;
- the **Regix web UI**, already connected to the registry;
- optionally an **Ingress** for each.

## Install

Build the Regix image and push it somewhere your cluster can pull from (see the
[project README](../../../README.md#install)). Then write a values file:

```yaml
# my-values.yaml
ui:
  image:
    repository: registry.example.com/tools/regix
    tag: "1.0.0"
  ingress:
    enabled: true
    className: traefik
    host: regix.example.com
    annotations:
      cert-manager.io/cluster-issuer: letsencrypt-prod
  protectedTags: [latest]

registry:
  persistence:
    size: 20Gi
  ingress:
    enabled: true
    className: traefik
    host: registry.example.com
    annotations:
      cert-manager.io/cluster-issuer: letsencrypt-prod
```

and install, giving the accounts on the command line so that passwords stay out of files:

```bash
helm install registry deploy/helm/regix \
  --namespace registry --create-namespace \
  -f my-values.yaml \
  --set registry.auth.users[0].username=ci \
  --set registry.auth.users[0].password="$CI_PASSWORD"
```

Then `docker login registry.example.com`, and open `https://regix.example.com` and sign in with the
same username and password.

To try the chart without an ingress or a pushed image, see [ci/test-values.yaml](ci/test-values.yaml)
and the port-forward command that `helm install` prints.

## Accounts

`registry.auth.users` is a list of usernames and passwords. The chart stores them bcrypt-hashed in
a Secret; the registry picks up changes without a restart. With the default `ui.authMode: registry`,
these are also the accounts that can sign in to the UI.

Helm keeps the values of a release, including these passwords, in a Secret in the release's
namespace. If you would rather manage the credentials yourself, create a Secret with a key
`htpasswd` and name it in `registry.auth.existingSecret`:

```bash
htpasswd -Bbn ci "$CI_PASSWORD" > htpasswd
kubectl -n registry create secret generic registry-users --from-file=htpasswd
```

## Storage and garbage collection

Images live on the volume claimed by the registry's StatefulSet (`registry.persistence`).
**Uninstalling the release keeps that volume**; delete the PersistentVolumeClaim yourself if you
want the images gone. Whether a claim can be grown later depends on your storage class.

Deleting an image, in the UI or through the API, removes the reference to it. The disk space is
freed by the registry's garbage collector, which must not run while images are being pushed. The
chart therefore runs it each time the registry pod starts, and a CronJob restarts the registry on
`registry.garbageCollection.schedule` (03:30 every night by default). Pushes and pulls fail for
the few seconds the restart takes; pick a quiet time. To free space immediately:

```bash
kubectl -n registry rollout restart statefulset/<release>-regix-registry
```

## Only the UI

For a registry that already exists, leave the registry out:

```yaml
registry:
  enabled: false
ui:
  registryUrl: http://registry.registry.svc:5000
  pullHost: registry.example.com
```

## Values

The settings you are most likely to change. [values.yaml](values.yaml) documents all of them.

| Value | Default | Meaning |
|---|---|---|
| `registry.enabled` | `true` | Deploy the registry. `false` deploys only the UI. |
| `registry.image.tag` | `3.1.2` | Registry version. |
| `registry.auth.users` | `[]` | Accounts: `[{username, password}]`. Required unless `existingSecret` is set or auth is disabled. |
| `registry.auth.existingSecret` | `""` | A Secret with an `htpasswd` key, instead of `users`. |
| `registry.persistence.size` | `10Gi` | Size of the image volume. |
| `registry.persistence.storageClass` | `""` | Storage class; empty uses the cluster's default. |
| `registry.deleteEnabled` | `true` | Allow deleting images. |
| `registry.garbageCollection.schedule` | `30 3 * * *` | When the registry restarts to free the space of deleted images. |
| `registry.ingress.host` | `""` | The registry's public host name. |
| `registry.configOverrides` | `{}` | Merged over the generated registry configuration. |
| `ui.image.repository` / `tag` | `regix` / chart `appVersion` | The Regix image. |
| `ui.authMode` | `registry` | `registry`, `static` or `none`. |
| `ui.readOnly` | `false` | Switch off deleting in the UI. |
| `ui.protectedTags` | `[]` | Tags the UI refuses to delete. |
| `ui.ingress.host` | `""` | The UI's host name. |

## Notes for particular setups

- **ingress-nginx** limits uploads to 1 MB by default, which breaks `docker push`. Add
  `nginx.ingress.kubernetes.io/proxy-body-size: "0"` to `registry.ingress.annotations`.
- **Object storage** (S3 and the like) instead of a volume: set the storage driver in
  `registry.configOverrides.storage`, pass keys through `registry.extraEnv`, and set
  `registry.persistence.enabled: false`.
- **Pulling the Regix image from this same registry** works once it has been pushed there, with
  `imagePullSecrets`; for the first install, load the image onto the node or use another registry.
