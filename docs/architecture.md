# Architecture (v0.1)

Goal: treat Neovim as a replaceable **UI client**.

## Layers

- Domain: `membox/domain/*`
- Application (use-cases): `membox/application/*`
- Infra: `membox/infra/*`
- Protocol contract: `membox/protocol/*`
- CLI adapter: `membox/apps/memboxd.py`

## UI <-> Core boundary

v0.1 uses "spawn per request":

- UI calls: `python -m membox.apps.memboxd --stdin`
- stdin: `{"line":"task ls"}`
- stdout: JSON ResultEnvelope with a `table` view.

Future versions can upgrade to a long-running daemon without breaking the envelope.
