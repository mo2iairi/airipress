"""Deterministic offline tools used by the Go service and CI."""
from __future__ import annotations
import hashlib, html, json, os, re, shutil, subprocess, tempfile
from pathlib import Path
from typing import Any

IMAGE_EXT = {".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg"}

def _safe(root: Path, relative: str) -> Path:
    root = root.resolve(); p = (root / relative).resolve()
    if p != root and root not in p.parents: raise ValueError(f"path escapes workspace: {relative}")
    return p

def _slug(value: str) -> str:
    value = re.sub(r"[^\w\- ]+", "", value, flags=re.UNICODE).strip().lower()
    return re.sub(r"[\s_]+", "-", value) or "untitled"

def export_site(workspace: str, output: str, title: str = "airipress", engine: str = "astro", theme_path: str = "", theme_id: str = "") -> dict[str, Any]:
    root, dest = Path(workspace), Path(output)
    if not root.is_dir(): raise ValueError("workspace must be a directory")
    if engine not in {"astro", "hugo"}: raise ValueError("unsupported site engine")
    theme = Path(theme_path) if theme_path else None
    if theme and (not theme.is_dir() or not (theme / "airipress.theme.json").is_file()): raise ValueError("installed theme contract is missing")
    dest.mkdir(parents=True, exist_ok=True); posts=[]; copied=0
    markdown_root = root / "markdown"
    sources = sorted(markdown_root.rglob("*.md")) if markdown_root.is_dir() else sorted(root.rglob("*.md"))
    for src in sources:
        rel=src.relative_to(markdown_root if markdown_root.is_dir() else root); slug=_slug(str(rel.with_suffix("")))
        if engine == "astro":
            target=dest / "src/pages" / f"{slug}.md"; target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text("---\ntitle: " + json.dumps(src.stem, ensure_ascii=False) + "\nlayout: ../layouts/Base.astro\n---\n\n" + src.read_text(encoding="utf-8"), encoding="utf-8")
        else:
            target=dest / "content" / f"{slug}.md"; target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text("---\ntitle: " + json.dumps(src.stem, ensure_ascii=False) + "\n---\n\n" + src.read_text(encoding="utf-8"), encoding="utf-8")
        posts.append({"source": str(rel), "slug": slug, "title": src.stem})
    image_root = dest / "public/assets"
    destination_root = dest.resolve()
    for src in sorted(root.rglob("*")):
        # Do not re-ingest an output directory located inside the workspace.
        if src.is_file() and src.suffix.lower() in IMAGE_EXT and ".build" not in src.parts and destination_root not in src.resolve().parents:
            target=_safe(image_root, str(src.relative_to(root))); target.parent.mkdir(parents=True, exist_ok=True); shutil.copy2(src, target); copied += 1
    links="".join(f"<li><a href='/{p['slug']}'>{html.escape(p['title'])}</a></li>" for p in posts)
    if engine == "astro":
        (dest / "src/pages").mkdir(parents=True, exist_ok=True)
        (dest / "src/pages/index.astro").write_text(f"---\nimport Base from '../layouts/Base.astro';\n---\n<Base title={json.dumps(title, ensure_ascii=False)}><h1>{html.escape(title)}</h1><ul>{links}</ul></Base>\n", encoding="utf-8")
        (dest / "package.json").write_text(json.dumps({"name":"airipress-site","private":True,"type":"module","scripts":{"build":"astro build","dev":"astro dev"},"dependencies":{"astro":"latest"}},indent=2), encoding="utf-8")
        (dest / "astro.config.mjs").write_text("import { defineConfig } from 'astro/config';\nexport default defineConfig({ output: 'static', site: undefined });\n", encoding="utf-8")
        (dest / "src/layouts").mkdir(parents=True, exist_ok=True)
        (dest / "src/layouts/Base.astro").write_text("---\nconst { title = 'airipress' } = Astro.props;\n---\n<!doctype html><html lang='zh-CN'><head><meta charset='utf-8'><meta name='viewport' content='width=device-width'><title>{title}</title><link rel='stylesheet' href='/style.css'></head><body><header><a href='/'>airipress</a></header><main><slot /></main></body></html>\n", encoding="utf-8")
        if theme:
            layout=theme / "src/layouts/Base.astro"
            if not layout.is_file(): raise ValueError("Astro theme must provide src/layouts/Base.astro")
            shutil.copy2(layout, dest / "src/layouts/Base.astro")
    else:
        (dest / "layouts/_default").mkdir(parents=True, exist_ok=True)
        (dest / "layouts/index.html").write_text("{{ define \"main\" }}<h1>{{ .Site.Title }}</h1><ul>{{ range .Site.RegularPages }}<li><a href='{{ .RelPermalink }}'>{{ .Title }}</a></li>{{ end }}</ul>{{ end }}", encoding="utf-8")
        (dest / "layouts/_default/baseof.html").write_text("<!doctype html><html><head><meta charset='utf-8'><meta name='viewport' content='width=device-width'><title>{{ .Title }}</title><link rel='stylesheet' href='/style.css'></head><body><header><a href='{{ .Site.Home.RelPermalink }}'>{{ .Site.Title }}</a></header><main>{{ block \"main\" . }}{{ .Content }}{{ end }}</main></body></html>", encoding="utf-8")
        config="baseURL = '/'\nlanguageCode = 'zh-CN'\ntitle = " + json.dumps(title, ensure_ascii=False) + "\n"
        if theme:
            target=dest / "themes" / (theme_id or "airipress-theme"); target.parent.mkdir(parents=True, exist_ok=True); shutil.copytree(theme, target, symlinks=False)
            config += "theme = " + json.dumps(theme_id or "airipress-theme") + "\n"
        (dest / "hugo.toml").write_text(config, encoding="utf-8")
    (dest / "manifest.json").write_text(json.dumps({"title":title,"engine":engine,"theme":theme_id,"posts":posts,"images":copied},ensure_ascii=False,indent=2), encoding="utf-8")
    (dest / "public").mkdir(parents=True, exist_ok=True)
    (dest / "public/style.css").write_text("body{font-family:system-ui,sans-serif;max-width:72rem;margin:auto;padding:2rem;line-height:1.7;color:#243047}header{border-bottom:1px solid #ddd;margin-bottom:2rem;padding-bottom:1rem}a{color:#1769aa;text-decoration:none}a:hover{text-decoration:underline}main{min-height:70vh}", encoding="utf-8")
    return {"ok":True,"output":str(dest),"engine":engine,"posts":len(posts),"images":copied}

