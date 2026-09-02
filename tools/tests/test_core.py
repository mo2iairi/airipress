from pathlib import Path
from tools.core import export_site, make_mindmap

def test_export_and_manifest(tmp_path: Path):
    (tmp_path / "markdown/topic").mkdir(parents=True)
    (tmp_path / "markdown/topic/a.md").write_text("# Hello\nbody", encoding="utf-8")
    out = tmp_path / "site"
    result = export_site(str(tmp_path), str(out))
    assert result["posts"] == 1
    assert (out / "src/pages/topic-a.md").exists()
    assert '"posts"' in (out / "manifest.json").read_text()

def test_mindmap_fallback_is_deterministic():
    first = make_mindmap("# A\n## B", "T")
    assert first["source"] == "deterministic"
    assert first["root"]["children"][0]["children"][0]["text"] == "B"
    assert first == make_mindmap("# A\n## B", "T")
