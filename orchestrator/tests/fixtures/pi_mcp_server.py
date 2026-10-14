"""Minimal stdio MCP server for SDK lifecycle/permissions/evidence tests."""

import json
import sys
import time

TOOLS = [
    "list_targets",
    "find_function",
    "get_callees",
    "get_callers",
    "slice_from",
    "paths_between",
]
for line in sys.stdin:
    request = json.loads(line)
    if "id" not in request:
        continue
    method = request.get("method")
    if method == "initialize":
        result = {
            "protocolVersion": "2024-11-05",
            "capabilities": {"tools": {}},
            "serverInfo": {"name": "defuzz-fixture", "version": "1.0"},
        }
    elif method == "tools/list":
        result = {
            "tools": []
            if "--empty" in sys.argv
            else [
                {
                    "name": name,
                    "description": "Fixture query",
                    "inputSchema": {
                        "type": "object",
                        "properties": {},
                    },
                }
                for name in TOOLS
            ]
        }
    elif method == "tools/call":
        if "--slow" in sys.argv:
            time.sleep(10)
        sys.stderr.write("mcp-fixture-stderr\n" * 5000)
        sys.stderr.flush()
        result = {"content": [{"type": "text", "text": "mcp-original-" + "界" * 30000}]}
    else:
        result = {}
    print(json.dumps({"jsonrpc": "2.0", "id": request["id"], "result": result}), flush=True)
