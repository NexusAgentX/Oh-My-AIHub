package c2c

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/identity"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

type Service struct {
	store   Store
	keyring *Keyring
	now     func() time.Time
}

func NewService(store Store, keyring *Keyring) (*Service, error) {
	return NewServiceWithClock(store, keyring, time.Now)
}

func NewServiceWithClock(store Store, keyring *Keyring, now func() time.Time) (*Service, error) {
	if store == nil || keyring == nil || now == nil {
		return nil, ErrInvalidInput
	}
	return &Service{store: store, keyring: keyring, now: now}, nil
}

func (s *Service) ValidateEncryptedInventory(ctx context.Context) error {
	targets, err := s.store.EncryptionTargets(ctx)
	if err != nil {
		return err
	}
	for _, target := range targets {
		if _, err := s.keyring.Decrypt(target.RecordID, target.Purpose, target.Encrypted); err != nil {
			return fmt.Errorf("validate C2C encrypted record %s: %w", target.RecordID, err)
		}
	}
	return nil
}

func (s *Service) Market(ctx context.Context, actor identity.Account) (Market, error) {
	if !readyActor(actor) {
		return Market{}, ErrForbidden
	}
	_, _ = s.store.ExpireDue(ctx, s.now().UTC(), 100)
	return s.store.Market(ctx)
}

func (s *Service) Order(ctx context.Context, actor identity.Account, orderID string) (Order, error) {
	if !readyActor(actor) || strings.TrimSpace(orderID) == "" {
		return Order{}, ErrForbidden
	}
	order, err := s.store.Order(ctx, orderID)
	if err != nil {
		return Order{}, err
	}
	allowPrivate := actor.IsAdmin || actor.ID == order.OwnerAccountID || (order.Status == OrderOpen && order.Available > 0)
	if !allowPrivate {
		allowPrivate, err = s.store.OrderParticipant(ctx, order.ID, actor.ID)
		if err != nil {
			return Order{}, err
		}
	}
	if !allowPrivate {
		order.PaymentMethods = nil
		return order, nil
	}
	if err := s.decryptPaymentMethods(&order); err != nil {
		return Order{}, err
	}
	return order, nil
}

func (s *Service) PaymentQR(ctx context.Context, actor identity.Account, orderID, methodID string) (SanitizedImage, error) {
	if !readyActor(actor) || strings.TrimSpace(orderID) == "" || strings.TrimSpace(methodID) == "" {
		return SanitizedImage{}, ErrForbidden
	}
	order, err := s.store.Order(ctx, orderID)
	if err != nil {
		return SanitizedImage{}, err
	}
	allowPrivate := actor.IsAdmin || actor.ID == order.OwnerAccountID || (order.Status == OrderOpen && order.Available > 0)
	if !allowPrivate {
		allowPrivate, err = s.store.OrderParticipant(ctx, order.ID, actor.ID)
		if err != nil {
			return SanitizedImage{}, err
		}
	}
	if !allowPrivate {
		return SanitizedImage{}, ErrNotFound
	}
	for _, method := range order.PaymentMethods {
		if method.ID != methodID {
			continue
		}
		plaintext, err := s.keyring.Decrypt(method.ID, "payment_method", method.Private)
		if err != nil {
			return SanitizedImage{}, err
		}
		var private PaymentPrivate
		if err := json.Unmarshal(plaintext, &private); err != nil || len(private.QRBytes) == 0 || (private.QRMIME != "image/jpeg" && private.QRMIME != "image/png") {
			return SanitizedImage{}, ErrNotFound
		}
		return SanitizedImage{MIME: private.QRMIME, Bytes: private.QRBytes, SHA256: sha256.Sum256(private.QRBytes)}, nil
	}
	return SanitizedImage{}, ErrNotFound
}

