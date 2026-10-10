package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/apikey"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/routing"
)

// nullableAmount renders an optional amount as a decimal string, or null when unset.
func nullableAmount(value *money.Amount) *string {
	if value == nil {
		return nil
	}
	text := value.String()
	return &text
}

// apiKeySpendJSON is the OpenAPI ApiKeySpend schema.
type apiKeySpendJSON struct {
	Today string `json:"today"`
	Month string `json:"month"`
	Total string `json:"total"`
}

// apiKeyRefJSON is the OpenAPI KeyRef schema. It is not named keyRefJSON because observe.go
// still uses that name for its map-based helper.
type apiKeyRefJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// newApiKeyRefJSON references the API key a ledger entry or call belongs to: nil when the key is
// unknown (id is nil), with an empty name when only the id is known.
func newApiKeyRefJSON(id, name *string) *apiKeyRefJSON {
	if id == nil {
		return nil
	}
	ref := apiKeyRefJSON{ID: *id}
	if name != nil {
		ref.Name = *name
	}
	return &ref
}

// apiKeyJSON is the OpenAPI ApiKey schema.
type apiKeyJSON struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Prefix        string            `json:"prefix"`
	Status        apikey.Status     `json:"status"`
	IsDefault     bool              `json:"is_default"`
	ExpiresAt     *time.Time        `json:"expires_at"`
	AllowedModels []string          `json:"allowed_models"`
	BudgetDaily   *string           `json:"budget_daily"`
	BudgetMonthly *string           `json:"budget_monthly"`
	BudgetTotal   *string           `json:"budget_total"`
	ModelAliases  map[string]string `json:"model_aliases"`
	RoutedModels  []string          `json:"routed_models"`
	Spend         apiKeySpendJSON   `json:"spend"`
	LastUsedAt    *time.Time        `json:"last_used_at"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

// apiKeyListJSON is the OpenAPI ApiKeyList schema.
type apiKeyListJSON struct {
	Items []apiKeyJSON `json:"items"`
}

// apiKeyEnvelopeJSON is the OpenAPI ApiKeyEnvelope schema.
type apiKeyEnvelopeJSON struct {
	Key apiKeyJSON `json:"key"`
}

// apiKeyCreatedJSON is the OpenAPI ApiKeyCreated schema.
type apiKeyCreatedJSON struct {
	Key    apiKeyJSON `json:"key"`
	Secret string     `json:"secret"`
}

// apiKeyDetailJSON is the OpenAPI ApiKeyDetail schema.
type apiKeyDetailJSON struct {
	Key     apiKeyJSON              `json:"key"`
	Routing []routingPreferenceJSON `json:"routing"`
}

// apiKeySecretJSON is the OpenAPI ApiKeySecret schema.
type apiKeySecretJSON struct {
	Secret string `json:"secret"`
}

// routingPreferenceJSON is the OpenAPI RoutingPreference schema.
type routingPreferenceJSON struct {
	ModelID       string         `json:"model_id"`
	Source        routing.Source `json:"source"`
	Mode          routing.Mode   `json:"mode"`
	Order         []string       `json:"order"`
	Excluded      []string       `json:"excluded"`
	MaxAttempts   *int32         `json:"max_attempts"`
	TTFTTimeoutMS *int32         `json:"ttft_timeout_ms"`
	UpdatedAt     *time.Time     `json:"updated_at"`
}

func newApiKeyJSON(key apikey.Key) apiKeyJSON {
	return apiKeyJSON{
		ID:            key.ID,
		Name:          key.Name,
		Prefix:        key.Prefix,
		Status:        key.Status,
		IsDefault:     key.IsDefault,
		ExpiresAt:     key.ExpiresAt,
		AllowedModels: key.AllowedModels,
		BudgetDaily:   nullableAmount(key.BudgetDaily),
		BudgetMonthly: nullableAmount(key.BudgetMonthly),
		BudgetTotal:   nullableAmount(key.BudgetTotal),
		ModelAliases:  key.ModelAliases,
		RoutedModels:  key.RoutedModels,
		Spend:         apiKeySpendJSON{Today: key.Spend.Today.String(), Month: key.Spend.Month.String(), Total: key.Spend.Total.String()},
		LastUsedAt:    key.LastUsedAt,
		CreatedAt:     key.CreatedAt,
		UpdatedAt:     key.UpdatedAt,
	}
}

func newRoutingPreferenceJSON(pref routing.Pref) routingPreferenceJSON {
	order, excluded := pref.Order, pref.Excluded
	if order == nil {
		order = []string{}
	}
	if excluded == nil {
		excluded = []string{}
	}
	return routingPreferenceJSON{
		ModelID:       pref.ModelID,
		Source:        pref.Source,
		Mode:          pref.Mode,
		Order:         order,
		Excluded:      excluded,
		MaxAttempts:   pref.MaxAttempts,
		TTFTTimeoutMS: pref.TTFTTimeoutMS,
		UpdatedAt:     pref.UpdatedAt,
	}
}

type keyRequest struct {
	Name          *string             `json:"name"`
	Status        *string             `json:"status"`
	ExpiresAt     optional[time.Time] `json:"expires_at"`
	AllowedModels *[]string           `json:"allowed_models"`
	BudgetDaily   optional[string]    `json:"budget_daily"`
	BudgetMonthly optional[string]    `json:"budget_monthly"`
	BudgetTotal   optional[string]    `json:"budget_total"`
	ModelAliases  *map[string]string  `json:"model_aliases"`
}

// parseBudget turns an optional decimal string into a nullable amount.
func parseBudget(field optional[string]) (*money.Amount, error) {
	if field.Value == nil {
		return nil, nil
	}
	amount, err := money.Parse(*field.Value)
	if err != nil {
		return nil, apikey.ErrInvalidInput
	}
	return &amount, nil
}

func (a *app) listKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := a.keys.List(r.Context(), accountFromContext(r.Context()).ID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	items := make([]apiKeyJSON, 0, len(keys))
	for _, key := range keys {
		items = append(items, newApiKeyJSON(key))
	}
	writeJSON(w, http.StatusOK, apiKeyListJSON{Items: items})
}

func (a *app) createKey(w http.ResponseWriter, r *http.Request) {
	var request keyRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeInvalidJSON(w)
		return
	}
	if request.Name == nil {
		writeDomainError(w, apikey.ErrInvalidInput)
		return
	}
	input := apikey.CreateInput{Name: *request.Name, Settings: apikey.Settings{ExpiresAt: request.ExpiresAt.Value}}
	if request.Status != nil {
		input.Status = apikey.Status(*request.Status)
	}
	if request.AllowedModels != nil {
		input.AllowedModels = *request.AllowedModels
	}
	if request.ModelAliases != nil {
		input.ModelAliases = *request.ModelAliases
	}
	var err error
	if input.BudgetDaily, err = parseBudget(request.BudgetDaily); err == nil {
		if input.BudgetMonthly, err = parseBudget(request.BudgetMonthly); err == nil {
			input.BudgetTotal, err = parseBudget(request.BudgetTotal)
		}
	}
	if err != nil {
		writeDomainError(w, err)
		return
	}
	key, secret, err := a.keys.Create(r.Context(), accountFromContext(r.Context()).ID, input)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, apiKeyCreatedJSON{Key: newApiKeyJSON(key), Secret: secret})
}

func pathUUID(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	id := r.PathValue(name)
	if !uuidPattern.MatchString(id) {
		writeError(w, http.StatusNotFound, "not_found", "资源不存在")
		return "", false
	}
	return id, true
}

func (a *app) getKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "keyID")
	if !ok {
		return
	}
	ownerID := accountFromContext(r.Context()).ID
	key, err := a.keys.Get(r.Context(), ownerID, id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	prefs, err := a.routing.ListForKey(r.Context(), ownerID, id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	items := make([]routingPreferenceJSON, 0, len(prefs))
	for _, pref := range prefs {
		items = append(items, newRoutingPreferenceJSON(pref))
	}
	writeJSON(w, http.StatusOK, apiKeyDetailJSON{Key: newApiKeyJSON(key), Routing: items})
}

func (a *app) updateKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "keyID")
	if !ok {
		return
	}
	var request keyRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeInvalidJSON(w)
		return
	}
	update := apikey.Update{Name: request.Name, AllowedModels: request.AllowedModels, ModelAliases: request.ModelAliases}
	if request.Status != nil {
		status := apikey.Status(*request.Status)
		update.Status = &status
	}
	if request.ExpiresAt.Set {
		update.ExpiresAt = &request.ExpiresAt.Value
	}
	var err error
	if request.BudgetDaily.Set {
		var budget *money.Amount
		if budget, err = parseBudget(request.BudgetDaily); err == nil {
			update.BudgetDaily = &budget
		}
	}
	if err == nil && request.BudgetMonthly.Set {
		var budget *money.Amount
		if budget, err = parseBudget(request.BudgetMonthly); err == nil {
			update.BudgetMonthly = &budget
		}
	}
	if err == nil && request.BudgetTotal.Set {
		var budget *money.Amount
		if budget, err = parseBudget(request.BudgetTotal); err == nil {
			update.BudgetTotal = &budget
		}
	}
	if err != nil {
		writeDomainError(w, err)
		return
	}
	key, err := a.keys.Update(r.Context(), accountFromContext(r.Context()).ID, id, update)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiKeyEnvelopeJSON{Key: newApiKeyJSON(key)})
}

func (a *app) deleteKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "keyID")
	if !ok {
		return
	}
	if err := a.keys.Delete(r.Context(), accountFromContext(r.Context()).ID, id); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) getKeySecret(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "keyID")
	if !ok {
		return
	}
	secret, err := a.keys.Secret(r.Context(), accountFromContext(r.Context()).ID, id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiKeySecretJSON{Secret: secret})
}

type routingRequest struct {
	Mode          routing.Mode `json:"mode"`
	Order         []string     `json:"order"`
	Excluded      []string     `json:"excluded"`
	MaxAttempts   *int32       `json:"max_attempts"`
	TTFTTimeoutMS *int32       `json:"ttft_timeout_ms"`
}

// saveRouting stores the preference of the account (keyID empty) or of a key.
func (a *app) saveRouting(w http.ResponseWriter, r *http.Request, keyID string) {
	ownerID := accountFromContext(r.Context()).ID
	modelID := r.PathValue("modelID")
	var request routingRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeInvalidJSON(w)
		return
	}
	if _, err := a.catalog.Get(r.Context(), modelID); err != nil {
		writeDomainError(w, err)
		return
	}
	pref, err := routing.Validate(routing.Pref{
		ModelID: modelID, Mode: request.Mode, Order: request.Order, Excluded: request.Excluded,
		MaxAttempts: request.MaxAttempts, TTFTTimeoutMS: request.TTFTTimeoutMS,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	saved, err := a.routing.Set(r.Context(), ownerID, keyID, pref)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newRoutingPreferenceJSON(saved))
}

func (a *app) setRouting(w http.ResponseWriter, r *http.Request) {
	a.saveRouting(w, r, "")
}

func (a *app) setKeyRouting(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "keyID")
	if !ok {
		return
	}
	if _, err := a.keys.Get(r.Context(), accountFromContext(r.Context()).ID, id); err != nil {
		writeDomainError(w, err)
		return
	}
	a.saveRouting(w, r, id)
}

func (a *app) deleteKeyRouting(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r, "keyID")
	if !ok {
		return
	}
	ownerID := accountFromContext(r.Context()).ID
	if _, err := a.keys.Get(r.Context(), ownerID, id); err != nil {
		writeDomainError(w, err)
		return
	}
	if err := a.routing.Delete(r.Context(), ownerID, id, r.PathValue("modelID")); err != nil {
		if errors.Is(err, routing.ErrNotFound) {
			w.WriteHeader(http.StatusNoContent) // already following the account setting
			return
		}
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// registerKeyRoutes 注册 API Key 与路由偏好路由（Feature B）。
func (a *app) registerKeyRoutes(r *router) {
	r.implement("B", "GET /api/keys", accessReady, a.listKeys)
	r.implement("B", "POST /api/keys", accessReady, a.createKey)
	r.implement("B", "GET /api/keys/{keyID}", accessReady, a.getKey)
	r.implement("B", "PATCH /api/keys/{keyID}", accessReady, a.updateKey)
	r.implement("B", "DELETE /api/keys/{keyID}", accessReady, a.deleteKey)
	r.implement("B", "GET /api/keys/{keyID}/secret", accessReady, a.getKeySecret)
	r.implement("B", "PUT /api/routing/{modelID}", accessReady, a.setRouting)
	r.implement("B", "PUT /api/keys/{keyID}/routing/{modelID}", accessReady, a.setKeyRouting)
	r.implement("B", "DELETE /api/keys/{keyID}/routing/{modelID}", accessReady, a.deleteKeyRouting)
}
