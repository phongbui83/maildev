# Shared pieces of the static guides (full edition and lite edition).
import html, datetime

import os
OUT = os.path.normpath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "public", "guide"))
TODAY = datetime.date.today().strftime("%Y-%m-%d")

CSS = """
:root{--bg:#f3f5f8;--panel:#fff;--border:#d9dfe7;--text:#1f2933;--muted:#5b6776;--accent:#0f6cbd;--soft:#e3effa;--ok:#107c41;--warn:#b45309;--danger:#c4314b}
@media (prefers-color-scheme:dark){:root{--bg:#0f1419;--panel:#171d26;--border:#2a3340;--text:#e6e9ee;--muted:#94a0b2;--accent:#60a5fa;--soft:#1e3a5f;--ok:#4ade80;--warn:#fbbf24;--danger:#f87171}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font:15px/1.6 ui-sans-serif,system-ui,"Segoe UI",Roboto,sans-serif}
header{position:sticky;top:0;z-index:10;background:var(--panel);border-bottom:1px solid var(--border);padding:10px 20px;display:flex;align-items:center;gap:14px;flex-wrap:wrap}
header .brand{font-weight:700;font-size:17px;display:flex;align-items:center;gap:8px;text-decoration:none;color:var(--text)}
header nav a{margin-right:10px;color:var(--muted);text-decoration:none;font-size:14px}header nav a:hover{color:var(--accent)}
.lang{margin-left:auto;display:flex;gap:6px}.lang a{padding:4px 10px;border:1px solid var(--border);border-radius:8px;text-decoration:none;color:var(--muted);font-size:13px}.lang a.on{background:var(--soft);color:var(--accent);font-weight:600}
.wrap{display:grid;grid-template-columns:260px 1fr;gap:28px;max-width:1280px;margin:0 auto;padding:24px 20px}
aside{position:sticky;top:64px;align-self:start;max-height:calc(100vh - 80px);overflow:auto;font-size:13.5px}
aside a{display:block;color:var(--muted);text-decoration:none;padding:3px 0}aside a.l2{padding-left:14px;font-size:13px}aside a:hover{color:var(--accent)}
main{min-width:0}
h1{font-size:30px;margin:0 0 6px}h2{font-size:22px;margin:40px 0 12px;padding-top:10px;border-top:1px solid var(--border)}h3{font-size:17px;margin:24px 0 8px}
p,li{color:var(--text)}.lead{font-size:17px;color:var(--muted)}
figure{margin:16px 0;background:var(--panel);border:1px solid var(--border);border-radius:12px;padding:10px}
figure img{width:100%;height:auto;border-radius:8px;display:block}figcaption{color:var(--muted);font-size:13px;margin-top:8px}
figcaption b{color:var(--danger)}
.note{border-left:4px solid var(--accent);background:var(--soft);padding:10px 14px;border-radius:0 10px 10px 0;margin:14px 0}
.warn{border-left-color:var(--warn);background:color-mix(in srgb,var(--warn) 12%,transparent)}
.danger{border-left-color:var(--danger);background:color-mix(in srgb,var(--danger) 12%,transparent)}
table{width:100%;border-collapse:collapse;font-size:14px;margin:12px 0;background:var(--panel)}th,td{border:1px solid var(--border);padding:7px 10px;text-align:left;vertical-align:top}th{background:var(--soft)}
code{background:var(--panel);border:1px solid var(--border);border-radius:5px;padding:1px 5px;font-size:13px}pre{background:var(--panel);border:1px solid var(--border);border-radius:10px;padding:12px;overflow:auto;font-size:12.5px}
.badge{display:inline-block;padding:1px 8px;border-radius:999px;border:1px solid var(--border);font-size:12px;color:var(--muted)}
.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(260px,1fr));gap:12px}.card{background:var(--panel);border:1px solid var(--border);border-radius:12px;padding:14px}
.card h4{margin:0 0 6px;font-size:15px}
footer{text-align:center;color:var(--muted);font-size:12px;padding:30px 20px;border-top:1px solid var(--border);margin-top:40px}
@media(max-width:900px){.wrap{grid-template-columns:1fr}aside{position:static;max-height:none}}
"""

