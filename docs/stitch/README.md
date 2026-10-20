# Google Stitch 设计交接

本目录面向生产产品的原型探索，记录维护者在本轮确认的核心能力、贡献方式与设计自由度。产品设计的重点是骨架和主要交互；当前网站的完整功能与实现事实继续由 `PRODUCT.md`、`DESIGN.md` 和源码维护。

## 交接内容

| 文件 | 用途 |
| --- | --- |
| [product-spec.md](product-spec.md) | 业务背景、七项核心能力、实体内编辑与三条主流程 |
| [visual-language.md](visual-language.md) | 保留的贴纸品牌识别与可发展的视觉方向 |
| [desktop.md](desktop.md) | 桌面应用与安装后独立窗口的体验方向 |
| [mobile.md](mobile.md) | 独立移动结构、单手操作和原生感 PWA 的体验方向 |

首版围绕用 API、共享 API、买卖积分三条主流程组织骨架。页面数量、导航、布局与交互由 Stitch 发挥，账户、余额、实体编辑与管理操作自然融入相关任务。两端分别设计，各自让核心任务成立。

贡献融入实体维护：在相应详情发起编辑，从目录或父实体创建新对象，审核结果与对象关联。

交接以自然语言说明业务与任务。本次更新只修改设计输入。

## 导出上传文件

在仓库根目录运行（Python 3.9 或更新版本，使用标准库）：

```sh
python3 scripts/export-stitch-briefs.py --output /tmp/oh-my-aihub-stitch-v3
```

选择尚未使用的输出目录与同名 ZIP 路径，得到：

```text
oh-my-aihub-stitch-v3/
  README.md
  product-spec.md
  desktop/design.md
  mobile/design.md
oh-my-aihub-stitch-v3.zip
```

每份 `design.md` 都包含本端方向、产品简报、共享视觉和品牌配色参考，单文件即可独立使用。配色从现有 `tokens.css` 提取。

## 两个项目的起始提示

桌面项目上传 `desktop/design.md`：

> 请以产品设计师和交互设计师的判断，为 Oh-My-AIHub 设计首版桌面应用。围绕简报里的核心任务，自由组织页面、导航、布局与流程，延续贴纸品牌。先说明产品思路，再生成能贯通主要任务的原型骨架，考虑 PWA 安装后的独立窗口体验。

移动项目上传 `mobile/design.md`：

> 请为 Oh-My-AIHub 设计首版原生感移动 PWA。围绕简报里的核心任务，从手机使用情境自由组织首页、导航、页面与流程，延续贴纸品牌。先说明组织思路，再用主要页面和连续流程表达顺手的移动应用骨架。

核对交付时关注三条主流程是否贯通、核心能力是否有自然落点、实体内编辑是否顺手，以及两端各自的整体体验。
