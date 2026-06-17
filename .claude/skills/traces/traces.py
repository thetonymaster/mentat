#!/usr/bin/env python3
"""
Query OpenTelemetry traces from Mentat's Tempo.

Stdlib only. Defaults to the local dev Tempo (the deploy/ stack) at
http://localhost:3200. Built for Mentat's correlation model: a run is tagged with
test.run.id and may span several traces, so `run` merges them into one forest.

Usage:
    traces.py [--endpoint URL] [--limit N] [--service NAME] <subcommand> [args]

Subcommands:
    run <test.run.id>    all traces for a run, merged into one span forest
    tools <test.run.id>  tool-call sequence for a run (execute_tool spans)
    list                 5 most recent invoke_agent traces
    all                  10 most recent traces of any kind
    get <traceID>        span tree for one trace id
    errors               traces with status=error
    query <traceQL>      arbitrary TraceQL search

Env vars:
    TEMPO_ENDPOINT   Tempo base URL (overridden by --endpoint; default http://localhost:3200)
    TEMPO_USER       HTTP basic-auth username (optional, e.g. Grafana Cloud instance id)
    TEMPO_TOKEN      HTTP basic-auth password / token (optional)
"""

import base64
import json
import os
import sys
import urllib.parse
import urllib.request
import urllib.error
from datetime import datetime
from typing import Any


DEFAULT_ENDPOINT = "http://localhost:3200"

INTERESTING_ATTRS = [
    "gen_ai.operation.name", "gen_ai.agent.name", "gen_ai.request.model",
    "gen_ai.response.finish_reasons", "gen_ai.tool.name",
    "gen_ai.tool.call.arguments", "gen_ai.tool.call.result",
    "gen_ai.usage.input_tokens", "gen_ai.usage.output_tokens", "gen_ai.usage.cost_usd",
    "test.run.id", "test.scenario", "test.case",
]


# --- Endpoint / auth -----------------------------------------------------------

def resolve_endpoint(override: str | None) -> tuple[str, dict[str, str]]:
    base = (override or os.environ.get("TEMPO_ENDPOINT") or DEFAULT_ENDPOINT).rstrip("/")
    headers: dict[str, str] = {}
    user, token = os.environ.get("TEMPO_USER"), os.environ.get("TEMPO_TOKEN")
    if user and token:
        creds = base64.b64encode(f"{user}:{token}".encode()).decode()
        headers["Authorization"] = f"Basic {creds}"
    return base, headers


# --- HTTP ----------------------------------------------------------------------

def _http_get(url: str, headers: dict[str, str]) -> Any:
    req = urllib.request.Request(url, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=15) as r:
            body = r.read()
            try:
                return json.loads(body)
            except json.JSONDecodeError:
                preview = body[:200].decode("utf-8", errors="replace")
                sys.stderr.write(f"non-JSON response from {url}\n{preview}\n")
                sys.exit(1)
    except urllib.error.HTTPError as e:
        sys.stderr.write(f"HTTP {e.code} from {url}\n{e.read().decode(errors='replace')}\n")
        sys.exit(1)
    except urllib.error.URLError as e:
        sys.stderr.write(
            f"connection error to {url}: {e.reason}\n"
            "Is the dev stack up? Try: make harness-up\n"
        )
        sys.exit(2)


def search_traces(base: str, headers: dict[str, str], traceql: str, limit: int) -> list[dict]:
    q = urllib.parse.urlencode({"q": traceql, "limit": str(limit)})
    data = _http_get(f"{base}/api/search?{q}", headers)
    return data.get("traces") or []


