package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/c2c"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/contribution"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/detection"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

// Counts are exact only for completed scans. A failed or bounded source keeps
// its known rows but has null counts, never a plausible-looking zero.
type inboxItem struct {
	ID        string     `json:"id"`
	Category  string     `json:"category"`
	Title     string     `json:"title"`
	Message   string     `json:"message"`
	Href      string     `json:"href"`
	Blocker   bool       `json:"blocker"`
	Deadline  *time.Time `json:"deadline"`
	UpdatedAt time.Time  `json:"updated_at"`
}
type inboxCounts struct {
	Action   int `json:"action"`
	Anomaly  int `json:"anomaly"`
	Progress int `json:"progress"`
}
type inboxSource struct {
	ID        string       `json:"id"`
	Label     string       `json:"label"`
	Workspace string       `json:"workspace"`
	Href      string       `json:"href"`
	State     string       `json:"state"`
	Counts    *inboxCounts `json:"counts"`
	Items     []inboxItem  `json:"items"`
}
type inboxResponse struct {
	GeneratedAt time.Time     `json:"generated_at"`
	Sources     []inboxSource `json:"sources"`
}
type inboxLoader struct {
	source inboxSource
	load   func(context.Context) ([]inboxItem, error)
}

var errInboxLimited = errors.New("inbox source scan budget reached")
var errInboxUnavailable = errors.New("inbox source unavailable")

const inboxPageLimit = 100
const inboxScanLimit = 1000

