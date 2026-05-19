#!/usr/bin/env python3
"""Render a visual HTML report from fair-suite benchmark artifacts."""

from __future__ import annotations

import argparse
import math
from pathlib import Path
from statistics import median
from typing import Dict, List, Tuple

LANG_ORDER = ["c", "cpp", "rust", "go", "lumen"]
LANG_COLORS = {
    "c": "#2563eb",
    "cpp": "#0ea5e9",
    "rust": "#f59e0b",
    "go": "#22c55e",
    "lumen": "#ef4444",
}


def read_floats(path: Path) -> List[float]:
    if not path.exists():
        return []
    vals: List[float] = []
    for raw in path.read_text().splitlines():
        s = raw.strip()
        if not s:
            continue
        try:
            vals.append(float(s))
        except ValueError:
            continue
    return vals


def mean(xs: List[float]) -> float:
    return sum(xs) / len(xs) if xs else 0.0


def cv_pct(xs: List[float]) -> float:
    if len(xs) <= 1:
        return 0.0
    m = mean(xs)
    if m == 0:
        return 0.0
    var = sum((x - m) ** 2 for x in xs) / len(xs)
    return math.sqrt(var) * 100.0 / m


def parse_http(root: Path) -> Dict[str, Dict[str, List[float]]]:
    out: Dict[str, Dict[str, List[float]]] = {}
    http_root = root / "http"
    if not http_root.exists():
        return out
    for case_dir in sorted(p for p in http_root.iterdir() if p.is_dir() and "_pipe" in p.name):
        case = case_dir.name
        out[case] = {}
        for lang in LANG_ORDER:
            vals = read_floats(case_dir / f"{lang}.rps")
            if vals:
                out[case][lang] = vals
    return out


def parse_time_workload(root: Path, workload: str) -> Dict[str, List[float]]:
    wdir = root / workload
    out: Dict[str, List[float]] = {}
    if not wdir.exists():
        return out
    for lang in LANG_ORDER:
        vals = read_floats(wdir / f"{lang}.s")
        if vals:
            out[lang] = vals
    return out


def score_rankings(http_cases, json_vals, fib_vals, sort_vals) -> List[Tuple[str, float]]:
    scores: Dict[str, List[float]] = {k: [] for k in LANG_ORDER}

    for case in http_cases.values():
        med = {lang: median(vals) for lang, vals in case.items() if vals}
        if not med:
            continue
        best = max(med.values())
        for lang, v in med.items():
            scores[lang].append(v / best)

    for workload in (json_vals, fib_vals, sort_vals):
        med = {lang: median(vals) for lang, vals in workload.items() if vals}
        if not med:
            continue
        best = min(med.values())
        for lang, v in med.items():
            if v > 0:
                scores[lang].append(best / v)

    ranked: List[Tuple[str, float]] = []
    for lang in LANG_ORDER:
        vals = scores[lang]
        if vals:
            ranked.append((lang, mean(vals)))
    ranked.sort(key=lambda t: t[1], reverse=True)
    return ranked


def bar_row(label: str, value: float, max_value: float, unit: str, color: str) -> str:
    width = 0.0 if max_value <= 0 else 100.0 * value / max_value
    return (
        f'<div class="row">'
        f'<div class="name">{label}</div>'
        f'<div class="bar-wrap"><div class="bar" style="width:{width:.2f}%;background:{color}"></div></div>'
        f'<div class="val">{value:,.2f}{unit}</div>'
        f"</div>"
    )


