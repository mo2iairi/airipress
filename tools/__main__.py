import argparse, json, sys
from pathlib import Path
from .core import export_site, generate_thumbnail, make_mindmap, publish_site
def main():
    p=argparse.ArgumentParser(prog="airipress-tools"); p.add_argument("command",choices=["export","publish","thumbnail","mindmap"]); p.add_argument("--input")
    a=p.parse_args(); cfg=json.loads(Path(a.input).read_text() if a.input else (sys.stdin.read() or "{}"))
    if a.command=="export": result=export_site(cfg["workspace"],cfg["output"],cfg.get("title","airipress"),cfg.get("engine","astro"),cfg.get("theme_path",""),cfg.get("theme_id",""))
    elif a.command=="publish": result=publish_site(cfg["site"],cfg)
    elif a.command=="thumbnail": result=generate_thumbnail(cfg["source"],cfg["output"],cfg.get("width",320),cfg.get("height",180))
    else: result=make_mindmap(cfg.get("content", ""),cfg.get("title","Mindmap"))
    print(json.dumps(result,ensure_ascii=False))
if __name__ == "__main__": main()
