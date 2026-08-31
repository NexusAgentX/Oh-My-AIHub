# Oh-My-AIHub

Oh-My-AIHub is a small full-stack foundation for an AI hub:

- React + TypeScript frontend powered by Vite
- Go HTTP API
- Docker Compose deployment
- Node.js and Go versions managed by mise

## Prerequisites

- [mise](https://mise.jdx.dev/)
- Docker with Compose

## Local development

```bash
mise install
mise run install
mise run dev-backend
```

In another terminal:

```bash
mise run dev-frontend
```

The Vite development server runs at <http://localhost:5173> and proxies `/api`
requests to the Go server at <http://localhost:8080>.

## Docker deployment

```bash
mise run up
```

Open <http://localhost:3000>. Stop the stack with `mise run down`.

## Verification

```bash
mise run test
docker compose config --quiet
```
