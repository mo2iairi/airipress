import json, tempfile, unittest
from pathlib import Path
from unittest.mock import patch
from tools.core import export_site, make_mindmap, publish_site

class ToolsTest(unittest.TestCase):
    def test_astro_project_files(self):
        with tempfile.TemporaryDirectory() as d:
            root=Path(d); (root/'markdown').mkdir(); (root/'markdown/a.md').write_text('# A', encoding='utf-8')
            out=root/'out'; export_site(str(root), str(out))
            for name in ('package.json','astro.config.mjs','src/layouts/Base.astro','public/style.css'):
                self.assertTrue((out/name).exists(), name)
            self.assertEqual(json.loads((out/'package.json').read_text())['scripts']['build'], 'astro build')

    def test_hugo_project_files(self):
        with tempfile.TemporaryDirectory() as d:
            root=Path(d)/'workspace'; (root/'markdown').mkdir(parents=True); (root/'markdown/a.md').write_text('# A', encoding='utf-8')
            out=Path(d)/'site'; result=export_site(str(root), str(out), engine='hugo')
            self.assertEqual(result['engine'], 'hugo')
            self.assertTrue((out/'hugo.toml').exists())
            self.assertTrue((out/'content/a.md').exists())

    def test_manifest_sources_and_path_escape(self):
        m=make_mindmap({'title':'M','sources':[{'content':'# One\n## Two'}]})
        self.assertEqual(m['root']['children'][0]['children'][0]['text'], 'Two')
        with self.assertRaises(ValueError):
            from tools.core import _safe
            _safe(Path('/tmp/root'), '../outside')

    @patch('tools.core.subprocess.run')
    def test_publish_fixed_commands_and_redaction(self, run):
        class R:
            returncode=0; stdout='abc123\n'; stderr=''
        run.return_value=R()
        with tempfile.TemporaryDirectory() as d:
            result=publish_site(d, {'owner':'o','repo':'r','token':'SECRET'})
        self.assertEqual(result['status'], 'published')
        commands=[call.args[0] for call in run.call_args_list]
        self.assertTrue(any(c[:3] == ['git','push','--force'] for c in commands))
        self.assertNotIn('SECRET', json.dumps(result))
        self.assertNotIn('SECRET', json.dumps(commands))
        self.assertTrue(all('SECRET' not in json.dumps(call.args[0]) for call in run.call_args_list))

if __name__ == '__main__': unittest.main()
