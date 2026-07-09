# Perkeep Integration

membox can back its notes up to a Perkeep server. The local filesystem
remains the primary working copy; Perkeep acts as immutable, content-addressed
remote durable storage.

## How It Works

- Each markdown note is content-hashed with SHA-256.
- `mm perkeep push` uploads changed notes to Perkeep.
- Perkeep stores the file as a blob and creates a **permanode** for it.
- The note UUID is recorded both as a Perkeep attribute (`membox-uuid`) and in
the local `perkeep_sync` table, so the two systems stay linked.
- `mmd` (membox daemon) runs the same push loop in the background.

## Perkeep Principles Alignment

| Principle | Implementation |
|-----------|---------------|
| **Content-addressability** | Notes are synced based on SHA-256 content hashes. Identical content never uploads twice. |
| **User owns data** | Local markdown files are the source of truth; Perkeep is an independent copy. |
| **Decentralization** | The local copy works offline; sync happens only when the tunnel is up. |
| **Redundancy** | Both local files and the remote Perkeep server hold the data. |
| **Disk is cheap; don't delete** | `mmd` creates new Perkeep blobs for edits. It does not currently issue Perkeep delete claims, so old versions remain on the server. |

## Setup

1. Ensure Perkeep client config is initialized:

```bash
pk-put init --gpgkey <YOUR_KEY_ID>
```

2. Use `127.0.0.1` in `~/.config/perkeep/client-config.json` (not `localhost`) so the SSH tunnel matches IPv4:

```json
{
  "servers": {
    "localhost": {
      "server": "http://127.0.0.1:3179",
      "auth": "localhost",
      "default": true
    }
  },
  "identity": "..."
}
```

3. (Optional) Let `mmd` manage the SSH tunnel automatically by setting environment variables:

```bash
export MM_TUNNEL_HOST=38.207.176.66
export MM_TUNNEL_USER=edward
export MM_TUNNEL_PORT=43568
# export MM_TUNNEL_KEY=/path/to/private/key
```

If `MM_TUNNEL_HOST` is set, `mmd` will run `ssh -N -L 3179:127.0.0.1:3179 ...` for you, restart it when it drops, and only sync when the tunnel is healthy.

4. Initialize membox and add a workspace directory:

```bash
MM_DEV=1 mm init
mm workspace add /path/to/notes
```

## Commands

```bash
# Push changed notes to Perkeep now
mm perkeep push

# Show notes already synced to Perkeep
mm perkeep status

# Start the background daemon. If MM_TUNNEL_HOST is set, it also manages the tunnel.
mmd

# Change the sync interval
MM_SYNC_INTERVAL=30s mmd
```

## Limitations

- Only **push** is implemented. Pull/download from Perkeep into the local cache is future work.
- membox `note delete` removes the local file but does not currently issue a Perkeep delete claim; the old version remains on the server.
- Conflicts are not resolved; the local version wins on the next push.
