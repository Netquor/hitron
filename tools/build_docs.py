#!/usr/bin/env python3
"""Render the Markdown guides into static HTML under site/docs/.

The site itself is build-free; this script only turns docs/*.md and
cli/README.md into pages that share the site's theme. CI runs it before
publishing (see .github/workflows/pages.yml), and the output is committed so
`hitron site` and a plain checkout serve the same pages.

    pip install markdown
    python3 tools/build_docs.py
"""
from __future__ import annotations

import html
import posixpath
import re
import sys
from pathlib import Path

import markdown

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "site" / "docs"
REPO = "https://github.com/Netquor/hitron"
SITE = "https://hitron.netquor.dev"

# (source, page slug, nav title, one-line summary)
PAGES = [
    ("docs/GETTING-STARTED.md", "getting-started", "Getting started", "Install, verify, export, scrape, alert, and troubleshoot, in order."),
    ("cli/README.md", "hitron-cli", "hitron-cli", "The terminal dashboard: install, keys, themes, and every command."),
    ("docs/GRAFANA.md", "grafana", "Grafana", "Import or provision dashboards, pick panels, write alerts, ship logs to Loki."),
    ("docs/LOCAL-PROXY.md", "local-proxy", "Local proxy", "Run the API console against your own modem with one command."),
    ("docs/KB.md", "kb", "Knowledge base", "Auth model, firmware quirks, shell sinks, and open questions."),
    ("docs/VERIFICATION.md", "verification", "Verification", "What was measured on a real CODA-60, and how."),
]
BY_SOURCE = {src: slug for src, slug, _, _ in PAGES}


def slugify(value: str, separator: str = "-") -> str:
    """GitHub-style heading ids, so anchors in the Markdown keep working."""
    value = re.sub(r"[^\w\s-]", "", value.lower().strip())
    return re.sub(r"\s", separator, value)


def rewrite_link(url: str, src_dir: str) -> str:
    if re.match(r"^([a-z][a-z0-9+.-]*:|#|//)", url, re.I):
        return url
    path, _, frag = url.partition("#")
    if not path:
        return url
    target = posixpath.normpath(posixpath.join(src_dir, path))
    frag = "#" + frag if frag else ""
    if target in BY_SOURCE:
        return BY_SOURCE[target] + ".html" + frag
    if target.startswith("site/"):
        return "../" + target[len("site/"):] + frag
    kind = "tree" if not posixpath.splitext(target)[1] else "blob"
    return f"{REPO}/{kind}/main/{target}{frag}"


def rewrite_html(body: str, src_dir: str) -> str:
    def attr(match: re.Match) -> str:
        name, url = match.group(1), html.unescape(match.group(2))
        return f'{name}="{html.escape(rewrite_link(url, src_dir), quote=True)}"'

    body = re.sub(r'\b(href|src)="([^"]*)"', attr, body)
    # External links open normally; mark them so CSS can add the arrow.
    body = re.sub(r'<a href="(https?://[^"]+)"', r'<a class="ext" href="\1"', body)
    body = re.sub(r"<table>", '<div class="table-scroll"><table>', body)
    body = body.replace("</table>", "</table></div>")
    return body


