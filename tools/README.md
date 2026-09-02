# airipress-tools

无常驻服务的 Python CLI。命令从 stdin（或 `--input file.json`）读取一个 JSON 对象，并向 stdout 输出单行 JSON；错误以非零退出码报告。Go 可启动 `python -m tools <command>`。

* `export`: `{workspace, output, title?}`。导出 `markdown/**/*.md`、图片到 Astro 目录，并生成 `manifest.json`、`package.json`、`astro.config.mjs`、默认 layout/CSS，可直接 `npm install && npm run build`。
* `thumbnail`: `{source, output, width?, height?}`。输出到 `.build`；使用 Pillow 等比居中缩放为 JPEG，缺少 Pillow 时明确失败，不会用原图伪装缩略图。
* `mindmap`: `{content, title?}` 或 `{sources:[{content|text}], title?}`，按标题/列表生成确定性 `{ok,root,source}` 树。
* `publish`: `{site, owner, repo, branch?, token}`。固定真实执行 git 初始化、提交并推送 Pages 分支；token 只通过子进程环境提供给 `GIT_ASKPASS`，不会进入命令或日志。返回分支、commit 和 Pages URL。

例：`echo '{"workspace":"data","output":"/tmp/site"}' | python -m tools export`
