#!/usr/bin/env python3
"""Import/sync GUKO server inventory into Zeno Admin nodes.

The script is intentionally conservative:
- it never deletes Zeno nodes;
- it never calls install-command, so existing/new Agent credentials are not rotated;
- existing nodes are patched only for display metadata;
- writes require --apply (default is dry-run).
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import urllib.error
import urllib.request
from dataclasses import dataclass
from ipaddress import ip_address
from pathlib import Path
from typing import Any


@dataclass(frozen=True)
class DesiredNode:
    id: str
    display_name: str
    country_code: str
    display_order: int
    public_ipv4: str
    public_ipv6: str

    def create_payload(self) -> dict[str, Any]:
        payload: dict[str, Any] = {
            "id": self.id,
            "display_name": self.display_name,
            "display_order": self.display_order,
            "disabled": False,
        }
        if self.country_code:
            payload["country_code"] = self.country_code
        if self.public_ipv4:
            payload["public_ipv4"] = self.public_ipv4
        if self.public_ipv6:
            payload["public_ipv6"] = self.public_ipv6
        return payload

    def patch_payload(self, existing: dict[str, Any]) -> dict[str, Any]:
        payload: dict[str, Any] = {}
        comparable = {
            "display_name": self.display_name,
            "country_code": self.country_code,
            "display_order": self.display_order,
        }
        if self.public_ipv4:
            comparable["public_ipv4"] = self.public_ipv4
        if self.public_ipv6:
            comparable["public_ipv6"] = self.public_ipv6
        for key, value in comparable.items():
            if existing.get(key) != value:
                payload[key] = value
        return payload


def normalize_node_id(value: str) -> str:
    value = value.strip().lower()
    parts: list[str] = []
    last_dash = False
    for char in value:
        if "a" <= char <= "z" or "0" <= char <= "9":
            parts.append(char)
            last_dash = False
        elif char in "-_" or char.isspace():
            if parts and not last_dash:
                parts.append("-")
                last_dash = True
        if len(parts) >= 48:
            break
    return "".join(parts).strip("-") or "node"


def normalize_country(value: Any) -> str:
    country = str(value or "").strip().upper()
    if re.fullmatch(r"[A-Z]{2}", country):
        return country
    return ""


def normalize_ip(value: Any, family: int) -> str:
    raw = str(value or "").strip()
    if not raw:
        return ""
    try:
        parsed = ip_address(raw)
    except ValueError:
        return ""
    if parsed.version != family:
        return ""
    return str(parsed)


def load_guko_servers(path: Path) -> list[dict[str, Any]]:
    with path.open("r", encoding="utf-8") as fh:
        data = json.load(fh)
    if isinstance(data, list):
        return data
    if isinstance(data, dict) and isinstance(data.get("servers"), list):
        return data["servers"]
    raise ValueError("servers json must be a list or an object with a servers list")


def desired_nodes_from_guko(servers: list[dict[str, Any]]) -> list[DesiredNode]:
    nodes: list[DesiredNode] = []
    seen: set[str] = set()
    for index, server in enumerate(servers, start=1):
        name = str(server.get("name") or server.get("display_name") or "").strip()
        host = server.get("host") or server.get("ipv4") or server.get("ip")
        ipv4 = normalize_ip(host, 4)
        if not name or not ipv4:
            continue
        node_id = normalize_node_id(name)
        if node_id in seen:
            suffix = 2
            base = node_id
            while f"{base}-{suffix}" in seen:
                suffix += 1
            node_id = f"{base}-{suffix}"
        seen.add(node_id)
        nodes.append(
            DesiredNode(
                id=node_id,
                display_name=name,
                country_code=normalize_country(server.get("country") or server.get("country_code")),
                display_order=index * 10,
                public_ipv4=ipv4,
                public_ipv6=normalize_ip(server.get("ipv6"), 6),
            )
        )
    return nodes


class ZenoAdminClient:
    def __init__(self, base_url: str, admin_token: str, timeout: float = 10.0) -> None:
        self.base_url = base_url.rstrip("/")
        self.admin_token = admin_token.strip()
        self.timeout = timeout

    def request(self, method: str, path: str, payload: dict[str, Any] | None = None) -> tuple[int, dict[str, Any]]:
        data = None
        headers = {"X-Admin-Token": self.admin_token, "Accept": "application/json"}
        if payload is not None:
            data = json.dumps(payload, ensure_ascii=False).encode("utf-8")
            headers["Content-Type"] = "application/json"
        req = urllib.request.Request(self.base_url + path, data=data, headers=headers, method=method)
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                body = resp.read()
                return resp.status, json.loads(body.decode("utf-8") or "{}")
        except urllib.error.HTTPError as err:
            body = err.read().decode("utf-8", errors="replace")
            raise RuntimeError(f"{method} {path} returned {err.code}: {body}") from err

    def nodes(self) -> list[dict[str, Any]]:
        _, response = self.request("GET", "/api/admin/v1/nodes")
        nodes = response.get("nodes")
        if not isinstance(nodes, list):
            raise RuntimeError("admin nodes response missing nodes list")
        return nodes

    def create_node(self, payload: dict[str, Any]) -> None:
        self.request("POST", "/api/admin/v1/nodes", payload)

    def patch_node(self, node_id: str, payload: dict[str, Any]) -> None:
        self.request("PATCH", f"/api/admin/v1/nodes/{node_id}", payload)


def read_admin_token(args: argparse.Namespace) -> str:
    if args.admin_token:
        return args.admin_token.strip()
    if args.admin_token_file:
        return Path(args.admin_token_file).read_text(encoding="utf-8").strip()
    env_token = os.environ.get("ZENO_ADMIN_TOKEN", "").strip()
    if env_token:
        return env_token
    raise SystemExit("provide --admin-token-file, --admin-token, or ZENO_ADMIN_TOKEN")


def plan_changes(desired: list[DesiredNode], existing: list[dict[str, Any]]) -> tuple[list[DesiredNode], list[tuple[DesiredNode, dict[str, Any]]]]:
    existing_by_id = {str(node.get("id", "")): node for node in existing}
    creates: list[DesiredNode] = []
    patches: list[tuple[DesiredNode, dict[str, Any]]] = []
    for node in desired:
        current = existing_by_id.get(node.id)
        if current is None:
            creates.append(node)
            continue
        patch = node.patch_payload(current)
        if patch:
            patches.append((node, patch))
    return creates, patches


def main() -> int:
    parser = argparse.ArgumentParser(description="Sync GUKO server inventory into Zeno Admin nodes")
    parser.add_argument("--servers-json", required=True, help="Path to server-manager/servers.json")
    parser.add_argument("--controller-url", default="http://127.0.0.1:18980")
    parser.add_argument("--admin-token-file")
    parser.add_argument("--admin-token")
    parser.add_argument("--apply", action="store_true", help="Write changes. Default is dry-run.")
    args = parser.parse_args()

    desired = desired_nodes_from_guko(load_guko_servers(Path(args.servers_json)))
    if not desired:
        raise SystemExit("no importable GUKO servers found")
    token = read_admin_token(args)
    client = ZenoAdminClient(args.controller_url, token)
    existing = client.nodes()
    creates, patches = plan_changes(desired, existing)

    print(f"desired={len(desired)} existing={len(existing)} create={len(creates)} patch={len(patches)} mode={'apply' if args.apply else 'dry-run'}")
    for node in creates:
        print(f"CREATE {node.id} {node.display_name} {node.public_ipv4} {node.public_ipv6} {node.country_code} order={node.display_order}")
    for node, patch in patches:
        print(f"PATCH  {node.id} {json.dumps(patch, ensure_ascii=False, sort_keys=True)}")

    if not args.apply:
        return 0
    for node in creates:
        client.create_node(node.create_payload())
    for node, patch in patches:
        client.patch_node(node.id, patch)
    print("sync ok")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
