# Google Stitch 设计交接

首版围绕用 API、共享 API、买卖积分三条主流程组织骨架。页面、导航、布局与交互由 Stitch 发挥，账户、余额、实体编辑与管理操作融入相关任务。桌面与移动分别设计。

## 两类输入

| 输入 | 内容与用途 |
| --- | --- |
| `DESIGN.md` | 品牌、配色、字体和贴纸视觉语言，用于导入视觉系统；两端共用 |
| `desktop/prompt.txt` / `mobile/prompt.txt` | 本端设计任务与核心业务，粘贴到相应项目的设计对话 |

`DESIGN.md` 按 [Google 的格式说明](https://github.com/google-labs-code/design.md/blob/main/docs/spec.md) 组织：YAML 品牌配色与 Markdown 视觉章节。业务任务独立保存，输入时各就其位。

## 源文件

| 文件 | 用途 |
| --- | --- |
| [product-spec.md](product-spec.md) | 七项核心能力、实体内编辑与三条主流程 |
| [visual-language.md](visual-language.md) | 视觉章节，导出时填入现有双主题配色 |
| [desktop.md](desktop.md) | 桌面应用与独立窗口的设计任务 |
| [mobile.md](mobile.md) | 原生感移动 PWA 的设计任务 |

贡献融入实体维护：在相应详情编辑，从目录或父实体创建，审核结果与对象关联。交接以自然语言说明首版任务；当前实现事实继续由产品文档与源码维护。本次更新只修改设计输入。

## 导出

在仓库根目录运行（Python 3.9 或更新版本，使用标准库）：

```sh
python3 scripts/export-stitch-briefs.py --output /tmp/oh-my-aihub-stitch-v4
```

选择尚未使用的输出目录与同名 ZIP 路径，得到：

```text
oh-my-aihub-stitch-v4/
  README.md
  DESIGN.md
  product-spec.md
  desktop/prompt.txt
  mobile/prompt.txt
oh-my-aihub-stitch-v4.zip
```

配色从现有 `tokens.css` 提取。每份 `prompt.txt` 都带有本端任务和完整的首版业务简报，可以独立粘贴使用。

## 对应 Stitch 界面

在 `Start with your design` 页面：

1. `Upload a DESIGN.md file` 上传导出的 `DESIGN.md`。
2. `Additional instructions` 可填“保留芥末与墨的贴纸风，以此建立适合新页面的视觉系统”。
3. 完成风格导入后，将相应端的 `prompt.txt` 全文粘贴到项目设计对话中，开始首版原型设计。

核对原型时关注三条主流程是否贯通、实体内编辑是否顺手，以及两端各自的整体体验。