func (s *Service) Trade(ctx context.Context, actor identity.Account, tradeID string) (Trade, error) {
	if !readyActor(actor) || strings.TrimSpace(tradeID) == "" {
		return Trade{}, ErrForbidden
	}
	_, _ = s.store.ExpireDue(ctx, s.now().UTC(), 100)
	trade, err := s.store.Trade(ctx, tradeID)
	if err != nil {
		return Trade{}, err
	}
	if !actor.IsAdmin && actor.ID != trade.BuyerAccountID && actor.ID != trade.SellerAccountID {
		return Trade{}, ErrNotFound
	}
	if trade.SelectedPaymentMethod != nil {
		order := Order{PaymentMethods: []PaymentMethod{*trade.SelectedPaymentMethod}}
		if err := s.decryptPaymentMethods(&order); err != nil {
			return Trade{}, err
		}
		trade.SelectedPaymentMethod = &order.PaymentMethods[0]
	}
	if len(trade.PaymentReferenceData.Ciphertext) > 0 {
		plaintext, err := s.keyring.Decrypt(trade.ID, "payment_reference", trade.PaymentReferenceData)
		if err != nil {
			return Trade{}, err
		}
		trade.PaymentReference = string(plaintext)
	}
	trade.PaymentReferenceData = EncryptedValue{}
	for index := range trade.Statements {
		statement := &trade.Statements[index]
		if statement.DeletedAt == nil && len(statement.Encrypted.Ciphertext) > 0 {
			plaintext, err := s.keyring.Decrypt(statement.ID, "dispute_statement", statement.Encrypted)
			if err != nil {
				return Trade{}, err
			}
			statement.Text = string(plaintext)
		}
		statement.Encrypted = EncryptedValue{}
	}
	for index := range trade.Evidence {
		trade.Evidence[index].Encrypted = EncryptedValue{}
	}
	return trade, nil
}

func (s *Service) MyActivity(ctx context.Context, actor identity.Account) ([]Order, []Trade, error) {
	if !readyActor(actor) {
		return nil, nil, ErrForbidden
	}
	_, _ = s.store.ExpireDue(ctx, s.now().UTC(), 100)
	return s.store.MyActivity(ctx, actor.ID)
}

func (s *Service) AdminDisputes(ctx context.Context, actor identity.Account) ([]Trade, error) {
	if !readyActor(actor) || !actor.IsAdmin {
		return nil, ErrForbidden
	}
	return s.store.AdminDisputes(ctx)
}

func (s *Service) EvidenceFile(ctx context.Context, actor identity.Account, evidenceID string) (Evidence, []byte, error) {
	if !readyActor(actor) {
		return Evidence{}, nil, ErrForbidden
	}
	evidence, err := s.store.Evidence(ctx, evidenceID)
	if err != nil || evidence.DeletedAt != nil {
		return Evidence{}, nil, ErrNotFound
	}
	trade, err := s.store.Trade(ctx, evidence.TradeID)
	if err != nil {
		return Evidence{}, nil, err
	}
	if !actor.IsAdmin && actor.ID != trade.BuyerAccountID && actor.ID != trade.SellerAccountID {
		return Evidence{}, nil, ErrNotFound
	}
	plaintext, err := s.keyring.Decrypt(evidence.ID, evidencePurpose(evidence.Kind), evidence.Encrypted)
	if err != nil {
		return Evidence{}, nil, err
	}
	evidence.Encrypted = EncryptedValue{}
	return evidence, plaintext, nil
}

func (s *Service) CreateOrder(ctx context.Context, actor identity.Account, key string, side Side, unitPriceFen int64, total, minimum, maximum money.Amount, methods []PaymentMethodInput) (Order, error) {
	if !readyActor(actor) || !validKey(key) || (side != SideSell && side != SideBuy) || unitPriceFen <= 0 || total <= 0 || minimum <= 0 || maximum < minimum || maximum > total || len(methods) < 1 || len(methods) > MaximumMethods {
		return Order{}, ErrInvalidInput
	}
	normalized := make([]PaymentMethodInput, len(methods))
	for index, method := range methods {
		method.Contact = strings.TrimSpace(method.Contact)
		method.Instructions = strings.TrimSpace(method.Instructions)
		if method.QR != nil {
			clean, err := normalizeImage(method.QR)
			if err != nil {
				return Order{}, err
			}
			method.QR = &clean
		}
		if !validPaymentType(method.Type) || len(method.Contact) > 256 || len(method.Instructions) > 1_000 || (method.Contact == "" && method.Instructions == "" && method.QR == nil) || (side == SideBuy && method.Contact == "") {
			return Order{}, ErrInvalidInput
		}
		normalized[index] = method
	}
	payload := orderPayloadForHash(side, unitPriceFen, total, minimum, maximum, normalized)
	command, err := s.command(actor, "c2c.order.create", key, payload)
	if err != nil {
		return Order{}, err
	}
	orderID, err := newID()
	if err != nil {
		return Order{}, err
	}
	createdMethods := make([]PaymentMethod, 0, len(normalized))
	for index, method := range normalized {
		methodID, err := newID()
		if err != nil {
			return Order{}, err
		}
		private := PaymentPrivate{Contact: method.Contact, Instructions: method.Instructions}
		if method.QR != nil {
			private.QRMIME, private.QRBytes = method.QR.MIME, append([]byte(nil), method.QR.Bytes...)
		}
		encoded, err := json.Marshal(private)
		if err != nil {
			return Order{}, err
		}
		encrypted, err := s.keyring.Encrypt(methodID, "payment_method", encoded)
		if err != nil {
			return Order{}, err
		}
		createdMethods = append(createdMethods, PaymentMethod{
			ID: methodID, OrderID: orderID, Type: method.Type, Position: index + 1,
			Private: encrypted, QRAvailable: method.QR != nil,
		})
	}
	return s.store.CreateOrder(ctx, command, NewOrder{
		ID: orderID, Side: side, UnitPriceFen: unitPriceFen, Total: total,
		Minimum: minimum, Maximum: maximum, PaymentMethods: createdMethods,
	})
}

