#!/bin/sh
# Fills the demo registry with a few public images, so there is something to look at.
set -eu

copy() {
  echo "-> $2"
  skopeo copy --quiet --all --dest-tls-verify=false --dest-creds demo:demo-password "docker://$1" "docker://registry:5000/$2"
}

copy docker.io/library/alpine:3.20 base/alpine:3.20
copy docker.io/library/alpine:3.21 base/alpine:3.21
copy docker.io/library/alpine:3.21 base/alpine:latest
copy docker.io/library/busybox:1.36 base/busybox:1.36
copy docker.io/library/busybox:1.37 base/busybox:1.37
copy docker.io/library/nginx:1.27-alpine web/nginx:1.27
copy docker.io/library/nginx:1.28-alpine web/nginx:1.28
copy docker.io/library/nginx:1.28-alpine web/nginx:stable
copy docker.io/library/redis:7-alpine data/redis:7
copy docker.io/library/redis:8-alpine data/redis:8
echo "Done. Open http://localhost:8080 and sign in as demo / demo-password."
