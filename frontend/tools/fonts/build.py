#!/usr/bin/env python3
"""生成自托管的标题字体：Baloo 2（拉丁与数字）与按字切片的源泉圆体（中文）。

用法（需要 fontTools 与 brotli：python3 -m pip install fonttools brotli）：

    python3 frontend/tools/fonts/build.py

从 npm 下载固定版本的字体包并校验 sha512，输出：
- frontend/public/fonts/：Baloo 2 与源泉圆体切片（woff2）
- frontend/src/styles/fonts.css：对应的 @font-face 与 unicode-range

源泉圆体只取 Bold 一个字重。切片顺序如下，浏览器只下载页面实际用到的切片：
1. 常用中文标点与前端源码里出现的中文，切成小片。
2. GB2312 一级字、二级字。
3. 字体里其余的 CJK 汉字，其余全角符号与假名。
拉丁字母、数字与通用标点交给 Baloo 2。
"""

import base64
import hashlib
import io
import re
import sys
import tarfile
import urllib.request
from pathlib import Path

from fontTools import subset
from fontTools.ttLib import TTFont

ROOT = Path(__file__).resolve().parents[2]
PUBLIC = ROOT / "public" / "fonts"
CSS = ROOT / "src" / "styles" / "fonts.css"

BALOO = {
    "url": "https://registry.npmjs.org/@fontsource/baloo-2/-/baloo-2-5.3.0.tgz",
    "integrity": "sha512-CBuxZ27jvMmD5+KaaPkMWv/yjdRNppVAd5/002yAyvyQyLNL3aZCbkx6gbAT/bjxPd1A0VMdDQjfcELI8chccQ==",
    "member": "package/files/baloo-2-latin-800-normal.woff2",
}
GENSEN = {
    "url": "https://registry.npmjs.org/@fontpkg/gen-sen-maru-gothic-tw-ttf/-/gen-sen-maru-gothic-tw-ttf-1.301.0.tgz",
    "integrity": "sha512-JqpADuiCDHFQBLeMXgu+3+KH1rNiXNrLRGT1MYFSFcVnGvhdWLGY5A5SvF43e0lpoolR1x6avkK2c09YoZDB8Q==",
    "member": "package/GenSenMaruGothicTW-Bold.ttf",
}
# 与 @fontsource/baloo-2 latin 子集的 unicode-range 一致
BALOO_RANGE = (
    "U+0000-00FF,U+0131,U+0152-0153,U+02BB-02BC,U+02C6,U+02DA,U+02DC,U+0304,U+0308,"
    "U+0329,U+2000-206F,U+20AC,U+2122,U+2191,U+2193,U+2212,U+2215,U+FEFF,U+FFFD"
)
GENSEN_DIR = "gensen-rounded-1301-bold"  # 版本写进目录名，字体更新时换目录即可绕过缓存


def download(spec):
    with urllib.request.urlopen(spec["url"]) as response:
        data = response.read()
    digest = "sha512-" + base64.b64encode(hashlib.sha512(data).digest()).decode()
    if digest != spec["integrity"]:
        sys.exit(f"校验失败：{spec['url']}")
    with tarfile.open(fileobj=io.BytesIO(data)) as archive:
        return archive.extractfile(spec["member"]).read()


def is_cjk(code):
    return 0x4E00 <= code <= 0x9FFF or 0x3400 <= code <= 0x4DBF


def source_chars():
    """前端源码里出现的中文。"""
    found = set()
    for path in [ROOT / "index.html", *(ROOT / "src").rglob("*.ts*")]:
        if path.name.endswith((".test.ts", ".test.tsx", ".gen.ts")):
            continue
        found.update(ord(char) for char in path.read_text(encoding="utf-8") if is_cjk(ord(char)))
    return found