func (s *Service) TakeOrder(ctx context.Context, actor identity.Account, key, orderID string, quantity money.Amount, paymentMethodID string) (Trade, error) {
	if !readyActor(actor) || !validKey(key) || strings.TrimSpace(orderID) == "" || quantity <= 0 {
		return Trade{}, ErrInvalidInput
	}
	payload := struct {
		OrderID, PaymentMethodID string
		Quantity                 money.Amount
	}{strings.TrimSpace(orderID), strings.TrimSpace(paymentMethodID), quantity}
	command, err := s.command(actor, "c2c.order.take", key, payload)
	if err != nil {
		return Trade{}, err
	}
	tradeID, err := newID()
	if err != nil {
		return Trade{}, err
	}
	return s.store.TakeOrder(ctx, command, payload.OrderID, NewTrade{
		ID: tradeID, Quantity: quantity, PaymentMethodID: payload.PaymentMethodID,
		PaymentDeadline: command.Now.Add(PaymentWindow),
	})
}

func (s *Service) CancelOrder(ctx context.Context, actor identity.Account, key, orderID string) (Order, error) {
	command, err := s.simpleCommand(actor, "c2c.order.cancel", key, strings.TrimSpace(orderID))
	if err != nil {
		return Order{}, err
	}
	return s.store.CancelOrder(ctx, command, strings.TrimSpace(orderID))
}

func (s *Service) AdminCancelOrder(ctx context.Context, actor identity.Account, key, orderID, reason string) (Order, error) {
	reason = strings.TrimSpace(reason)
	if !readyActor(actor) || !actor.IsAdmin || reason == "" || len(reason) > 512 || strings.TrimSpace(orderID) == "" {
		return Order{}, ErrInvalidInput
	}
	payload := struct{ OrderID, Reason string }{strings.TrimSpace(orderID), reason}
	command, err := s.command(actor, "c2c.order.admin_cancel", key, payload)
	if err != nil {
		return Order{}, err
	}
	return s.store.AdminCancelOrder(ctx, command, payload.OrderID, reason)
}

func (s *Service) MarkPaid(ctx context.Context, actor identity.Account, key, tradeID, paymentReference string, screenshot *SanitizedImage) (Trade, error) {
	paymentReference = strings.TrimSpace(paymentReference)
	if len([]rune(paymentReference)) > 256 {
		return Trade{}, ErrInvalidInput
	}
	if screenshot != nil {
		clean, err := normalizeImage(screenshot)
		if err != nil {
			return Trade{}, err
		}
		screenshot = &clean
	}
	payload := struct {
		TradeID          string
		PaymentReference string
		ScreenshotHash   string
	}{strings.TrimSpace(tradeID), paymentReference, imageHash(screenshot)}
	command, err := s.command(actor, "c2c.trade.paid", key, payload)
	if err != nil {
		return Trade{}, err
	}
	var evidence *NewEvidence
	if screenshot != nil {
		created, err := s.encryptEvidence(EvidencePayment, screenshot)
		if err != nil {
			return Trade{}, err
		}
		evidence = &created
	}
	var encryptedReference *EncryptedValue
	if paymentReference != "" {
		encrypted, err := s.keyring.Encrypt(payload.TradeID, "payment_reference", []byte(paymentReference))
		if err != nil {
			return Trade{}, err
		}
		encryptedReference = &encrypted
	}
	return s.store.MarkPaid(ctx, command, payload.TradeID, evidence, encryptedReference, len([]rune(paymentReference)))
}

