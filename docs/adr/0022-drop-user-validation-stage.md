# ADR-0022：取消用户验证环节

- 状态：已通过
- 日期：2026-10-07
- 决策者：项目维护者
- 关联内容：[Feature #154](https://github.com/NexusAgentX/Oh-My-AIHub/issues/154)、[Feature #22](https://github.com/NexusAgentX/Oh-My-AIHub/issues/22)；部分取代 [ADR-0002](0002-adopt-ai-native-product-workflow.md)

## 背景

ADR-0002 把任务状态分为 Ready、Done 与 Validated，要求产品能力在交付之外再取得真实目标用户反馈或行为证据，并以小圈子试用（#22）作为首个方向的验证闭环。维护者在全面重构（Epic #109）完成后决定不再组织用户验证。

## 决策目标

- 任务流程只以交付完成为终点，不再等待或收集真实用户结果证据。
- 保留 Ready 的开工条件、Issue 四板块、测试门禁、现场验收与人类产品方向检查点。

不解决：是否保留已交付的试用证据摘要等代码能力（另行决定）。

## 候选方案

### 方案一：保留 Validated 但暂缓试用

流程中长期存在无法完成的状态与文档门槛，增加维护负担并误导后续任务。

### 方案二：取消用户验证环节

任务状态只有 Ready 与 Done；Done 即最终状态。文档、模板与路线图删除 Validated 与试用相关要求。

## 决定

采用方案二。`AGENTS.md`、`PRODUCT.md`、`ROADMAP.md`、`README.md` 与 Issue 模板移除 Validated、真实用户验证与试用要求；删除仅服务于试用的 `docs/runbooks/small-circle-trial.md`；关闭 #22。

## 后果

### 正面影响

- 流程与文档不再保留无法达成的门槛，任务在交付验收后即可结束。

### 负面影响与成本

- 产品方向不再有真实用户证据支撑，方向判断完全依赖维护者。

### 风险与缓解措施

- 方向偏离用户需求：由维护者在人类检查点判断发布候选是否符合产品方向。

## 验证方式

- 仓库文档与模板中不再要求 Validated 或真实用户验证（CHANGELOG 历史段与已通过 ADR 正文除外）。

## 替代关系

部分取代 ADR-0002 中 Done/Validated 分离与真实用户验证的内容；ADR-0002 其余决定继续有效。