def get_spans(base: str, headers: dict[str, str], trace_id: str) -> list[dict]:
    """Fetch one trace's spans, tolerating both OTLP-JSON shapes Tempo emits."""
    data = _http_get(f"{base}/api/traces/{urllib.parse.quote(trace_id)}", headers)
    spans: list[dict] = []
    # Newer Tempo: {"resourceSpans":[{"scopeSpans":[{"spans":[...]}]}]}
    # Older Tempo: {"batches":[{"scopeSpans":[{"spans":[...]}]}]}
    for batch in (data.get("resourceSpans") or data.get("batches") or []):
        res_attrs = (batch.get("resource") or {}).get("attributes", [])
        for ss in (batch.get("scopeSpans") or batch.get("instrumentationLibrarySpans") or []):
            for span in ss.get("spans", []):
                # carry resource attrs onto the span so test.run.id is visible in the tree
                span.setdefault("attributes", [])
                span["attributes"] = list(span["attributes"]) + list(res_attrs)
                spans.append(span)
    return spans


# --- Attribute / span helpers --------------------------------------------------

def _attr_val(v: dict) -> Any:
    if "stringValue" in v:
        return v["stringValue"]
    if "intValue" in v:
        return v["intValue"]
    if "boolValue" in v:
        return v["boolValue"]
    if "doubleValue" in v:
        return v["doubleValue"]
    if "arrayValue" in v:
        return [_attr_val(x) for x in v["arrayValue"].get("values", [])]
    return ""


def _attrs(span: dict) -> dict[str, Any]:
    return {a["key"]: _attr_val(a.get("value") or {}) for a in span.get("attributes", [])}


def _dur_ms(span: dict) -> float:
    start = int(span.get("startTimeUnixNano", "0") or "0")
    end = int(span.get("endTimeUnixNano", "0") or "0")
    return (end - start) / 1e6


def _decode_id(raw: str) -> str:
    """Tempo returns trace/span IDs as base64 in OTLP JSON; show hex."""
    if not raw or raw == "?":
        return raw or "?"
    try:
        decoded = base64.b64decode(raw, validate=True)
        if len(decoded) in (8, 16):
            return decoded.hex()
    except Exception:
        pass
    return raw


def _is_error(span: dict) -> bool:
    code = (span.get("status") or {}).get("code")
    return code in ("STATUS_CODE_ERROR", 2)


# --- Formatting ----------------------------------------------------------------

def fmt_list(traces: list[dict]) -> str:
    if not traces:
        return "No traces found."
    out = []
    for t in traces:
        tid = _decode_id(t.get("traceID", "?"))
        root = t.get("rootTraceName") or t.get("rootServiceName") or "?"
        dur = t.get("durationMs") or 0
        start_ns = int(t.get("startTimeUnixNano", "0") or "0")
        start = datetime.fromtimestamp(start_ns / 1e9).strftime("%H:%M:%S") if start_ns else "?"
        out.append(f"{tid} | {dur:>8}ms | {start} | {root}")
    return "\n".join(out)


def fmt_forest(spans: list[dict], title: str) -> str:
    if not spans:
        return "No spans found."
    span_map = {s["spanId"]: s for s in spans if s.get("spanId")}
    children: dict[Any, list[dict]] = {}
    for s in spans:
        children.setdefault(s.get("parentSpanId"), []).append(s)

    lines = [f"{title} ({len(spans)} spans)", ""]

    def emit(sid: str, depth: int) -> None:
        s = span_map[sid]
        a = _attrs(s)
        name = s.get("name", "?")
        kept = []
        for k in INTERESTING_ATTRS:
            if k in a:
                v = str(a[k])
                if len(v) > 60:
                    v = v[:60] + "..."
                kept.append(f"{k}={v}")
        err = " [ERROR]" if _is_error(s) else ""
        attr_str = (" | " + ", ".join(kept)) if kept else ""
        lines.append(f'{"  " * depth}+-  {name} ({_dur_ms(s):.1f}ms){err}{attr_str}')
        kids = sorted(children.get(sid, []), key=lambda x: int(x.get("startTimeUnixNano", "0") or "0"))
        for c in kids:
            if c.get("spanId") in span_map:
                emit(c["spanId"], depth + 1)

    roots = [s for s in spans if not s.get("parentSpanId") or s.get("parentSpanId") not in span_map]
    for r in sorted(roots, key=lambda x: int(x.get("startTimeUnixNano", "0") or "0")):
        if r.get("spanId"):
            emit(r["spanId"], 0)
    return "\n".join(lines)