TEMPLATE = """<!doctype html>
<html lang="en" data-theme="dark">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{title} · hitron docs</title>
<meta name="description" content="{description}">
<meta name="theme-color" content="#04050b">
<link rel="canonical" href="{site}/docs/{slug}.html">
<link rel="icon" href="data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 32 32'><defs><linearGradient id='g' x1='0' y1='0' x2='1' y2='1'><stop offset='0' stop-color='%235eead4'/><stop offset='1' stop-color='%23a78bfa'/></linearGradient></defs><rect width='32' height='32' rx='9' fill='%2304050b'/><circle cx='16' cy='16' r='6.5' fill='none' stroke='url(%23g)' stroke-width='3'/></svg>">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600&amp;family=JetBrains+Mono:wght@400;500;600&amp;family=Space+Grotesk:wght@500;600;700&amp;display=swap">
<link rel="stylesheet" href="../assets/style.css">
<link rel="stylesheet" href="../assets/glass.css">
<link rel="stylesheet" href="../assets/docs.css">
<script>try{{var t=localStorage.getItem('hitron.theme');if(t)document.documentElement.dataset.theme=t;else if(matchMedia('(prefers-color-scheme: light)').matches)document.documentElement.dataset.theme='light'}}catch(e){{}}</script>
<script src="../assets/fx.js" defer></script>
<script src="../assets/docs.js" defer></script>
</head>
<body>
<a class="skip" href="#main">Skip to content</a>
<header class="topbar">
  <a class="brand" href="../index.html#top">
    <span class="brand-mark" aria-hidden="true"><svg viewBox="0 0 16 16" width="14" height="14"><circle cx="8" cy="8" r="5.5" fill="none" stroke="currentColor" stroke-width="2.2"/></svg></span>
    <span>hitron<span class="dim">/docs</span></span>
  </a>
  <nav class="topnav">
    <a href="index.html" class="active">Guides</a>
    <a href="../index.html#cli">CLI</a>
    <a href="../index.html#explorer">API console</a>
    <a href="../index.html#dashboards">Grafana</a>
    <a href="../openapi.json" class="nav-secondary">OpenAPI</a>
    <a href="../llms.txt" class="nav-secondary">llms.txt</a>
  </nav>
  <div class="topbar-actions">
    <button id="theme-toggle" class="icon-btn" type="button" title="Toggle theme" aria-label="Toggle theme">
      <svg class="icon-sun" viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="4.2"/><path d="M12 2.5v2.2M12 19.3v2.2M2.5 12h2.2M19.3 12h2.2M5.2 5.2l1.6 1.6M17.2 17.2l1.6 1.6M18.8 5.2l-1.6 1.6M6.8 17.2l-1.6 1.6"/></svg>
      <svg class="icon-moon" viewBox="0 0 24 24" aria-hidden="true"><path d="M20 14.4A8.6 8.6 0 0 1 9.6 4 8.6 8.6 0 1 0 20 14.4Z"/></svg>
    </button>
  </div>
</header>
<div class="docs-shell">
  <aside class="docs-side" aria-label="Documentation">
    <nav class="docs-nav">
      <p class="docs-nav-label">Guides</p>
{nav}
    </nav>
{toc}
  </aside>
  <main id="main" class="docs-main">
{content}
{pager}
    <p class="docs-edit"><a class="ext" href="{repo}/blob/main/{source}">Edit this page on GitHub</a></p>
  </main>
</div>
<footer class="footer">
  <p>Not official Hitron documentation. Field names come from the device; mistakes are ours. <a href="../llms.txt">llms.txt</a> · <a href="../openapi.json">OpenAPI</a> · <a href="{repo}">Source</a></p>
</footer>
</body>
</html>
"""

INDEX_BODY = """<article class="doc">
<p class="eyebrow">Documentation</p>
<h1>Guides</h1>
<p class="doc-lede">Everything that does not fit on the front page: install paths, the Grafana tour, the reverse-engineering notes, and what was verified on real hardware.</p>
<div class="cards three doc-cards">
{cards}
</div>
</article>"""


def render(md_text: str, src_dir: str) -> tuple[str, str, str, list[tuple[str, str, int]]]:
    md = markdown.Markdown(
        extensions=["fenced_code", "tables", "toc", "sane_lists", "attr_list"],
        extension_configs={"toc": {"slugify": slugify, "permalink": "#", "permalink_title": "Link to this section", "toc_depth": "2-3"}},
        output_format="html",
    )
    body = md.convert(md_text)
    body = re.sub(r'(<h1[^>]*>.*?)<a class="headerlink"[^>]*>.*?</a>(</h1>)', r"\1\2", body, flags=re.S)
    title_match = re.search(r"<h1[^>]*>(.*?)</h1>", body, re.S)
    title = html.unescape(re.sub(r"<[^>]+>", "", title_match.group(1))).strip() if title_match else "Documentation"
    first_p = re.search(r"<p>(.*?)</p>", body, re.S)
    summary = html.unescape(re.sub(r"<[^>]+>", "", first_p.group(1))).strip() if first_p else title
    toc_items = []
    for m in re.finditer(r'<h([23]) id="([^"]+)">(.*?)</h\1>', body, re.S):
        text = re.sub(r'<a class="headerlink".*?</a>', "", m.group(3), flags=re.S)
        text = html.unescape(re.sub(r"<[^>]+>", "", text)).strip()
        toc_items.append((m.group(2), text, int(m.group(1))))
    return rewrite_html(body, src_dir), title, summary, toc_items


