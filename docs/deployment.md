# Deployment

## Overview

This project uses:

- `GitHub Actions` for `CI`, image release, and production deployment workflows
- a regional OCI registry as the production image source, with `Tencent TCR` as the default target
- `SSH + Docker Compose` for remote deployment

The remote server only runs:

- `web`
- `server`

Database lifecycle is intentionally outside this deployment flow.

## Required GitHub Secrets

Configure these repository secrets before running the release or deploy workflows:

- `REGISTRY_HOST`
- `REGISTRY_NAMESPACE`
- `REGISTRY_USERNAME`
- `REGISTRY_PASSWORD`
- `DEPLOY_HOST`
- `DEPLOY_PORT`
- `DEPLOY_USER`
- `DEPLOY_SSH_KEY`

`REGISTRY_*` are used both for pushing release images and for authenticating the production pull step on the server.

## Server Preparation

Run these commands on the remote Linux server once:

```bash
sudo mkdir -p /opt/agent-project
sudo chown -R <deploy-user>:<deploy-user> /opt/agent-project
```

Copy the compose template and create the runtime env file:

```bash
cp deploy/docker-compose.prod.yml /opt/agent-project/docker-compose.prod.yml
cat >/opt/agent-project/.env <<'EOF'
IMAGE_REGISTRY=ccr.ccs.tencentyun.com
IMAGE_NAMESPACE=docreview-agent-prod
IMAGE_TAG=v0.1.0
SERVER_PORT=8080
WEB_PORT=3000
DATABASE_URL=<replace-with-real-production-database-url>
NEXT_PUBLIC_API_BASE_URL=http://106.52.42.194:8080
EOF
```

Before the first real production deployment, manually confirm that `DATABASE_URL` is a real value rather than a placeholder.

## CI Workflow

File:

- `.github/workflows/ci.yml`

Triggers:

- `pull_request`
- `push` to `main`

Checks:

- `go test ./apps/server/...`
- `npm ci && npm run build` in `apps/web`
- local Docker build smoke checks for `server` and `web`

## Release Images Workflow

File:

- `.github/workflows/release-images.yml`

Triggers:

- `push` tags matching `v*`

Behavior:

- tag push builds and pushes fresh images to the configured regional registry
- this workflow does not touch the remote server

## Deploy Production Workflow

File:

- `.github/workflows/deploy-production.yml`

Triggers:

- manual `workflow_dispatch` with `image_tag`
- optional `dry_run`

Behavior:

- syncs the compose file and deploy script to `/opt/agent-project`
- writes a short-lived registry credential file on the server
- updates only `IMAGE_TAG`
- runs `docker compose pull`
- runs `docker compose up -d`
- verifies the server health check from the production host itself

This separation exists because the old `GHCR -> runner -> SSH stream -> docker load` path proved too slow and unstable for production use.

## Publishing a New Version

Create and push a release tag:

```bash
git tag v0.1.0
git push origin v0.1.0
```

Expected result:

- `Release Images` pushes `docreview-agent-server:v0.1.0`
- `Release Images` pushes `docreview-agent-web:v0.1.0`
- no production containers are changed yet

Then trigger `Deploy Production` manually with `image_tag=v0.1.0`.

## Rolling Back

Use the `Deploy Production` workflow manually:

1. Open the workflow in GitHub Actions.
2. Choose `Run workflow`.
3. Enter an existing image tag such as `v0.1.0`.
4. Execute the workflow.

The workflow skips image builds and only redeploys the existing image tag from the regional registry.

## Runtime Verification

After deployment, verify:

```bash
curl http://<server-ip>:8080/healthz
```

Then open:

- `http://<server-ip>:3000`
- `http://<server-ip>:3000/resources`

## Notes

- This first version does not manage DNS, HTTPS, or reverse proxy setup.
- Keep stable runtime values in `/opt/agent-project/.env`.
- Let deployments only change `IMAGE_TAG`.
- Do not treat `DATABASE_URL` in example files as production-ready.
