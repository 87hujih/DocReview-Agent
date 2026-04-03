# Deployment

## Overview

This project uses:

- `GitHub Actions` for CI and release workflows
- `GHCR` for container image storage
- `SSH + Docker Compose` for remote deployment

The remote server only runs:

- `web`
- `server`

Database lifecycle is intentionally outside this deployment flow.

## Required GitHub Secrets

Configure these repository secrets before running the release workflow:

- `DEPLOY_HOST`
- `DEPLOY_PORT`
- `DEPLOY_USER`
- `DEPLOY_SSH_KEY`

`GHCR` publish and pull inside the workflow use:

- `${{ github.actor }}`
- `${{ secrets.GITHUB_TOKEN }}`

so you do not need to add separate `GHCR_USERNAME` or `GHCR_TOKEN` secrets for this first version.

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
GHCR_OWNER=87hujih
IMAGE_TAG=v0.1.0
SERVER_PORT=8080
WEB_PORT=3000
DATABASE_URL=postgres://postgres:postgres@db.example.internal:5432/agent_project?sslmode=disable
NEXT_PUBLIC_API_BASE_URL=http://106.52.42.194:8080
EOF
```

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

## Release Workflow

File:

- `.github/workflows/release-deploy.yml`

Triggers:

- `push` tags matching `v*`
- manual `workflow_dispatch` with `image_tag`

Behavior:

- tag push builds and pushes fresh images to `GHCR`
- manual dispatch deploys an existing image tag for rollback or replay
- deploy job pulls the tagged images on the GitHub runner and streams them over `SSH` into `docker load` on the server

## Publishing a New Version

Create and push a release tag:

```bash
git tag v0.1.0
git push origin v0.1.0
```

Expected result:

- GitHub Actions builds `docreview-agent-server:v0.1.0`
- GitHub Actions builds `docreview-agent-web:v0.1.0`
- the deploy job updates `/opt/agent-project/.env`
- the deploy job streams both images into the remote Docker daemon
- the remote server runs `docker compose up -d`

## Rolling Back

Use the `Release Deploy` workflow manually:

1. Open the workflow in GitHub Actions.
2. Choose `Run workflow`.
3. Enter an existing image tag such as `v0.1.0`.
4. Execute the workflow.

The workflow skips image builds and only redeploys the existing GHCR images.

This rollback path still uses the GitHub runner as the component that pulls the images from `GHCR`. The server only receives the image stream through `SSH` and does not need to pull from `GHCR` itself.

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
