from __future__ import annotations

import argparse
import json
import sys

from cli.membox_cli.api_client import APIClient


def _build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(prog="mm", description="membox CLI")
    p.add_argument("--hub", default=None, help="Hub base URL (default: env MEMBOX_HUB_URL)")
    sub = p.add_subparsers(dest="cmd", required=True)

    # mm new
    pn = sub.add_parser("new", help="Create a new note")
    pn.add_argument("title", nargs="?", default="", help="Note title")
    pn.add_argument("--backend", default="work", help="Backend name")
    pn.add_argument("--subdir", default="inbox", help="Subdirectory")
    pn.add_argument("--source-url", default=None, help="Source URL")
    pn.add_argument("--tag", action="append", default=[], dest="tags", help="Tags")
    pn.add_argument("--content", default=None, help="Note content (reads from stdin if omitted)")

    # mm search
    ps = sub.add_parser("search", help="Search indexed content")
    ps.add_argument("query", help="Search query")
    ps.add_argument("-n", "--topk", type=int, default=10, help="Top K results")
    ps.add_argument("--doc", default=None, help="Restrict to doc")
    ps.add_argument("--path-prefix", default=None, help="Path prefix filter")
    ps.add_argument("--format", choices=["text", "json"], default="text")

    return p


def _read_content(arg_content: str | None) -> str:
    if arg_content is not None:
        return arg_content
    if not sys.stdin.isatty():
        return sys.stdin.read()
    return ""


def _cmd_new(args: argparse.Namespace, client: APIClient) -> int:
    content = _read_content(args.content)
    if not content:
        print("Error: note content is empty. Use --content or pipe stdin.", file=sys.stderr)
        return 1

    payload = {
        "type": "note",
        "backend": args.backend,
        "subdir": args.subdir,
        "source_url": args.source_url,
        "title": args.title or "Untitled",
        "tags": args.tags,
        "content": content,
    }

    out = client.ingest_note(payload)
    print(json.dumps(out, ensure_ascii=False, indent=2))
    return 0


def _cmd_search(args: argparse.Namespace, client: APIClient) -> int:
    payload = {
        "query": args.query,
        "topk": args.topk,
        "doc": args.doc,
        "path_prefix": args.path_prefix,
    }
    out = client.search(payload)

    if args.format == "json":
        print(json.dumps(out, ensure_ascii=False, indent=2))
        return 0

    hits = out.get("hits", [])
    if not hits:
        print("No results.")
        return 0

    for i, r in enumerate(hits, 1):
        page = ""
        if r.get("page_start") is not None:
            if r.get("page_end") and r["page_end"] != r["page_start"]:
                page = f"  p.{r['page_start']}-{r['page_end']}"
            else:
                page = f"  p.{r['page_start']}"
        print(f"{i:>2}. score={r.get('score', 0):.3f}  {r.get('source_path', '')}{page}")
        print(f"    {r.get('snippet', '')}")
    return 0


def main(argv: list[str] | None = None) -> int:
    args = _build_parser().parse_args(argv)
    client = APIClient(base_url=args.hub)

    if args.cmd == "new":
        return _cmd_new(args, client)
    elif args.cmd == "search":
        return _cmd_search(args, client)
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