func (s *Service) CancelTrade(ctx context.Context, actor identity.Account, key, tradeID string) (Trade, error) {
	command, err := s.simpleCommand(actor, "c2c.trade.cancel", key, strings.TrimSpace(tradeID))
	if err != nil {
		return Trade{}, err
	}
	return s.store.CancelTrade(ctx, command, strings.TrimSpace(tradeID), false)
}

func (s *Service) ConfirmReceipt(ctx context.Context, actor identity.Account, key, tradeID string) (Trade, error) {
	command, err := s.simpleCommand(actor, "c2c.trade.release", key, strings.TrimSpace(tradeID))
	if err != nil {
		return Trade{}, err
	}
	return s.store.ConfirmReceipt(ctx, command, strings.TrimSpace(tradeID))
}

func (s *Service) OpenDispute(ctx context.Context, actor identity.Account, key, tradeID, statement string, images []SanitizedImage) (Trade, error) {
	return s.submitDisputeEvidence(ctx, actor, key, "c2c.trade.dispute", tradeID, statement, images, true)
}

func (s *Service) AddDisputeEvidence(ctx context.Context, actor identity.Account, key, tradeID, statement string, images []SanitizedImage) (Trade, error) {
	return s.submitDisputeEvidence(ctx, actor, key, "c2c.trade.dispute_evidence", tradeID, statement, images, false)
}

func (s *Service) submitDisputeEvidence(ctx context.Context, actor identity.Account, key, operation, tradeID, statement string, images []SanitizedImage, open bool) (Trade, error) {
	statement = strings.TrimSpace(statement)
	if statement == "" || len([]rune(statement)) > MaximumStatement || len(images) > MaximumDisputeFiles {
		return Trade{}, ErrInvalidInput
	}
	cleanImages := make([]SanitizedImage, len(images))
	hashes := make([]string, len(images))
	for index := range images {
		clean, err := normalizeImage(&images[index])
		if err != nil {
			return Trade{}, err
		}
		cleanImages[index] = clean
		hashes[index] = hex.EncodeToString(clean.SHA256[:])
	}
	payload := struct {
		TradeID   string
		Statement string
		Hashes    []string
	}{strings.TrimSpace(tradeID), statement, hashes}
	command, err := s.command(actor, operation, key, payload)
	if err != nil {
		return Trade{}, err
	}
	evidence := make([]NewEvidence, 0, len(cleanImages))
	for index := range cleanImages {
		created, err := s.encryptEvidence(EvidenceDispute, &cleanImages[index])
		if err != nil {
			return Trade{}, err
		}
		evidence = append(evidence, created)
	}
	statementID, err := newID()
	if err != nil {
		return Trade{}, err
	}
	encryptedStatement, err := s.keyring.Encrypt(statementID, "dispute_statement", []byte(statement))
	if err != nil {
		return Trade{}, err
	}
	createdStatement := NewStatement{ID: statementID, CharacterCount: len([]rune(statement)), Encrypted: encryptedStatement}
	if open {
		return s.store.OpenDispute(ctx, command, payload.TradeID, createdStatement, evidence)
	}
	return s.store.AddDisputeEvidence(ctx, command, payload.TradeID, createdStatement, evidence)
}

func (s *Service) ResolveDispute(ctx context.Context, actor identity.Account, key, tradeID string, action ResolutionAction, reason string) (Trade, error) {
	reason = strings.TrimSpace(reason)
	if !actor.IsAdmin || reason == "" || len(reason) > 512 || (action != ResolutionRelease && action != ResolutionReturn && action != ResolutionExtend) {
		return Trade{}, ErrInvalidInput
	}
	payload := struct {
		TradeID string
		Action  ResolutionAction
		Reason  string
	}{strings.TrimSpace(tradeID), action, reason}
	command, err := s.command(actor, "c2c.dispute.resolve", key, payload)
	if err != nil {
		return Trade{}, err
	}
	return s.store.ResolveDispute(ctx, command, payload.TradeID, action, reason, command.Now)
}

func (s *Service) ExpireDue(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 1_000 {
		return 0, ErrInvalidInput
	}
	return s.store.ExpireDue(ctx, s.now().UTC(), limit)
}

func (s *Service) CleanupEvidence(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 1_000 {
		return 0, ErrInvalidInput
	}
	return s.store.CleanupEvidence(ctx, s.now().UTC(), limit)
}

