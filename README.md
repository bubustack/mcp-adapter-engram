# 🧩 MCP Adapter Engram

Bridge bobrapet Stories to [Model Context Protocol](https://modelcontextprotocol.io/) (MCP) servers. The MCP Adapter Engram hides JSON-RPC wiring, transport fan-out, and Kubernetes plumbing so your workflows can invoke external MCP providers or in-cluster MCP services with a simple `with:` block.

## 🌟 Highlights

- **Two transport modes** – Connect to remote MCP servers over Streamable HTTP/S, or spin up one-off stdio Pods with automatic cleanup.
- **Protocol fidelity** – Raw MCP responses flow back under `output.data.result` without reshaping, preserving the MCP schema.
- **Secure secret handling** – The stdio runner receives only the `server` secret bucket; optionally materialized as a short-lived Secret with `stdio.useEphemeralSecret`.
- **Cleanup aware** – Owner references and a two-hour reaper prevent orphan runner Pods.
- **Enriched observability** – SDK patches include manifest snapshots (object counts, sizes) and latch onto bobrapet metrics/logging.

```
┌──────────────┐     ┌─────────────────────────┐      ┌──────────────────┐
│ Story (step) │───▶ │ MCP Adapter Engram Pod  │────▶ │ MCP transport     │
└──────────────┘     │  - loads StepRun env    │      │  • HTTP endpoint  │
                     │  - establishes session  │      │  • stdio runner   │
                     │  - forwards JSON-RPC    │◀─────│  • SSE stream     │
                     └─────────────────────────┘      └──────────────────┘
```

## 🚀 Quick Start

```bash
make lint
go test ./...
make docker-build
```

Apply `Engram.yaml` and point your Story step at the `mcp-adapter` template. Example (`stdio` transport):

```yaml
apiVersion: bubustack.io/v1alpha1
kind: Engram
metadata:
  name: mcp-github-batch
spec:
  templateRef:
    name: mcp-adapter
  mode: job
  with:
    transport: "stdio"
    stdio:
      image: ghcr.io/github/mcp-server:latest
      deletionPolicy: DeleteOnFinish
    mcp:
      initClientCapabilities: {}
  secrets:
    server: github-mcp-secrets
```

## 🗂️ MCP registries and directories

- GitHub MCP registry: https://github.com/modelcontextprotocol/registry
- Cursor MCP directory: https://cursor.directory/mcp
- Docker Hub (search for MCP servers): https://hub.docker.com/search?q=mcp
- https://mcpservers.org/remote-mcp-servers
= https://mcp.so/
- https://mcpmarket.com/
- https://mcpservers.org/
- GHCR listing for this engram: https://ghcr.io/bubustack/mcp-adapter-engram

## ⚙️ Configuration (`Engram.spec.with`)

| Field | Type | Description | Default |
| --- | --- | --- | --- |
| `transport` | `string` | `streamable_http` or `stdio`. | _required_ |
| `server.baseURL` | `string` | Base URL for HTTP transport. | _required when `transport=streamable_http`_ |
| `server.headers` | `map[string]string` | Static headers applied to every request. | `{}` |
| `server.headersFromSecret` | `map[string]string` | Secret key → header name mapping. | `{}` |
| `stdio.image` | `string` | Runner image hosting the MCP server. | required for `stdio` |
| `stdio.command` / `args` | `[]string` | Override entrypoint/args. | image defaults |
| `stdio.resources` | `WorkloadResources` | CPU/memory requests & limits. | `{}` |
| `stdio.security` | `WorkloadSecurity` | Security context options. | `{}` |
| `stdio.nodeSelector`, `tolerations` | Kubernetes placement controls. | `{}` / `[]` |
| `stdio.deletionPolicy` | `string` | `DeleteOnFinish` or `Keep`. | `DeleteOnFinish` |
| `stdio.useEphemeralSecret` | `bool` | For stdio transport, copy `server` secret values into a per-run Secret mounted via `envFrom`. | `false` |
| `stdio.terminationGracePeriodSeconds` | `int64` | Override Pod termination grace period. | inherits bobrapet default |
| `mcp.initClientCapabilities` | `map[string]any` | Capabilities passed during MCP `initialize`. | `{}` |

## 🔐 Secrets

| Logical Secret | Usage | Notes |
| --- | --- | --- |
| `server` | Injects credentials/headers into HTTP requests or stdio environment. | Mounts as env vars; combine with `server.headersFromSecret` for header wiring or TLS material. |

## 📥 Inputs (batch & streaming)

The Engram expects the following inputs, decoded into the `Inputs` struct:

| Field | Type | Description |
| --- | --- | --- |
| `action` | `string` | MCP action (`listTools`, `callTool`, `listResources`, `readResource`, `getPrompt`, or `reconcile`). |
| `tool` | `string` | Tool name for `callTool`. |
| `arguments` | `map[string]any` | Tool arguments (JSON-serialisable). |
| `resourceURI` | `string` | Resource identifier for `readResource`. |
| `promptName` | `string` | Prompt identifier for `getPrompt`. |
| `reconcile` | `bool` | Reserved; no-op placeholder. |
| `timeout` | `duration` | Optional per-call timeout. |
| `server` | `string` | Reserved; ignored in v1 (single server). |

## 📤 Outputs

Batch mode returns `map[string]any{"data": {...}}` where:

| Field | Description |
| --- | --- |
| `data.result` | Raw MCP response (e.g., `listTools` result array). |
| `data.error` | Error envelope when the adapter or MCP server reports a failure. |
| `data.meta.durationMs` | Execution time (milliseconds). |
| `data.meta.server` | Resolved server endpoint or runner Pod name. |
| `data.meta.tool` | Tool invoked during the request (when applicable). |

Streaming mode emits each response as a `StreamMessage` with the JSON response
in `Payload` and the same bytes mirrored into `Binary` with
`MimeType: application/json`.

## 🔄 Transport Details

- **`streamable_http`**
  - Minimal footprint—no extra Pods.
  - Adds `Mcp-Session-Id` header automatically and merges `server.headers` + secrets.
  - Supports long-lived sessions using JSON polling or SSE.
- **`stdio`**
  - Spins up a dedicated runner Pod per Engram instance.
  - Uses SPDY `pods/attach` to stream MCP JSON-RPC over stdin/stdout.
  - `stdio.useEphemeralSecret=true` creates a short-lived per-run Secret for `server` credentials.

## 🧪 Local Development

- `make lint` – Validate code with golangci-lint.
- `go test ./...` – Run unit tests (no cluster required).
- `make run` – Start the adapter locally (requires bobrapet env vars + kubeconfig).
- `make docker-build` – Multi-arch image build via `docker buildx`.

## 🤝 Community & Support

- [Contributing](./CONTRIBUTING.md)
- [Support](./SUPPORT.md)
- [Security Policy](./SECURITY.md)
- [Code of Conduct](./CODE_OF_CONDUCT.md)
- [Discord](https://discord.gg/dysrB7D8H6)


## 📄 License

Copyright 2025 BubuStack.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