def gb2312_levels():
    """GB2312 一级字（16–55 区）与二级字（56–87 区），按码位顺序。"""
    levels = ([], [])
    for row in range(16, 88):
        for cell in range(1, 95):
            try:
                char = bytes([0xA0 + row, 0xA0 + cell]).decode("gb2312")
            except UnicodeDecodeError:
                continue
            levels[0 if row < 56 else 1].append(ord(char))
    return levels


def chunks(codes, size):
    return [codes[index : index + size] for index in range(0, len(codes), size)]


def unicode_range(codes):
    parts = []
    codes = sorted(codes)
    start = previous = codes[0]
    for code in codes[1:] + [None]:
        if code is not None and code == previous + 1:
            previous = code
            continue
        parts.append(f"U+{start:04X}" if start == previous else f"U+{start:04X}-{previous:04X}")
        if code is not None:
            start = previous = code
    return ",".join(parts)


def plan(available):
    """按使用频率从高到低排好切片，每个码位只出现一次。"""
    used = set()
    groups = []

    def take(codes, size):
        picked = [code for code in codes if code in available and code not in used]
        used.update(picked)
        groups.extend(chunks(picked, size))

    level1, level2 = gb2312_levels()
    common_punctuation = [ord(char) for char in "，。、：；！？（）《》「」『』【】〈〉…—～"]
    take(common_punctuation + sorted(source_chars()), 120)
    take(level1, 200)
    take(level2, 300)
    take(sorted(code for code in available if is_cjk(code)), 500)
    take([*range(0x3000, 0x3040), *range(0xFF00, 0xFFF0)], 300)
    take(list(range(0x3040, 0x3100)), 300)
    return groups


def subset_woff2(font_bytes, codes):
    font = TTFont(io.BytesIO(font_bytes))
    options = subset.Options()
    options.flavor = "woff2"
    options.layout_features = ["*"]
    options.hinting = False
    options.drop_tables += ["FFTM"]
    options.name_IDs = [0, 1, 2, 3, 4, 5, 6, 13, 14]
    options.name_languages = ["*"]
    subsetter = subset.Subsetter(options)
    subsetter.populate(unicodes=codes)
    subsetter.subset(font)
    output = io.BytesIO()
    font.flavor = "woff2"
    font.save(output)
    return output.getvalue()


def face(family, url, unicode_ranges):
    return (
        "@font-face {\n"
        f'  font-family: "{family}";\n'
        "  font-style: normal;\n"
        "  font-weight: 700 900;\n"
        "  font-display: swap;\n"
        f'  src: url("{url}") format("woff2");\n'
        f"  unicode-range: {unicode_ranges};\n"
        "}\n"
    )


def main():
    PUBLIC.mkdir(parents=True, exist_ok=True)
    gensen_out = PUBLIC / GENSEN_DIR
    gensen_out.mkdir(exist_ok=True)
    for old in gensen_out.glob("*.woff2"):
        old.unlink()

    (PUBLIC / "baloo-2-latin-800.woff2").write_bytes(download(BALOO))
    faces = [face("Baloo 2", "/fonts/baloo-2-latin-800.woff2", BALOO_RANGE)]

    gensen = download(GENSEN)
    available = set(TTFont(io.BytesIO(gensen)).getBestCmap())
    total = 0
    for index, codes in enumerate(plan(available)):
        name = f"{index:03d}.woff2"
        data = subset_woff2(gensen, codes)
        (gensen_out / name).write_bytes(data)
        total += len(data)
        faces.append(face("GenSen Rounded", f"/fonts/{GENSEN_DIR}/{name}", unicode_range(codes)))

    header = (
        "/*\n"
        " * 标题与数字字体（自托管），由 frontend/tools/fonts/build.py 生成，不要手改。\n"
        " * Baloo 2 与源泉圆体均为 SIL OFL 1.1，许可见 /licenses/fonts.txt。\n"
        " */\n"
    )
    CSS.write_text(header + "\n".join(faces), encoding="utf-8")
    print(f"源泉圆体 {len(faces) - 1} 个切片，共 {total / 1024 / 1024:.1f} MiB")


if __name__ == "__main__":
    main()