LOGO = '<svg viewBox="0 0 64 64" width="26" height="26"><rect x="2" y="2" width="60" height="60" rx="14" fill="#0f6cbd"/><rect x="12" y="19" width="40" height="28" rx="5" fill="#fff"/><path d="M14 23 L32 37 L50 23" fill="none" stroke="#0b4f8f" stroke-width="3.2" stroke-linecap="round" stroke-linejoin="round"/><path d="M22 44 L17 50 L22 56 M42 44 L47 50 L42 56" fill="none" stroke="#9be7a4" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"/><circle cx="50" cy="17" r="6" fill="#34c759" stroke="#fff" stroke-width="2"/></svg>'

def esc(s): return html.escape(s, quote=False)

def fig(imgdir, name, caption):
    return f'<figure><img src="img/{imgdir}/{name}.png" alt="{esc(name)}" loading="lazy"><figcaption>{caption}</figcaption></figure>'

def render(loc, T, imgdir, langs, nav):
    """langs: [(code, file, label)], nav: [(href, label)]"""
    toc = ""
    body = ""
    for sec in T["sections"]:
        sid = sec["id"]
        toc += f'<a href="#{sid}">{esc(sec["title"])}</a>'
        body += f'<h2 id="{sid}">{esc(sec["title"])}</h2>'
        for block in sec["blocks"]:
            kind = block[0]
            if kind == "p": body += f"<p>{block[1]}</p>"
            elif kind == "lead": body += f'<p class="lead">{block[1]}</p>'
            elif kind == "h3":
                hid = block[2] if len(block) > 2 else ""
                body += f'<h3 id="{hid}">{esc(block[1])}</h3>'
                if hid: toc += f'<a class="l2" href="#{hid}">{esc(block[1])}</a>'
            elif kind == "ul": body += "<ul>" + "".join(f"<li>{x}</li>" for x in block[1]) + "</ul>"
            elif kind == "ol": body += "<ol>" + "".join(f"<li>{x}</li>" for x in block[1]) + "</ol>"
            elif kind == "note": body += f'<div class="note">{block[1]}</div>'
            elif kind == "warn": body += f'<div class="note warn">{block[1]}</div>'
            elif kind == "danger": body += f'<div class="note danger">{block[1]}</div>'
            elif kind == "fig": body += fig(imgdir, block[1], block[2])
            elif kind == "pre": body += f"<pre>{esc(block[1])}</pre>"
            elif kind == "table":
                hdr, rows = block[1], block[2]
                body += "<table><thead><tr>" + "".join(f"<th>{h}</th>" for h in hdr) + "</tr></thead><tbody>" + "".join("<tr>" + "".join(f"<td>{c}</td>" for c in r) + "</tr>" for r in rows) + "</tbody></table>"
            elif kind == "cards":
                body += '<div class="grid">' + "".join(f'<div class="card"><h4>{esc(h)}</h4><p>{t}</p></div>' for h, t in block[1]) + "</div>"
    langhtml = "".join(f'<a href="{f}" class="{"on" if l == loc else ""}">{n}</a>' for l, f, n in langs)
    navhtml = "".join(f'<a href="{h}">{esc(n)}</a>' for h, n in nav)
    return f"""<!doctype html><html lang="{loc}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{esc(T["title"])} · MailDev</title><link rel="icon" href="/icon.svg"><style>{CSS}</style></head><body>
<header><a class="brand" href="/">{LOGO} MailDev</a><nav>{navhtml}</nav><div class="lang">{langhtml}</div></header>
<div class="wrap"><aside>{toc}</aside><main>
<h1>{esc(T["title"])}</h1><p class="lead">{T["subtitle"]}</p><p class="badge">{esc(T["updated"])} {TODAY}</p>
{body}
</main></div>
<footer>{T["footer"]}</footer></body></html>"""
