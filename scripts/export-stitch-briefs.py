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
    colors = [("paper", "纸面"), ("paper-dot", "点阵"), ("canvas", "贴纸表面"),
              ("ink", "标题墨色"), ("body", "正文"), ("muted", "次要文字"),
              ("line", "描边"), ("shadow-ink", "硬投影"), ("mustard", "芥末主色"),
              ("on-color", "色块上文字"), ("mint", "薄荷"), ("sky", "浅蓝"),
              ("rose", "粉色")]
    palette = ["# 品牌配色参考", "",
               "这组配色提供现有品牌的起点，可围绕贴纸识别感与可读性继续发展。",
               "", "| 用途 | 浅色 | 墨夜 |", "| --- | --- | --- |"]
    palette.extend(f"| {label} | `{light['--' + name].strip()}` | "
                   f"`{dark['--' + name].strip()}` |" for name, label in colors)
    palette = "\n".join(palette) + "\n"

    files = {"product-spec.md": spec}
    for platform in ("desktop", "mobile"):
        brief = (source / (platform + ".md")).read_text(encoding="utf-8")
        files[f"{platform}/design.md"] = "\n\n---\n\n".join(
            part.rstrip() for part in (brief, spec, visual, palette)) + "\n"
    files["README.md"] = (
        "# Oh-My-AIHub · Google Stitch 交接\n\n"
        "建立两个独立项目：桌面项目上传 desktop/design.md，"
        "移动项目上传 mobile/design.md。\n\n"
        "每份 design.md 都包含核心产品简报、贴纸品牌与本端体验方向，"
        "单文件即可使用。product-spec.md 供单独阅读。\n\n"
        "请 Stitch 围绕用 API、共享 API、买卖积分三条主流程发挥产品与交互判断，"
        "自由组织页面、导航、布局和步骤，独立决定两端结构。\n\n"
        "公共资料从相应实体详情发起编辑，新增从目录或父实体开始。"
        "先说明产品组织，再生成主要页面和任务流程，细节后续补充。\n"
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