func FiatAmountFen(quantity money.Amount, unitPriceFen int64) (int64, error) {
	if quantity <= 0 || unitPriceFen <= 0 {
		return 0, ErrInvalidInput
	}
	numerator := new(big.Int).Mul(big.NewInt(quantity.Nano()), big.NewInt(unitPriceFen))
	denominator := big.NewInt(money.Scale)
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, denominator, remainder)
	if remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() || quotient.Int64() < 1 || quotient.Int64() > math.MaxInt64 {
		return 0, ErrInvalidInput
	}
	return quotient.Int64(), nil
}

func (s *Service) command(actor identity.Account, operation, key string, payload any) (Command, error) {
	if !readyActor(actor) || !validKey(key) || strings.TrimSpace(operation) == "" {
		return Command{}, ErrInvalidInput
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Command{}, err
	}
	return Command{
		Actor: actor, Operation: operation, IdempotencyKey: strings.TrimSpace(key),
		PayloadHash: sha256.Sum256(encoded), Now: s.now().UTC(),
	}, nil
}

func (s *Service) simpleCommand(actor identity.Account, operation, key, targetID string) (Command, error) {
	if targetID == "" {
		return Command{}, ErrInvalidInput
	}
	return s.command(actor, operation, key, struct{ ID string }{targetID})
}

func (s *Service) encryptEvidence(kind EvidenceKind, image *SanitizedImage) (NewEvidence, error) {
	if image == nil || (image.MIME != "image/jpeg" && image.MIME != "image/png") || len(image.Bytes) == 0 || len(image.Bytes) > MaximumImageBytes {
		return NewEvidence{}, ErrInvalidInput
	}
	id, err := newID()
	if err != nil {
		return NewEvidence{}, err
	}
	encrypted, err := s.keyring.Encrypt(id, evidencePurpose(kind), image.Bytes)
	if err != nil {
		return NewEvidence{}, err
	}
	return NewEvidence{
		ID: id, Kind: kind, MIME: image.MIME, SizeBytes: int64(len(image.Bytes)),
		Width: image.Width, Height: image.Height, SHA256: image.SHA256, Encrypted: encrypted,
	}, nil
}

func (s *Service) decryptPaymentMethods(order *Order) error {
	for index := range order.PaymentMethods {
		method := &order.PaymentMethods[index]
		plaintext, err := s.keyring.Decrypt(method.ID, "payment_method", method.Private)
		if err != nil {
			return err
		}
		var private PaymentPrivate
		if err := json.Unmarshal(plaintext, &private); err != nil {
			return fmt.Errorf("decode payment method: %w", err)
		}
		method.Contact = private.Contact
		method.Instructions = private.Instructions
		method.QRAvailable = len(private.QRBytes) > 0
		method.Private = EncryptedValue{}
	}
	return nil
}

func normalizeImage(image *SanitizedImage) (SanitizedImage, error) {
	if image == nil || len(image.Bytes) == 0 {
		return SanitizedImage{}, ErrInvalidInput
	}
	return SanitizeImage(bytes.NewReader(image.Bytes))
}

func newID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

func readyActor(actor identity.Account) bool {
	return actor.ID != "" && actor.Status == identity.StatusActive && !actor.MustChangePassword
}

func validKey(key string) bool {
	length := len(strings.TrimSpace(key))
	return length >= 1 && length <= 128
}

func validPaymentType(value PaymentMethodType) bool {
	return value == PaymentWeChat || value == PaymentAlipay || value == PaymentBankTransfer || value == PaymentOther
}

func evidencePurpose(kind EvidenceKind) string { return "evidence:" + string(kind) }

func imageHash(image *SanitizedImage) string {
	if image == nil {
		return ""
	}
	return hex.EncodeToString(image.SHA256[:])
}

func orderPayloadForHash(side Side, unitPriceFen int64, total, minimum, maximum money.Amount, methods []PaymentMethodInput) any {
	type methodHash struct {
		Type, Contact, Instructions, QRHash string
	}
	items := make([]methodHash, len(methods))
	for index, method := range methods {
		items[index] = methodHash{string(method.Type), method.Contact, method.Instructions, imageHash(method.QR)}
	}
	return struct {
		Side                    Side
		UnitPriceFen            int64
		Total, Minimum, Maximum money.Amount
		Methods                 []methodHash
	}{side, unitPriceFen, total, minimum, maximum, items}
}