// At most four source readers run per request and two requests per process.
// Reads have a six-second overall budget; no queue or background polling.
func (a *app) getInbox(w http.ResponseWriter, r *http.Request) {
	select {
	case a.inboxSlots <- struct{}{}:
		defer func() { <-a.inboxSlots }()
	default:
		writeError(w, 429, "inbox_busy", "待办暂时繁忙，请稍后重试")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	account := accountFromContext(ctx)
	now := time.Now().UTC()
	loaders := a.inboxLoaders(account, now)
	response := inboxResponse{GeneratedAt: now, Sources: make([]inboxSource, len(loaders))}
	var wg sync.WaitGroup
	jobs := make(chan int)
	for range 4 {
		wg.Go(func() {
			for index := range jobs {
				loader := loaders[index]
				source := loader.source
				source.Items, source.State = []inboxItem{}, "complete"
				items, err := loader.load(ctx)
				if items != nil {
					source.Items = items
				}
				if err != nil {
					source.State = "unavailable"
					if errors.Is(err, errInboxLimited) {
						source.State = "limited"
					}
				} else {
					counts := &inboxCounts{}
					for _, item := range source.Items {
						switch item.Category {
						case "action":
							counts.Action++
						case "anomaly":
							counts.Anomaly++
						case "progress":
							counts.Progress++
						}
					}
					source.Counts = counts
				}
				sort.SliceStable(source.Items, func(i, j int) bool {
					left, right := source.Items[i], source.Items[j]
					if left.Deadline != nil && right.Deadline != nil {
						return left.Deadline.Before(*right.Deadline)
					}
					if left.Deadline != nil {
						return true
					}
					if right.Deadline != nil {
						return false
					}
					if left.Blocker != right.Blocker {
						return left.Blocker
					}
					return left.UpdatedAt.After(right.UpdatedAt)
				})
				response.Sources[index] = source
			}
		})
	}
	for index := range loaders {
		jobs <- index
	}
	close(jobs)
	wg.Wait()
	writeJSON(w, 200, response)
}
func (a *app) inboxLoaders(account identity.Account, now time.Time) []inboxLoader {
	id := account.ID
	source := func(id, label, href string, load func(context.Context) ([]inboxItem, error)) inboxLoader {
		return inboxLoader{inboxSource{ID: id, Label: label, Href: href, Workspace: "member"}, load}
	}
	loaders := []inboxLoader{
		source("trades", "积分交易", "/points/trades", func(ctx context.Context) ([]inboxItem, error) {
			if a.c2c == nil {
				return nil, errInboxUnavailable
			}
			items := []inboxItem{}
			cursor := ""
			for scanned := 0; scanned < inboxScanLimit; scanned += inboxPageLimit {
				page, err := a.c2c.ListMySignals(ctx, id, cursor, inboxPageLimit)
				if err != nil {
					return items, err
				}
				for _, trade := range page.Items {
					if item, ok := inboxTrade(trade, now); ok {
						items = append(items, item)
					}
				}
				if page.Next == "" {
					return items, nil
				}
				if page.Next == cursor {
					return items, errInboxLimited
				}
				cursor = page.Next
			}
			return items, errInboxLimited
		}),
		source("channels", "我的渠道", "/supply/channels", func(ctx context.Context) ([]inboxItem, error) {
			if a.groupChannels == nil {
				return nil, errInboxUnavailable
			}
			rows, err := a.groupChannels.List(ctx, groupchannel.Actor{ID: id})
			if err != nil {
				return nil, err
			}
			items := []inboxItem{}
			for _, row := range rows {
				if item, ok := inboxChannel(row); ok {
					items = append(items, item)
				}
			}
			return items, nil
		}),
		source("keys", "API Key", "/keys", func(ctx context.Context) ([]inboxItem, error) {
			if a.keys == nil {
				return nil, errInboxUnavailable
			}
			rows, err := a.keys.List(ctx, id)
			if err != nil {
				return nil, err
			}
			items := []inboxItem{}
			for _, row := range rows {
				if item, ok := inboxKey(row, now); ok {
					items = append(items, item)
				}
			}
			return items, nil
		}),
		source("points", "钱包", "/points", func(ctx context.Context) ([]inboxItem, error) {
			if a.ledger == nil {
				return nil, errInboxUnavailable
			}
			points, err := a.ledger.Points(ctx, id)
			if err != nil {
				return nil, err
			}
			items := []inboxItem{}
			if points.Available() <= 0 {
				items = append(items, inboxItem{ID: "balance", Category: "anomaly", Title: "可用额度不足", Message: "调用可能被拒绝，请补充积分或查看账单。", Href: "/points", Blocker: true, UpdatedAt: points.UpdatedAt})
			}
			return items, nil
		}),
		source("contributions", "我的贡献", "/supply/contributions", func(ctx context.Context) ([]inboxItem, error) { return a.inboxContributions(ctx, account, false) }),
		source("detection", "检测任务", "/supply/detection", func(ctx context.Context) ([]inboxItem, error) {
			if a.detection == nil {
				return nil, errInboxUnavailable
			}
			items := []inboxItem{}
			var cursor *detection.Cursor
			for scanned := 0; scanned < inboxScanLimit; scanned += inboxPageLimit {
				rows, err := a.detection.List(ctx, detection.Actor{ID: id}, "", cursor, inboxPageLimit+1)
				if err != nil {
					return items, err
				}
				more := len(rows) > inboxPageLimit
				if more {
					rows = rows[:inboxPageLimit]
				}
				for _, row := range rows {
					if item, ok := inboxDetection(row); ok {
						items = append(items, item)
					}
				}
				if !more {
					return items, nil
				}
				last := rows[len(rows)-1]
				cursor = &detection.Cursor{At: last.CreatedAt, ID: last.ID}
			}
			return items, errInboxLimited
		}),
	}
	if account.IsAdmin {
		reviews := source("reviews", "贡献审核", "/admin/contributions", func(ctx context.Context) ([]inboxItem, error) { return a.inboxContributions(ctx, account, true) })
		reviews.source.Workspace = "admin"
		loaders = append(loaders, reviews)
		governance := source("governance", "治理提醒", "/admin", func(ctx context.Context) ([]inboxItem, error) {
			if a.observe == nil {
				return nil, errInboxUnavailable
			}
			overview, err := a.observe.Overview(ctx)
			if err != nil {
				return nil, err
			}
			items := []inboxItem{}
			for _, row := range overview.Attention {
				items = append(items, inboxItem{ID: row.Kind + ":" + row.Link, Category: "anomaly", Title: row.Title, Message: "查看治理详情", Href: row.Link, Blocker: row.Severity == "critical", UpdatedAt: now})
			}
			return items, nil
		})
		governance.source.Workspace = "admin"
		loaders = append(loaders, governance)
	}
	return loaders
}
func (a *app) inboxContributions(ctx context.Context, account identity.Account, queue bool) ([]inboxItem, error) {
	if a.contributions == nil {
		return nil, errInboxUnavailable
	}
	items := []inboxItem{}
	var cursor *contribution.Cursor
	actor := contribution.Actor{ID: account.ID, Admin: account.IsAdmin}
	for scanned := 0; scanned < inboxScanLimit; scanned += inboxPageLimit {
		rows, err := a.contributions.List(ctx, actor, queue, cursor, inboxPageLimit+1)
		if err != nil {
			return items, err
		}
		more := len(rows) > inboxPageLimit
		if more {
			rows = rows[:inboxPageLimit]
		}
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return items, err
			}
			if queue {
				items = append(items, inboxItem{ID: row.ID, Category: "action", Title: row.Title, Message: map[bool]string{true: "发布待完成，请重试完成发布。", false: "待审核，请查看内容与检测证据。"}[row.State == "publication_pending"], Href: "/admin/contributions/" + row.ID, UpdatedAt: row.UpdatedAt})
				continue
			}
			if row.State == "approved" {
				continue
			}
			detail, err := a.contributions.Detail(ctx, actor, row.ID)
			if err != nil {
				return items, err
			}
			if item, ok := inboxContribution(detail); ok {
				items = append(items, item)
			}
		}
		if !more {
			return items, nil
		}
		last := rows[len(rows)-1]
		cursor = &contribution.Cursor{At: last.CreatedAt, ID: last.ID}
	}
	return items, errInboxLimited
}
func inboxTrade(trade c2c.TradeSignal, now time.Time) (inboxItem, bool) {
	item := inboxItem{ID: trade.ID, Title: "积分交易", Category: "progress", Message: "等待交易对方处理", Href: "/points/trades/" + trade.ID, UpdatedAt: trade.CreatedAt}
	switch trade.Status {
	case c2c.TradeAwaitingPayment:
		item.Deadline = &trade.PaymentDeadline
		if !now.Before(trade.PaymentDeadline) {
			item.Message = "付款已超时，等待系统取消"
			item.Deadline = nil
		} else if trade.ViewerRole == "buyer" {
			item.Category = "action"
			item.Message = "待付款，请查看收款信息并完成付款。"
		} else {
			item.Message = "等待买家付款"
			item.Deadline = nil
		}
	case c2c.TradePaid:
		if trade.ViewerRole == "seller" {
			item.Category = "action"
			item.Message = "买家已标记付款，请确认收款后放行。"
			item.Blocker = true
		} else {
			item.Message = "已标记付款，等待卖家确认"
		}
	case c2c.TradeDisputed:
		missing := trade.MissingStatement
		if missing {
			item.Category = "action"
			item.Message = "申诉中，请补充你的陈述。"
		} else {
			item.Message = "申诉中，等待仲裁"
		}
	default:
		return item, false
	}
	return item, true
}
func inboxChannel(row groupchannel.View) (inboxItem, bool) {
	item := inboxItem{ID: row.ID, Title: row.Name, Category: "anomaly", Href: "/supply/channels/" + row.ID + "/config", UpdatedAt: row.UpdatedAt}
	if row.Status == channel.StatusSuspended {
		item.Message = "渠道已被下架，请查看原因。"
		item.Blocker = true
		return item, true
	}
	if row.Status != channel.StatusListed {
		return item, false
	}
	pending, failed, eligible := 0, 0, 0
	for _, model := range row.Models {
		if !model.Enabled || model.Excluded {
			continue
		}
		eligible++
		if model.ProbeState == "pending" {
			pending++
		}
		if model.ProbeState == "unavailable" {
			failed++
		}
	}
	switch {
	case row.Reason == "no_selected_address":
		item.Message = "没有可用地址，请重新选择分组地址。"
		item.Blocker = true
	case failed > 0:
		item.Message = "有模型探测失败，请查看健康状态。"
		item.Href = "/supply/channels/" + row.ID + "/health"
		item.Blocker = !row.Available && pending == 0
	case pending > 0:
		item.Category = "progress"
		item.Message = fmt.Sprintf("模型探测 %d/%d", eligible-pending, eligible)
	case !row.Available:
		item.Message = "当前没有可调用模型，请检查配置与分组状态。"
		item.Blocker = true
	default:
		return item, false
	}
	return item, true
}
func inboxKey(row apikey.Key, now time.Time) (inboxItem, bool) {
	item := inboxItem{ID: row.ID, Title: row.Name, Category: "anomaly", Href: "/keys/" + row.ID, UpdatedAt: row.UpdatedAt}
	if row.Status != apikey.StatusEnabled {
		return item, false
	}
	if row.ExpiresAt != nil && !now.Before(*row.ExpiresAt) {
		item.Message = "Key 已过期，请更新有效期或停用。"
		return item, true
	}
	// Avoid multiplying int64 amounts when comparing the 80% threshold.
	for _, exhaustedOnly := range []bool{true, false} {
		for _, budget := range []struct {
			limit *money.Amount
			spent money.Amount
			label string
		}{{row.BudgetDaily, row.Spend.Today, "今日"}, {row.BudgetMonthly, row.Spend.Month, "本月"}, {row.BudgetTotal, row.Spend.Total, "累计"}} {
			if budget.limit != nil && *budget.limit > 0 && (budget.spent >= *budget.limit || !exhaustedOnly && budget.spent >= *budget.limit-*budget.limit/5) {
				item.Message = budget.label + "预算已使用至少 80%"
				if budget.spent >= *budget.limit {
					item.Message = budget.label + "预算已用完，调用将被拒绝。"
				}
				return item, true
			}
		}

	}
	return item, false
}
func inboxDetection(job detection.Job) (inboxItem, bool) {
	item := inboxItem{ID: job.ID, Title: "检测任务 · " + job.Target.Model, Category: "anomaly", Href: "/supply/detection/" + job.ID, UpdatedAt: job.UpdatedAt}
	if job.ExecutionActive && (job.State == "queued" || job.State == "running") {
		item.Category = "progress"
		item.Message = fmt.Sprintf("检测进行中 %d/%d", min(len(job.Results), len(job.Plan.Items)), len(job.Plan.Items))
		return item, true
	}
	switch job.State {
	case "cancelled":
		return item, false
	case "failed":
		item.Message = "检测器异常，请查看结果后重试。"
	case "interrupted":
		item.Message = "检测已中断，请查看并继续。"
	case "suite_pending":
		item.Message = "当前检测套件尚不可执行。"
	default:
		for _, result := range job.Results {
			if result.Reason == "detector_error" {
				item.Message = "检测存在异常，请查看结果后重试。"
				return item, true
			}
		}
		return item, false
	}
	return item, true
}
func inboxContribution(detail contribution.Detail) (inboxItem, bool) {
	draft := detail.Draft
	item := inboxItem{ID: draft.ID, Title: draft.Title, Category: "action", Href: "/supply/contributions/" + draft.ID, UpdatedAt: draft.UpdatedAt}
	if draft.State == "rejected" {
		item.Message = "贡献被驳回，请查看原因并修订。"
		return item, true
	}
	switch detail.Stage {
	case "ready":
		item.Message = "内容与检测已就绪，可以提交。"
	case "conflict":
		item.Message = "生效内容已变化，请处理版本冲突。"
	case "invalid", "supplier_has_groups":
		item.Message = "内容暂不符合提交条件，请修正。"
	case "submitted":
		item.Category = "progress"
		item.Message = "已提交，等待审核。"
	case "publication_pending":
		item.Category = "progress"
		item.Message = "已批准，发布待完成。"
	case "detecting":
		item.Category = "progress"
		item.Message = "关联检测进行中。"
	default:
		return item, false
	}
	return item, true
}