def generate_thumbnail(source: str, output: str, width: int = 320, height: int = 180) -> dict[str, Any]:
    src, dst = Path(source), Path(output)
    if not src.is_file(): raise ValueError("source image does not exist")
    if ".build" not in dst.resolve().parts: raise ValueError("thumbnail output must be inside .build")
    if width <= 0 or height <= 0: raise ValueError("thumbnail dimensions must be positive")
    dst.parent.mkdir(parents=True, exist_ok=True)
    try:
        from PIL import Image
        with Image.open(src) as im:
            im.thumbnail((width, height)); canvas=Image.new("RGB", (width,height), "white"); canvas.paste(im.convert("RGB"), ((width-im.width)//2,(height-im.height)//2)); canvas.save(dst, format="JPEG", quality=85)
    except ImportError as exc:
        raise RuntimeError("thumbnail generation requires Pillow") from exc
    return {"ok":True,"output":str(dst),"width":width,"height":height,"sha256":hashlib.sha256(dst.read_bytes()).hexdigest()}

def make_mindmap(content: str | dict[str, Any], title: str = "Mindmap") -> dict[str, Any]:
    if isinstance(content, str): text = content
    else:
        parts=[]
        if content.get("content"): parts.append(str(content["content"]))
        for source in content.get("sources", []):
            if isinstance(source, str): parts.append(source)
            elif isinstance(source, dict): parts.append(str(source.get("content", source.get("text", ""))))
        text = "\n".join(parts)
        title = str(content.get("title", title))
    root={"id":"root","text":title,"children":[]}; stack=[(0,root)]
    for line in text.splitlines():
        heading=re.match(r"^(#{1,6})\s+(.+)$", line); bullet=re.match(r"^\s*[-*+]\s+(.+)$", line)
        if not heading and not bullet: continue
        level=len(heading.group(1)) if heading else 1; label=heading.group(2) if heading else bullet.group(1); node={"id":hashlib.sha1(label.encode()).hexdigest()[:12],"text":label.strip(),"children":[]}
        while stack[-1][0] >= level: stack.pop()
        stack[-1][1]["children"].append(node); stack.append((level,node))
    return {"ok":True,"root":root,"source":"deterministic"}

def publish_site(site: str, config: dict[str, Any] | None = None) -> dict[str, Any]:
    cfg=config or {}; cwd=Path(site)
    if not cwd.is_dir(): raise ValueError("site must be a directory")
    owner, repo = cfg.get("owner"), cfg.get("repo")
    remote = f"https://github.com/{owner}/{repo}.git" if owner and repo else None
    branch = cfg.get("branch", "gh-pages")
    if not remote: raise ValueError("publish config requires owner/repo")
    if not re.fullmatch(r"[A-Za-z0-9._/-]+", branch): raise ValueError("invalid branch")
    token = str(cfg.get("token") or "")
    if not token: raise ValueError("publish config requires token")
    commands=[["git","init"],["git","config","user.name","airipress"],["git","config","user.email","airipress@localhost"],["git","add","-A"],["git","commit","-m",cfg.get("message","publish site")],["git","branch","-M",branch],["git","remote","remove","origin"],["git","remote","add","origin",remote],["git","push","--force","origin",branch]]; results=[]
    with tempfile.TemporaryDirectory(prefix="airipress-askpass-") as temp:
        askpass = Path(temp) / "askpass.sh"
        askpass.write_text("#!/bin/sh\ncase \"$1\" in *Username*) printf %s x-access-token;; *) printf %s \"$AIRIPRESS_GITHUB_TOKEN\";; esac\n", encoding="utf-8")
        askpass.chmod(0o700)
        child_env = {**os.environ, "GIT_ASKPASS": str(askpass), "GIT_TERMINAL_PROMPT": "0", "AIRIPRESS_GITHUB_TOKEN": token}
        for command in commands:
            p=subprocess.run(command,cwd=cwd,text=True,capture_output=True,env=child_env)
            stdout=p.stdout[-2000:].replace(token,"[redacted]"); stderr=p.stderr[-2000:].replace(token,"[redacted]")
            results.append({"command":command,"returncode":p.returncode,"stdout":stdout,"stderr":stderr})
            if command[:3] == ["git", "remote", "remove"] and p.returncode: continue
            if p.returncode: return {"ok":False,"status":"failed","results":results}
        commit=subprocess.run(["git","rev-parse","HEAD"],cwd=cwd,text=True,capture_output=True,env=child_env).stdout.strip()
    return {"ok":True,"status":"published","branch":branch,"commit":commit,"url":f"https://{owner}.github.io/{repo}/" if owner and repo else None,"results":results}
