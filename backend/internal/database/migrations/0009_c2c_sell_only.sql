-- +goose Up
-- ADR-0023：C2C 只保留卖单。
-- 已终结的历史买单及其交易、账本保持原样；`side` 列因此保留，仅禁止再产生买单。
-- 未终结的买单状态（open/allocated 买单，或买单下处于 awaiting_payment/paid/disputed 的交易）
-- 持有账本冻结额，迁移既不静默取消也不改写账本，必须由管理员先在旧版本中处理完毕。

-- +goose StatementBegin
DO $$
DECLARE
    open_orders bigint;
    unfinished_trades bigint;
BEGIN
    SELECT count(*) INTO open_orders
      FROM c2c_orders
     WHERE side = 'buy' AND status IN ('open', 'allocated');
    SELECT count(*) INTO unfinished_trades
      FROM c2c_trades t
      JOIN c2c_orders o ON o.id = t.order_id
     WHERE o.side = 'buy' AND t.status IN ('awaiting_payment', 'paid', 'disputed');
    IF open_orders > 0 OR unfinished_trades > 0 THEN
        RAISE EXCEPTION 'C2C buy orders are removed: % open buy order(s) and % unfinished buy-order trade(s) remain; cancel or resolve them in the previous release before upgrading',
            open_orders, unfinished_trades
            USING ERRCODE = '55000';
    END IF;
END;
$$;
-- +goose StatementEnd

-- 新订单只能是卖单（插入时默认 sell）；历史买单行不受影响（NOT VALID 只约束之后的写入）。
ALTER TABLE c2c_orders ALTER COLUMN side SET DEFAULT 'sell';
ALTER TABLE c2c_orders DROP CONSTRAINT c2c_orders_side_check;
ALTER TABLE c2c_orders ADD CONSTRAINT c2c_orders_sell_only_check CHECK (side = 'sell') NOT VALID;
DROP INDEX c2c_orders_buy_book_idx;

-- 校验函数只对卖单与卖单成交成立；遗留的历史买单行不再被重新校验。
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION verify_c2c_order_invariants() RETURNS trigger AS $$
DECLARE
    target_order_id uuid;
    item c2c_orders%ROWTYPE;
    hold ledger_holds%ROWTYPE;
BEGIN
    IF TG_TABLE_NAME = 'c2c_orders' THEN
        target_order_id := NEW.id;
    ELSE
        SELECT id INTO target_order_id FROM c2c_orders WHERE parent_hold_id = NEW.id;
        IF target_order_id IS NULL THEN
            RETURN NULL;
        END IF;
    END IF;
    SELECT * INTO item FROM c2c_orders WHERE id = target_order_id;
    IF item.id IS NULL OR item.side <> 'sell' THEN
        RETURN NULL;
    END IF;
    IF item.status = 'cancelled' THEN
        IF item.available_nano <> 0 THEN
            RAISE EXCEPTION 'cancelled C2C order cannot remain matchable' USING ERRCODE = '23514';
        END IF;
    ELSIF item.available_nano > 0 AND item.status <> 'open' THEN
        RAISE EXCEPTION 'matchable C2C order must be open' USING ERRCODE = '23514';
    ELSIF item.available_nano = 0 AND item.allocated_nano > 0 AND item.status <> 'allocated' THEN
        RAISE EXCEPTION 'fully allocated C2C order has invalid state' USING ERRCODE = '23514';
    ELSIF item.available_nano = 0 AND item.allocated_nano = 0 AND item.settled_nano = item.total_nano AND item.status <> 'filled' THEN
        RAISE EXCEPTION 'fully settled C2C order must be filled' USING ERRCODE = '23514';
    END IF;
    SELECT * INTO hold FROM ledger_holds WHERE id = item.parent_hold_id;
    IF hold.id IS NULL
       OR hold.purpose <> 'asset_reservation'
       OR hold.funding_policy <> 'settled_balance_only'
       OR hold.business_type <> 'c2c_sell_order'
       OR hold.business_id <> item.id::text
       OR hold.amount_nano <> item.total_nano
       OR hold.remaining_nano <> item.available_nano + item.allocated_nano
       OR hold.captured_nano <> item.settled_nano
       OR hold.released_nano <> item.closed_nano
       OR NOT EXISTS (
           SELECT 1 FROM ledger_accounts la
            WHERE la.id = hold.ledger_account_id
              AND la.identity_account_id = item.owner_account_id
       ) THEN
        RAISE EXCEPTION 'C2C sell order and asset hold diverged' USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION verify_c2c_trade_invariants_row(target_trade_id uuid) RETURNS void AS $$
DECLARE
    item c2c_trades%ROWTYPE;
    parent c2c_orders%ROWTYPE;
    effect_kind text;
    effect_transaction uuid;
BEGIN
    SELECT * INTO item FROM c2c_trades WHERE id = target_trade_id;
    IF item.id IS NULL THEN
        RETURN;
    END IF;
    SELECT * INTO parent FROM c2c_orders WHERE id = item.order_id;
    IF parent.id IS NULL THEN
        RAISE EXCEPTION 'C2C trade references missing order' USING ERRCODE = '23514';
    END IF;
    IF parent.side <> 'sell' THEN
        RETURN;
    END IF;
    IF item.seller_account_id <> parent.owner_account_id OR item.hold_id <> parent.parent_hold_id THEN
        RAISE EXCEPTION 'C2C trade parties do not match parent order' USING ERRCODE = '23514';
    END IF;
    SELECT kind, transaction_id INTO effect_kind, effect_transaction
      FROM ledger_hold_events
     WHERE hold_id = item.hold_id AND business_id = item.id::text;
    IF item.status IN ('awaiting_payment', 'paid', 'disputed') AND effect_kind IS NOT NULL THEN
        RAISE EXCEPTION 'nonterminal C2C trade cannot have a hold effect' USING ERRCODE = '23514';
    END IF;
    IF item.status = 'released_to_buyer' AND (effect_kind <> 'capture' OR effect_transaction <> item.ledger_transaction_id) THEN
        RAISE EXCEPTION 'released C2C trade must match one hold capture' USING ERRCODE = '23514';
    END IF;
    IF item.status IN ('returned_to_seller', 'cancelled', 'expired') THEN
        IF parent.status = 'cancelled' AND effect_kind <> 'release' THEN
            RAISE EXCEPTION 'trade under cancelled sell order must release its allocation' USING ERRCODE = '23514';
        ELSIF parent.status <> 'cancelled' AND effect_kind IS NOT NULL THEN
            RAISE EXCEPTION 'trade under open sell order must restore allocation without release' USING ERRCODE = '23514';
        END IF;
    END IF;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down
-- 迁移删除了买单校验分支且无法还原，需要回滚时请从备份恢复。
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'migration 0009 (C2C sell-only) is irreversible; restore from backup' USING ERRCODE = '0A000'; END $$;
-- +goose StatementEnd
