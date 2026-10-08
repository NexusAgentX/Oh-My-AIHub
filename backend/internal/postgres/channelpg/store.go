// Package channelpg is the PostgreSQL implementation of channel.Store.
package channelpg

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/audit"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/localtime"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/auditpg"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/pgkit"
)

type Store struct {
	pool *pgxpool.Pool
	q    *Queries
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool, q: New(pool)} }

type headerRulesJSON struct {
	Set []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"set"`
	Remove []string `json:"remove"`
}

func encodeRules(rules channel.HeaderRules) ([]byte, error) {
	encoded := headerRulesJSON{Remove: rules.Remove}
	if encoded.Remove == nil {
		encoded.Remove = []string{}
	}
	encoded.Set = make([]struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}, 0, len(rules.Set))
	for _, rule := range rules.Set {
		encoded.Set = append(encoded.Set, struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}{rule.Name, rule.Value})
	}
	return json.Marshal(encoded)
}

func decodeRules(raw []byte) channel.HeaderRules {
	var decoded headerRulesJSON
	_ = json.Unmarshal(raw, &decoded)
	rules := channel.HeaderRules{Remove: decoded.Remove}
	for _, rule := range decoded.Set {
		rules.Set = append(rules.Set, channel.HeaderSet{Name: rule.Name, Value: rule.Value})
	}
	return rules
}

func toChannel(row GetChannelRow) channel.Channel {
	return channel.Channel{
		ID: row.ID, Owner: channel.Owner{ID: row.OwnerID, Username: row.OwnerUsername, DisplayName: row.OwnerDisplayName},
		Name: row.Name, BaseURL: row.BaseUrl, Status: channel.Status(row.Status), SuspendedReason: row.SuspendedReason,
		Advanced: channel.Advanced{
			UserAgent: row.UserAgent, HeaderRules: decodeRules(row.HeaderRules),
			ConcurrencyLimit: row.ConcurrencyLimit, RPMLimit: row.RpmLimit, DailyRevenueCap: row.DailyRevenueCapNano,
			TTFTTimeoutMS: row.TtftTimeoutMs, TotalTimeoutMS: row.TotalTimeoutMs,
			CooldownFailures: row.CooldownFailures, CooldownSeconds: row.CooldownSeconds,
		},
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func toModel(row ChannelModel) channel.Model {
	model := channel.Model{
		ModelID: row.ModelID, UpstreamModel: row.UpstreamModel, MultiplierNano: row.MultiplierNano,
		Enabled: row.Enabled, FormatTests: map[channel.Format]channel.FormatTest{},
	}
	for _, format := range row.Formats {
		model.Formats = append(model.Formats, channel.Format(format))
	}
	var tests map[string]channel.FormatTest
	if json.Unmarshal(row.FormatTests, &tests) == nil {
		for format, test := range tests {
			model.FormatTests[channel.Format(format)] = test
		}
	}
	return model
}

func formatStrings(formats []channel.Format) []string {
	result := make([]string, 0, len(formats))
	for _, format := range formats {
		result = append(result, string(format))
	}
	return result
}

// hydrate attaches models and today statistics to channels.
func hydrate(ctx context.Context, q *Queries, channels []channel.Channel) ([]channel.Channel, error) {
	if len(channels) == 0 {
		return channels, nil
	}
	ids := make([]string, len(channels))
	for index, item := range channels {
		ids[index] = item.ID
	}
	models, err := q.ListModelsOfChannels(ctx, ids)
	if err != nil {
		return nil, err
	}
	byChannel := map[string][]channel.Model{}
	for _, row := range models {
		byChannel[row.ChannelID] = append(byChannel[row.ChannelID], toModel(row))
	}
	stats, err := q.ChannelTodayStats(ctx, ChannelTodayStatsParams{ChannelIds: ids, Since: localtime.DayStart(time.Now())})
	if err != nil {
		return nil, err
	}
	today := map[string]channel.Today{}
	for _, row := range stats {
		if row.ChannelID == nil {
			continue
		}
		entry := channel.Today{Revenue: money.FromNano(row.RevenueNano), Calls: row.Calls}
		if row.Calls > 0 {
			rate := float64(row.OkCalls) / float64(row.Calls)
			entry.SuccessRate = &rate
		}
		today[*row.ChannelID] = entry
	}
	for index := range channels {
		channels[index].Models = byChannel[channels[index].ID]
		channels[index].Today = today[channels[index].ID]
	}
	return channels, nil
}

func get(ctx context.Context, q *Queries, id string) (channel.Channel, error) {
	row, err := q.GetChannel(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return channel.Channel{}, channel.ErrNotFound
	}
	if err != nil {
		return channel.Channel{}, err
	}
	hydrated, err := hydrate(ctx, q, []channel.Channel{toChannel(row)})
	if err != nil {
		return channel.Channel{}, err
	}
	return hydrated[0], nil
}

func (s *Store) Get(ctx context.Context, id string) (channel.Channel, error) {
	return get(ctx, s.q, id)
}

func insertModels(ctx context.Context, q *Queries, channelID string, models []channel.Model, previous map[string]channel.Model) error {
	for _, model := range models {
		tests := map[string]channel.FormatTest{}
		if old, ok := previous[model.ModelID]; ok {
			for format, test := range old.FormatTests {
				tests[string(format)] = test
			}
		}
		encoded, err := json.Marshal(tests)
		if err != nil {
			return err
		}
		if err := q.InsertChannelModel(ctx, InsertChannelModelParams{
			ChannelID: channelID, ModelID: model.ModelID, UpstreamModel: model.UpstreamModel, MultiplierNano: model.MultiplierNano,
			Formats: formatStrings(model.Formats), FormatTests: encoded, Enabled: model.Enabled,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Create(ctx context.Context, in channel.NewChannel) (channel.Channel, error) {
	rules, err := encodeRules(in.Advanced.HeaderRules)
	if err != nil {
		return channel.Channel{}, err
	}
	var created channel.Channel
	err = pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := New(tx)
		if err := q.InsertChannel(ctx, InsertChannelParams{
			ID: in.ID, OwnerID: in.OwnerID, Name: in.Name, BaseUrl: in.BaseURL,
			ApiKeyCiphertext: in.Credential.Ciphertext, ApiKeyNonce: in.Credential.Nonce, ApiKeyKeyID: in.Credential.KeyID,
			Status: string(in.Status), UserAgent: in.Advanced.UserAgent, HeaderRules: rules,
			ConcurrencyLimit: in.Advanced.ConcurrencyLimit, RpmLimit: in.Advanced.RPMLimit, DailyRevenueCapNano: in.Advanced.DailyRevenueCap,
			TtftTimeoutMs: in.Advanced.TTFTTimeoutMS, TotalTimeoutMs: in.Advanced.TotalTimeoutMS,
			CooldownFailures: in.Advanced.CooldownFailures, CooldownSeconds: in.Advanced.CooldownSeconds,
		}); err != nil {
			return err
		}
		if err := insertModels(ctx, q, in.ID, in.Models, nil); err != nil {
			return err
		}
		if err := q.InsertChannelEvent(ctx, InsertChannelEventParams{ChannelID: in.ID, Kind: statusEvent(in.Status)}); err != nil {
			return err
		}
		created, err = get(ctx, q, in.ID)
		return err
	})
	return created, err
}

func statusEvent(status channel.Status) string {
	if status == channel.StatusListed {
		return channel.EventListed
	}
	return channel.EventUnlisted
}

func (s *Store) ListByOwner(ctx context.Context, ownerID string) ([]channel.Channel, error) {
	rows, err := s.q.ListOwnerChannels(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	channels := make([]channel.Channel, 0, len(rows))
	for _, row := range rows {
		channels = append(channels, toChannel(GetChannelRow(row)))
	}
	return hydrate(ctx, s.q, channels)
}

func (s *Store) ListAll(ctx context.Context, filter channel.AdminFilter) ([]channel.Channel, error) {
	params := ListAdminChannelsParams{
		Status: string(filter.Status), OwnerID: filter.OwnerID, Query: filter.Query,
		BeforeID: "00000000-0000-0000-0000-000000000000", RowLimit: int32(filter.Limit),
	}
	if filter.AfterKey != "" {
		stamp, id, ok := strings.Cut(filter.AfterKey, "|")
		parsed, err := time.Parse(time.RFC3339Nano, stamp)
		if !ok || err != nil {
			return nil, channel.ErrInvalidInput
		}
		params.BeforeTime, params.BeforeID = &parsed, id
	}
	rows, err := s.q.ListAdminChannels(ctx, params)
	if err != nil {
		return nil, err
	}
	channels := make([]channel.Channel, 0, len(rows))
	for _, row := range rows {
		channels = append(channels, toChannel(GetChannelRow(row)))
	}
	return hydrate(ctx, s.q, channels)
}

func (s *Store) Update(ctx context.Context, id string, update channel.Update) (channel.Channel, error) {
	var updated channel.Channel
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := New(tx)
		current, err := q.GetChannelForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return channel.ErrNotFound
		}
		if err != nil {
			return err
		}
		var status *string
		if update.Status != nil {
			if current == string(channel.StatusSuspended) {
				return channel.ErrSuspended
			}
			value := string(*update.Status)
			status = &value
		}
		if err := q.UpdateChannelFields(ctx, UpdateChannelFieldsParams{ID: id, Name: update.Name, BaseUrl: update.BaseURL, Status: status}); err != nil {
			return err
		}
		if status != nil && *status != current {
			if err := q.InsertChannelEvent(ctx, InsertChannelEventParams{ChannelID: id, Kind: statusEvent(*update.Status)}); err != nil {
				return err
			}
		}
		if update.Credential != nil {
			if err := q.UpdateChannelCredential(ctx, UpdateChannelCredentialParams{
				ID: id, ApiKeyCiphertext: update.Credential.Ciphertext, ApiKeyNonce: update.Credential.Nonce, ApiKeyKeyID: update.Credential.KeyID,
			}); err != nil {
				return err
			}
		}
		if update.Advanced != nil {
			advanced := update.Advanced
			rules, err := encodeRules(advanced.HeaderRules)
			if err != nil {
				return err
			}
			if err := q.UpdateChannelAdvanced(ctx, UpdateChannelAdvancedParams{
				ID: id, UserAgent: advanced.UserAgent, HeaderRules: rules, ConcurrencyLimit: advanced.ConcurrencyLimit,
				RpmLimit: advanced.RPMLimit, DailyRevenueCapNano: advanced.DailyRevenueCap, TtftTimeoutMs: advanced.TTFTTimeoutMS,
				TotalTimeoutMs: advanced.TotalTimeoutMS, CooldownFailures: advanced.CooldownFailures, CooldownSeconds: advanced.CooldownSeconds,
			}); err != nil {
				return err
			}
		}
		if update.Models != nil {
			existing, err := q.ListModelsOfChannels(ctx, []string{id})
			if err != nil {
				return err
			}
			previous := map[string]channel.Model{}
			for _, row := range existing {
				previous[row.ModelID] = toModel(row)
			}
			if err := q.DeleteChannelModels(ctx, id); err != nil {
				return err
			}
			if err := insertModels(ctx, q, id, *update.Models, previous); err != nil {
				return err
			}
		}
		updated, err = get(ctx, q, id)
		return err
	})
	return updated, err
}

func (s *Store) SoftDelete(ctx context.Context, id string) error {
	affected, err := s.q.SoftDeleteChannel(ctx, id)
	if err != nil {
		return err
	}
	if affected == 0 {
		return channel.ErrNotFound
	}
	return nil
}

func (s *Store) Credential(ctx context.Context, id string) (channel.EncryptedCredential, error) {
	row, err := s.q.GetChannelCredential(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return channel.EncryptedCredential{}, channel.ErrNotFound
	}
	if err != nil {
		return channel.EncryptedCredential{}, err
	}
	return channel.EncryptedCredential{Version: 1, KeyID: row.ApiKeyKeyID, Nonce: row.ApiKeyNonce, Ciphertext: row.ApiKeyCiphertext}, nil
}

func (s *Store) Events(ctx context.Context, id string, limit int) ([]channel.Event, error) {
	rows, err := s.q.ListChannelEvents(ctx, ListChannelEventsParams{ChannelID: id, RowLimit: int32(limit)})
	if err != nil {
		return nil, err
	}
	events := make([]channel.Event, 0, len(rows))
	for _, row := range rows {
		events = append(events, channel.Event{ID: row.ID, Kind: row.Kind, Reason: row.Reason, CreatedAt: row.CreatedAt})
	}
	return events, nil
}

func (s *Store) RecordEvent(ctx context.Context, id, kind, reason string) error {
	return s.q.InsertChannelEvent(ctx, InsertChannelEventParams{ChannelID: id, Kind: kind, Reason: reason})
}

// RecordEvent writes a channel event through db, normally a transaction the
// gateway already owns.
func RecordEvent(ctx context.Context, db DBTX, id, kind, reason string) error {
	return New(db).InsertChannelEvent(ctx, InsertChannelEventParams{ChannelID: id, Kind: kind, Reason: reason})
}

func (s *Store) SaveTests(ctx context.Context, id string, results []channel.TestOutcome, testedAt time.Time, apply bool) (channel.Channel, error) {
	var updated channel.Channel
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := New(tx)
		if _, err := q.GetChannelForUpdate(ctx, id); errors.Is(err, pgx.ErrNoRows) {
			return channel.ErrNotFound
		} else if err != nil {
			return err
		}
		rows, err := q.ListModelsOfChannels(ctx, []string{id})
		if err != nil {
			return err
		}
		byModel := map[string][]channel.TestOutcome{}
		for _, result := range results {
			byModel[result.ModelID] = append(byModel[result.ModelID], result)
		}
		for _, row := range rows {
			outcomes := byModel[row.ModelID]
			if len(outcomes) == 0 {
				continue
			}
			model := toModel(row)
			tested := map[channel.Format]bool{}
			var passed []channel.Format
			for _, outcome := range outcomes {
				tested[outcome.Format] = true
				model.FormatTests[outcome.Format] = channel.FormatTest{
					OK: outcome.OK, StatusCode: outcome.StatusCode, Error: outcome.Error, DurationMS: &outcome.DurationMS, TestedAt: testedAt,
				}
				if outcome.OK {
					passed = append(passed, outcome.Format)
				}
			}
			formats := model.Formats
			if apply {
				var next []channel.Format
				for _, format := range channel.Formats {
					if slices.Contains(passed, format) || (slices.Contains(model.Formats, format) && !tested[format]) {
						next = append(next, format)
					}
				}
				if len(next) > 0 {
					formats = next
				}
			}
			tests := map[string]channel.FormatTest{}
			for format, test := range model.FormatTests {
				tests[string(format)] = test
			}
			encoded, err := json.Marshal(tests)
			if err != nil {
				return err
			}
			if err := q.SetChannelModelTests(ctx, SetChannelModelTestsParams{ChannelID: id, ModelID: row.ModelID, FormatTests: encoded, Formats: formatStrings(formats)}); err != nil {
				return err
			}
		}
		updated, err = get(ctx, q, id)
		return err
	})
	return updated, err
}

func (s *Store) Suspend(ctx context.Context, actorID, id, reason string) (channel.Channel, error) {
	return s.moderate(ctx, actorID, id, reason, true)
}

func (s *Store) Unsuspend(ctx context.Context, actorID, id, reason string) (channel.Channel, error) {
	return s.moderate(ctx, actorID, id, reason, false)
}

func (s *Store) moderate(ctx context.Context, actorID, id, reason string, suspend bool) (channel.Channel, error) {
	var updated channel.Channel
	err := pgkit.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := New(tx)
		current, err := q.GetChannelForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return channel.ErrNotFound
		}
		if err != nil {
			return err
		}
		isSuspended := current == string(channel.StatusSuspended)
		if isSuspended == suspend {
			return channel.ErrConflict
		}
		kind, action := channel.EventUnsuspended, audit.ActionChannelUnsuspended
		if suspend {
			kind, action = channel.EventSuspended, audit.ActionChannelSuspended
			err = q.SuspendChannel(ctx, SuspendChannelParams{ID: id, SuspendedReason: &reason})
		} else {
			err = q.UnsuspendChannel(ctx, id)
		}
		if err != nil {
			return err
		}
		if err := q.InsertChannelEvent(ctx, InsertChannelEventParams{ChannelID: id, Kind: kind, Reason: reason}); err != nil {
			return err
		}
		if err := auditpg.Record(ctx, tx, auditpg.Event{ActorID: actorID, Action: action, TargetType: "channel", TargetID: id, Reason: reason}); err != nil {
			return err
		}
		updated, err = get(ctx, q, id)
		return err
	})
	return updated, err
}