def build() -> int:
    OUT.mkdir(parents=True, exist_ok=True)
    def nav_for(current: str) -> str:
        rows = []
        for _, slug, t, _ in PAGES:
            mark = ' aria-current="page"' if slug == current else ""
            rows.append(f'      <a href="{slug}.html"{mark}>{html.escape(t)}</a>')
        return "\n".join(rows)
    written = []
    for i, (source, slug, nav_title, blurb) in enumerate(PAGES):
        text = (ROOT / source).read_text(encoding="utf-8")
        body, title, summary, toc_items = render(text, posixpath.dirname(source))
        toc = ""
        if len(toc_items) > 2:
            links = "\n".join(
                f'        <a href="#{html.escape(i_)}" class="toc-l{lvl}">{html.escape(t)}</a>' for i_, t, lvl in toc_items
            )
            toc = f'    <nav class="docs-toc" aria-label="On this page">\n      <p class="docs-nav-label">On this page</p>\n{links}\n    </nav>'
        prev_ = PAGES[i - 1] if i > 0 else None
        next_ = PAGES[i + 1] if i < len(PAGES) - 1 else None
        pager = '    <nav class="docs-pager" aria-label="Pages">\n'
        pager += (f'      <a class="prev" href="{prev_[1]}.html"><span>Previous</span>{html.escape(prev_[2])}</a>\n' if prev_ else "      <span></span>\n")
        pager += (f'      <a class="next" href="{next_[1]}.html"><span>Next</span>{html.escape(next_[2])}</a>\n' if next_ else "      <span></span>\n")
        pager += "    </nav>"
        description = summary[:200].rstrip()
        page = TEMPLATE.format(
            title=html.escape(title), description=html.escape(description, quote=True), slug=slug, site=SITE, repo=REPO,
            nav=nav_for(slug), toc=toc, content='<article class="doc">\n' + body + "\n</article>", pager=pager, source=source,
        )
        (OUT / f"{slug}.html").write_text(page, encoding="utf-8")
        written.append(slug)

    cards = "\n".join(
        f'  <a class="card link" href="{slug}.html"><h3>{html.escape(t)}</h3><p>{html.escape(b)}</p></a>' for _, slug, t, b in PAGES
    )
    index = TEMPLATE.format(
        title="Guides", description="Install guides, the Grafana tour, reverse-engineering notes, and verification results for hitron.",
        slug="index", site=SITE, repo=REPO, nav=nav_for(""), toc="", content=INDEX_BODY.format(cards=cards), pager="", source="docs",
    ).replace('Edit this page on GitHub', 'Browse the docs folder on GitHub').replace(f'{REPO}/blob/main/docs', f'{REPO}/tree/main/docs')
    (OUT / "index.html").write_text(index, encoding="utf-8")

    urls = [f"{SITE}/", f"{SITE}/docs/"] + [f"{SITE}/docs/{s}.html" for s in written] + [f"{SITE}/llms.txt", f"{SITE}/openapi.json"]
    sitemap = '<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n'
    sitemap += "".join(f"  <url><loc>{u}</loc></url>\n" for u in urls) + "</urlset>\n"
    (ROOT / "site" / "sitemap.xml").write_text(sitemap, encoding="utf-8")
    print(f"wrote {len(written) + 1} pages to {OUT.relative_to(ROOT)}")
    return 0


if __name__ == "__main__":
    sys.exit(build())