def render(root: Path) -> str:
    http_cases = parse_http(root)
    json_vals = parse_time_workload(root, "json")
    fib_vals = parse_time_workload(root, "fib")
    sort_vals = parse_time_workload(root, "sort")
    rankings = score_rankings(http_cases, json_vals, fib_vals, sort_vals)

    parts: List[str] = []
    parts.append(
        """
<!doctype html>
<html>
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width,initial-scale=1" />
  <title>Fair Suite Benchmark Report</title>
  <style>
    :root { --bg:#0b1020; --panel:#121a33; --text:#e8ecff; --muted:#98a2c8; --line:#23305c; }
    body { margin:0; font-family: ui-sans-serif, -apple-system, Segoe UI, Helvetica, Arial, sans-serif; background:radial-gradient(circle at 20% -10%, #1f2a56 0%, var(--bg) 45%); color:var(--text); }
    .wrap { max-width: 1100px; margin: 24px auto; padding: 0 16px 40px; }
    h1,h2,h3 { margin: 0 0 12px; }
    h1 { font-size: 28px; }
    .sub { color: var(--muted); margin-bottom: 24px; }
    .grid { display:grid; grid-template-columns:1fr; gap:16px; }
    .card { background: linear-gradient(180deg, #151f3d, var(--panel)); border:1px solid var(--line); border-radius:12px; padding:14px; }
    .row { display:grid; grid-template-columns:120px 1fr 120px; gap:10px; align-items:center; margin:8px 0; }
    .name { color:#d7deff; font-weight:600; text-transform:uppercase; font-size:12px; letter-spacing:.04em; }
    .bar-wrap { background:#0c1531; border:1px solid #22305a; border-radius:8px; height:18px; overflow:hidden; }
    .bar { height:100%; }
    .val { text-align:right; color:#d7deff; font-variant-numeric: tabular-nums; }
    table { width:100%; border-collapse: collapse; }
    th, td { text-align:left; padding:8px 6px; border-bottom:1px solid #23305c; }
    th { color:#aeb8e4; font-size:12px; text-transform:uppercase; letter-spacing:.04em; }
    td { font-variant-numeric: tabular-nums; }
  </style>
</head>
<body>
  <div class="wrap">
    <h1>Fair Suite Performance Report</h1>
    <div class="sub">Source: benchmarks/results/fair_suite (auto-generated)</div>
"""
    )

    parts.append('<div class="grid">')

    parts.append('<div class="card"><h2>Overall Ranking (Normalized Composite)</h2>')
    if rankings:
        max_score = max(s for _, s in rankings)
        for lang, score in rankings:
            parts.append(bar_row(lang, score * 100.0, max_score * 100.0, "", LANG_COLORS.get(lang, "#888")))
    else:
        parts.append("<div>No ranking data found.</div>")
    parts.append("</div>")

    parts.append('<div class="card"><h2>HTTP Throughput</h2>')
    if http_cases:
        for case_name, langs in sorted(http_cases.items()):
            med = {lang: median(vals) for lang, vals in langs.items() if vals}
            if not med:
                continue
            parts.append(f"<h3>{case_name}</h3>")
            m = max(med.values())
            for lang in LANG_ORDER:
                if lang in med:
                    parts.append(bar_row(lang, med[lang], m, " rps", LANG_COLORS.get(lang, "#888")))
            parts.append("<hr style='border-color:#23305c;border-width:1px 0 0 0'>")
    else:
        parts.append("<div>No HTTP data found.</div>")
    parts.append("</div>")

    for title, data in (("JSON Encode (lower is better)", json_vals), ("Math Fib (lower is better)", fib_vals), ("Sort Ints (lower is better)", sort_vals)):
        parts.append(f'<div class="card"><h2>{title}</h2>')
        if data:
            med = {lang: median(vals) for lang, vals in data.items() if vals}
            m = max(med.values())
            for lang in LANG_ORDER:
                if lang in med:
                    parts.append(bar_row(lang, med[lang], m, " s", LANG_COLORS.get(lang, "#888")))

            parts.append("<table><thead><tr><th>Lang</th><th>Median s</th><th>Mean s</th><th>CV%</th><th>N</th></tr></thead><tbody>")
            for lang in LANG_ORDER:
                vals = data.get(lang)
                if not vals:
                    continue
                parts.append(
                    f"<tr><td>{lang}</td><td>{median(vals):.4f}</td><td>{mean(vals):.4f}</td><td>{cv_pct(vals):.2f}</td><td>{len(vals)}</td></tr>"
                )
            parts.append("</tbody></table>")
        else:
            parts.append("<div>No data found.</div>")
        parts.append("</div>")

    parts.append("</div></div></body></html>")
    return "\n".join(parts)


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--input", default="benchmarks/results/fair_suite")
    ap.add_argument("--output", default="benchmarks/results/fair_suite/report.html")
    args = ap.parse_args()

    root = Path(args.input)
    out = Path(args.output)
    out.parent.mkdir(parents=True, exist_ok=True)
    html = render(root)
    out.write_text(html)
    print(f"wrote {out}")


if __name__ == "__main__":
    main()
