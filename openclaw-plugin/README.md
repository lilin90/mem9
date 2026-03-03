# OpenClaw Plugin for mnemos

Memory plugin for [OpenClaw](https://github.com/openclaw) — replaces the built-in memory slot with cloud-persistent shared memory. Supports hybrid vector + keyword search, direct TiDB or server mode.

## How It Works

```
OpenClaw loads plugin as kind: "memory"
     ↓
Plugin replaces built-in memory slot → framework manages lifecycle
     ↓
5 tools registered: store / search / get / update / delete
```

This is a `kind: "memory"` plugin — OpenClaw's framework manages when to load/save memories. The plugin provides 5 tools that the agent (or framework) can invoke:

| Tool | Description |
|---|---|
| `memory_store` | Store a new memory (upsert by key) |
| `memory_search` | Hybrid vector + keyword search (or keyword-only) |
| `memory_get` | Retrieve a single memory by ID |
| `memory_update` | Update an existing memory |
| `memory_delete` | Delete a memory by ID |

## Prerequisites

- [OpenClaw](https://github.com/openclaw) installed (`>=2026.1.26`)
- **One** of the following backends:
  - A [TiDB Cloud Serverless](https://tidbcloud.com) cluster (free tier) — **Direct mode** (default, recommended)
  - A running [mnemo-server](../server/) instance — **Server mode** (for teams / multi-agent setups)

## Installation

### 1. Clone this repo

```bash
git clone https://github.com/qiffang/mnemos.git
cd mnemos/openclaw-plugin
```

### 2. Install dependencies

```bash
npm install
```

### 3. Configure OpenClaw

Add mnemo to your project's `openclaw.json`:

#### Option A: Direct Mode (default — TiDB Serverless)

Connect directly to TiDB Cloud. No server deployment needed.

```json
{
  "plugins": {
    "slots": {
      "memory": "mnemo"
    },
    "entries": {
      "mnemo": {
        "enabled": true,
        "config": {
          "host": "gateway01.us-east-1.prod.aws.tidbcloud.com",
          "username": "xxx.root",
          "password": "xxx",
          "database": "mnemos"
        }
      }
    }
  }
}
```

**Optional — enable hybrid vector search:**

```json
{
  "config": {
    "host": "...",
    "username": "...",
    "password": "...",
    "database": "mnemos",
    "embedding": {
      "apiKey": "sk-...",
      "model": "text-embedding-3-small",
      "dims": 1536
    }
  }
}
```

**Optional — use TiDB auto-embedding (no external API needed):**

```json
{
  "config": {
    "host": "...",
    "username": "...",
    "password": "...",
    "database": "mnemos",
    "autoEmbedModel": "tidbcloud_free/amazon/titan-embed-text-v2",
    "autoEmbedDims": 1024
  }
}
```

#### Option B: Server Mode (mnemo-server) — Recommended for Teams

OpenClaw is often deployed across teams with multiple agents. Server mode gives you:

- **Space isolation** — each team/project gets its own memory pool, no cross-contamination
- **Per-agent tokens** — every OpenClaw instance gets a unique API token scoped to its space
- **Centralized management** — one mnemo-server manages all memory, with rate limiting and auth
- **LLM conflict merge (Phase 2)** — when two agents write to the same key, the server can merge intelligently

**Step 1: Deploy mnemo-server**

```bash
cd mnemos/server
MNEMO_DSN="user:pass@tcp(tidb-host:4000)/mnemos?parseTime=true" go run ./cmd/mnemo-server
```

**Step 2: Create a shared space and get tokens**

```bash
# Create a space for your team (no auth required for bootstrap)
curl -s -X POST http://localhost:8080/api/spaces \
  -H "Content-Type: application/json" \
  -d '{"name": "backend-team", "agent_name": "alice-openclaw", "agent_type": "openclaw"}'

# Response:
# {"ok": true, "space_id": "...", "api_token": "mnemo_abc123"}

# Add another agent to the same space
curl -s -X POST http://localhost:8080/api/spaces/<space_id>/tokens \
  -H "Authorization: Bearer mnemo_abc123" \
  -H "Content-Type: application/json" \
  -d '{"agent_name": "bob-openclaw", "agent_type": "openclaw"}'

# Response:
# {"ok": true, "api_token": "mnemo_def456"}
```

**Step 3: Configure each OpenClaw instance**

Each agent uses its own token, but they share the same memory pool:

```json
{
  "plugins": {
    "slots": {
      "memory": "mnemo"
    },
    "entries": {
      "mnemo": {
        "enabled": true,
        "config": {
          "apiUrl": "http://your-server:8080",
          "apiToken": "mnemo_abc123"
        }
      }
    }
  }
}
```

That's it. All agents in the same space read and write to the same memory pool. The server handles auth, scoping, and conflict resolution.

The plugin auto-detects the mode:
- `host` present → **Direct mode**
- `apiUrl` present → **Server mode**

### 4. Verify

Start OpenClaw. You should see one of these log lines:

```
[mnemo] Direct mode (keyword-only)
[mnemo] Direct mode (hybrid search)
[mnemo] Direct mode (auto-embedding: tidbcloud_free/amazon/titan-embed-text-v2)
[mnemo] Server mode
```

If you see `[mnemo] No mode configured...`, check your `openclaw.json` config.

## Config Schema

Defined in `openclaw.plugin.json`:

| Field | Type | Mode | Description |
|---|---|---|---|
| `host` | string | Direct | TiDB Serverless host |
| `username` | string | Direct | TiDB username |
| `password` | string | Direct | TiDB password |
| `database` | string | Direct | Database name (default: `mnemos`) |
| `autoEmbedModel` | string | Direct | TiDB auto-embedding model (takes priority over client-side embedding) |
| `autoEmbedDims` | number | Direct | Auto-embedding vector dimensions (default: 1024) |
| `apiUrl` | string | Server | mnemo-server URL |
| `apiToken` | string | Server | API token |
| `embedding.apiKey` | string | Direct | OpenAI key or `'local'` for Ollama |
| `embedding.baseUrl` | string | Direct | Custom endpoint (e.g. `http://localhost:11434/v1`) |
| `embedding.model` | string | Direct | Model name (default: `text-embedding-3-small`) |
| `embedding.dims` | number | Direct | Vector dimensions (default: 1536) |

## File Structure

```
openclaw-plugin/
├── README.md              # This file
├── openclaw.plugin.json   # Plugin metadata + config schema
├── package.json           # npm package config
├── index.ts               # Plugin entry point + tool registration
├── backend.ts             # MemoryBackend interface
├── direct-backend.ts      # Direct mode: @tidbcloud/serverless
├── server-backend.ts      # Server mode: fetch → mnemo API
├── embedder.ts            # OpenAI-compatible embedding provider
├── schema.ts              # Auto schema init with VECTOR column
└── types.ts               # Shared types (PluginConfig, Memory, etc.)
```

## Troubleshooting

| Problem | Cause | Fix |
|---|---|---|
| `No mode configured` | Missing config | Add `host` (direct) or `apiUrl` (server) to plugin config |
| `Direct mode requires...` | Missing credentials | Add `username` and `password` to config |
| `Server mode requires...` | Missing token | Add `apiToken` to config |
| Plugin not loading | Not in memory slot | Set `"slots": {"memory": "mnemo"}` in openclaw.json |
| Keyword-only search | No embedding config | Add `embedding` config or use `autoEmbedModel` |
