-- +goose Up
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- 模型最大输出 token 数：供 Anthropic 形态模型列表的 max_tokens 与 Gemini 的 outputTokenLimit 使用；未知时为空。
ALTER TABLE models
    ADD COLUMN max_output_tokens bigint CHECK (max_output_tokens IS NULL OR max_output_tokens > 0);

-- +goose Down
ALTER TABLE models DROP COLUMN max_output_tokens;
