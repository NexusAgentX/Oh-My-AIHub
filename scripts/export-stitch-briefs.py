#!/usr/bin/env python3
"""Export self-contained Stitch inputs without duplicating maintained sources."""

import argparse
import json
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
    palette = ["这组配色提供现有品牌的起点，可围绕贴纸识别感与可读性继续发展。",
               "", "| 用途 | 浅色 | 墨夜 |", "| --- | --- | --- |"]
    palette.extend(f"| {label} | `{light['--' + name].strip()}` | "
                   f"`{dark['--' + name].strip()}` |" for name, label in colors)
    palette = "\n".join(palette) + "\n"

    if visual.count("\n## Colors\n") != 1:
        parser.error("Visual source must have exactly one Colors section")
    brand_colors = {"primary": "mustard", "secondary": "ink", "tertiary": "mint",
                    "neutral": "paper", "surface": "canvas", "on-surface": "body"}
    metadata = ["---", "name: Oh-My-AIHub", "colors:"]
    metadata.extend(f"  {name}: {json.dumps(light['--' + token].strip())}"
                    for name, token in brand_colors.items())
    metadata.append("---")
    design = "\n".join(metadata) + "\n\n" + visual.replace(
        "\n## Colors\n", "\n## Colors\n\n" + palette)
    files = {"DESIGN.md": design, "product-spec.md": spec}
    for platform in ("desktop", "mobile"):
        brief = (source / (platform + ".md")).read_text(encoding="utf-8")
        files[f"{platform}/prompt.txt"] = "\n\n".join(
            part.rstrip() for part in (brief, spec)) + "\n"
    files["README.md"] = (
        "# Oh-My-AIHub · Google Stitch 交接\n\n"
        "在 Start with your design 页面，Upload a DESIGN.md file 上传 DESIGN.md。\n\n"
        "Additional instructions 可填写：保留芥末与墨的贴纸风，"
        "以此建立适合新页面的视觉系统。\n\n"
        "完成风格导入后，在项目设计对话中粘贴 prompt.txt 全文："
        "桌面项目使用 desktop/prompt.txt，移动项目使用 mobile/prompt.txt。\n\n"
        "两个项目共用 DESIGN.md，各自的 prompt.txt 包含本端任务与核心业务。"
        "product-spec.md 供单独阅读。\n"
    )
    output.mkdir(parents=True)
    for name, content in files.items():
        destination = output / name
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_text(content, encoding="utf-8")
    shutil.make_archive(str(output), "zip", root_dir=output)
    print(f"Exported {len(files)} files to {output}")
    print(f"ZIP: {archive}")


if __name__ == "__main__":
    main()