def fmt_tools(spans: list[dict], run_id: str) -> str:
    tools = [s for s in spans if _attrs(s).get("gen_ai.operation.name") == "execute_tool"]
    tools.sort(key=lambda x: int(x.get("startTimeUnixNano", "0") or "0"))
    if not tools:
        return f"Run {run_id}: no execute_tool spans found."
    lines = [f"Run {run_id}: {len(tools)} tool call(s)", ""]
    for i, s in enumerate(tools, 1):
        a = _attrs(s)
        name = a.get("gen_ai.tool.name", s.get("name", "?"))
        lines.append(f"{i:>2}. {name}  ({_dur_ms(s):.1f}ms)")
    return "\n".join(lines)


# --- Run lookup (merge all traces tagged with the run id) ----------------------

def spans_for_run(base: str, headers: dict[str, str], run_id: str, limit: int) -> list[dict]:
    traceql = f'{{ .test.run.id = "{run_id}" }}'
    summaries = search_traces(base, headers, traceql, limit)
    merged: list[dict] = []
    for t in summaries:
        tid = t.get("traceID")
        if tid:
            merged.extend(get_spans(base, headers, tid))
    return merged


# --- CLI -----------------------------------------------------------------------

USAGE = __doc__.strip()


def parse_argv(argv: list[str]) -> tuple[str | None, int | None, str, list[str]]:
    endpoint: str | None = None
    limit: int | None = None
    args: list[str] = []
    i = 1
    while i < len(argv):
        a = argv[i]
        if a in ("-h", "--help"):
            print(USAGE)
            sys.exit(0)
        elif a in ("--endpoint", "--limit", "--service"):
            if i + 1 >= len(argv):
                sys.stderr.write(f"missing value for {a}\n")
                sys.exit(2)
            val = argv[i + 1]
            if a == "--endpoint":
                endpoint = val
            elif a == "--limit":
                try:
                    limit = int(val)
                except ValueError:
                    sys.stderr.write(f"invalid --limit value: {val}\n")
                    sys.exit(2)
            # --service is accepted for compatibility but unused (Mentat keys on test.run.id)
            i += 2
        else:
            args.append(a)
            i += 1
    if not args:
        print(USAGE)
        sys.exit(0)
    return endpoint, limit, args[0], args[1:]


def main(argv: list[str]) -> int:
    endpoint, limit, sub, rest = parse_argv(argv)
    base, headers = resolve_endpoint(endpoint)

    if sub == "get":
        if not rest:
            sys.stderr.write("usage: traces.py get <traceID>\n")
            return 2
        print(fmt_forest(get_spans(base, headers, rest[0]), f"Trace {rest[0]}"))
        return 0

    if sub == "run":
        if not rest:
            sys.stderr.write("usage: traces.py run <test.run.id>\n")
            return 2
        spans = spans_for_run(base, headers, rest[0], limit or 20)
        print(fmt_forest(spans, f"Run {rest[0]}"))
        return 0

    if sub == "tools":
        if not rest:
            sys.stderr.write("usage: traces.py tools <test.run.id>\n")
            return 2
        spans = spans_for_run(base, headers, rest[0], limit or 20)
        print(fmt_tools(spans, rest[0]))
        return 0

    builders = {
        "list": ('{ span.gen_ai.operation.name = "invoke_agent" }', 5),
        "all": ("{}", 10),
        "errors": ("{ status = error }", 10),
    }
    if sub in builders:
        traceql, default_limit = builders[sub]
    elif sub == "query":
        if not rest:
            sys.stderr.write("usage: traces.py query <TraceQL>\n")
            return 2
        traceql, default_limit = rest[0], 10
    else:
        sys.stderr.write(f"unknown subcommand: {sub}\n\n{USAGE}\n")
        return 2

    print(fmt_list(search_traces(base, headers, traceql, limit or default_limit)))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
