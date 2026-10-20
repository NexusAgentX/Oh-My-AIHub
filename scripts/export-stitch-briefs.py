#!/usr/bin/env python3
"""Export self-contained Stitch inputs without duplicating maintained sources."""

import argparse
from pathlib import Path
import re
import shutil


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True,
                        help="New output directory outside the repository")
    output = parser.parse_args().output.expanduser().resolve()
    root = Path(__file__).resolve().parent.parent
    archive = output.with_name(output.name + ".zip")
    if output.is_relative_to(root):
        parser.error("Output must be outside the repository")
    if output.exists() or archive.exists():
        parser.error("Output directory or ZIP already exists; choose a new name")

    source = root / "docs" / "stitch"
    spec = (source / "product-spec.md").read_text(encoding="utf-8")
    visual = (source / "visual-language.md").read_text(encoding="utf-8")
    css = (root / "frontend/src/styles/tokens.css").read_text(encoding="utf-8")
    css = re.sub(r"/\*.*?\*/", "", css, flags=re.S)
    light_block = re.search(r":root\s*\{(.*?)\}", css, re.S)
    dark_block = re.search(r":root\[data-theme='dark'\]\s*\{(.*?)\}", css, re.S)
    if not light_block or not dark_block:
        parser.error("Cannot find both theme token blocks")
    token_pattern = r"(--[\w-]+)\s*:\s*([^;]+);"
    light = dict(re.findall(token_pattern, light_block.group(1)))
    dark = {**light, **dict(re.findall(token_pattern, dark_block.group(1)))}
    names = ["paper", "paper-dot", "canvas", "sunken", "hover", "selected",
             "ink", "body", "muted", "hairline", "hairline-strong", "line",
             "shadow-ink", "mustard", "mustard-hover", "mustard-text", "on-color",
             "rose", "sky", "mint", "positive", "warning", "danger", "info",
             "code-bg", "code-ink", "scrim"]
    palette = ["# 品牌配色快照", "",
               "由现有 tokens.css 自动提取；仅用于本次设计输入，浅色与墨夜分别取值。",
               "", "| Token | 浅色 | 墨夜 |", "| --- | --- | --- |"]
    palette.extend(f"| `--{name}` | `{light['--' + name].strip()}` | "
                   f"`{dark['--' + name].strip()}` |" for name in names)
    palette = "\n".join(palette) + "\n"

    files = {"product-spec.md": spec}
    for platform in ("desktop", "mobile"):
        brief = (source / (platform + ".md")).read_text(encoding="utf-8")
        files[f"{platform}/design.md"] = "\n\n---\n\n".join(
            part.rstrip() for part in (brief, visual, spec, palette)) + "\n"
    files["README.md"] = (
        "# Oh-My-AIHub · Google Stitch 交接\n\n"
        "建立两个独立项目：桌面项目上传 desktop/design.md，"
        "移动项目上传 mobile/design.md。\n\n"
        "每份 design.md 都包含完整产品规格、共享贴纸风与本端要求，"
        "无需再上传另一端文件或依赖仓库链接。product-spec.md 供单独阅读。\n\n"
        "要求 Stitch 先给信息架构、完整屏幕目录及 F01–F23 / A01–A12 覆盖表，"
        "再按目录逐批生成全部成员与管理原型及状态。页面数量、导航、组织均可重做。"
        "两个端可以采用不同结构；PWA 相关内容是设计目标，不是已实现能力。\n"
    )
    output.mkdir(parents=True)
    for name, content in files.items():
        destination = output / name
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_text(content, encoding="utf-8")
    shutil.make_archive(str(output), "zip", root_dir=output)
    print(f"Exported {len(files)} Markdown files to {output}")
    print(f"ZIP: {archive}")


if __name__ == "__main__":
    main()
